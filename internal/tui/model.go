package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"spettro/internal/agent"
	"spettro/internal/config"
	"spettro/internal/conversation"
	"spettro/internal/provider"
	"spettro/internal/session"
	"spettro/internal/storage"
)

const coAuthorInfo = "Co-Authored-By: Spettro <spettro@eyed.to>"

// ── message types ────────────────────────────────────────────────────────────

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

type ToolItem struct {
	Name   string
	Status string // pending | running | success | error
	Args   string
	Output string
	Open   bool
}

type ChatMessage struct {
	Role     Role
	Content  string
	Thinking string // content from <think>...</think> blocks, hidden by default
	Meta     string // muted footer hint shown below assistant messages
	Kind     string // "plan" for plan messages — rendered differently
	Tools    []ToolItem
	At       time.Time
}

// ── command palette ───────────────────────────────────────────────────────────

type commandDef struct {
	name string
	desc string
}

var allCommands = []commandDef{
	{"/help", "show help"},
	{"/models", "switch model"},
	{"/connect", "connect a provider"},
	{"/mode", "cycle mode"},
	{"/approve", "execute pending plan"},
	{"/permission", "set permission level"},
	{"/budget", "set token budget per request  usage: /budget <n|0>"},
	{"/image", "attach image to next message"},
	{"/init", "analyze codebase and write SPETTRO.md"},
	{"/compact", "summarize conversation (optionally focused)"},
	{"/clear", "clear conversation history"},
	{"/resume", "resume a previous conversation"},
	{"/exit", "exit spettro"},
}

var permissionCommands = []commandDef{
	{"/permission yolo", "no approval required for any action"},
	{"/permission restricted", "ask once, remember for session"},
	{"/permission ask-first", "always ask before executing"},
}

const localConnectProviderID = "__local_endpoint__"

func filterCommands(query string) []commandDef {
	if query == "" {
		return append([]commandDef(nil), allCommands...)
	}
	q := strings.ToLower(query)
	var out []commandDef
	for _, c := range allCommands {
		if strings.Contains(c.name, q) || strings.Contains(c.desc, q) {
			out = append(out, c)
		}
	}
	return out
}

// ── tea messages ─────────────────────────────────────────────────────────────

type tickMsg time.Time

type agentDoneMsg struct {
	content    string
	meta       string // hint shown below the message
	tools      []agent.ToolTrace
	tokensUsed int
	err        error
}

type planDoneMsg struct {
	plan       string
	tools      []agent.ToolTrace
	tokensUsed int
	err        error
}

type commitDoneMsg struct {
	commitMsg string
	err       error
}

type searchDoneMsg struct {
	result string
	err    error
}

type bannerClearMsg struct{}

type quitWarningMsg struct{}

type compactDoneMsg struct {
	summary string
	err     error
}

type toolProgressMsg struct {
	trace agent.ToolTrace
}

// ── parallel agent tracking ───────────────────────────────────────────────────

type parallelAgentEntry struct {
	ID       string
	Label    string
	Kind     string
	Instance int
	Task     string
	Status   string
}

type modifiedFileEntry struct {
	Path      string
	Added     int
	Deleted   int
	Untracked bool
}

type sidePanelItem struct {
	Kind   string // agent | file
	ID     string
	Title  string
	Detail string
	Status string
}

type agentTickMsg struct{}

type shellApprovalRequestMsg struct {
	request  agent.ShellApprovalRequest
	response chan shellApprovalResponse
}

type shellApprovalResponse struct {
	decision agent.ShellApprovalDecision
	err      error
}

// ── setup state ──────────────────────────────────────────────────────────────

type setupState struct {
	step     int
	provider string
	model    string
}

// ── main model ───────────────────────────────────────────────────────────────

type Model struct {
	// terminal dimensions
	width  int
	height int
	ready  bool

	// sub-models
	vp   viewport.Model
	ta   textarea.Model
	spin spinner.Model

	// mode and config
	mode string
	cfg  config.UserConfig

	// messages
	messages []ChatMessage

	// input history (for up/down recall in composer)
	inputHistory    []string
	historyIndex    int
	historyDraft    string
	historyBrowsing bool

	// animation frame
	eyeFrame int

	// thinking indicator
	thinking bool

	// model selector dialog
	showSelector bool
	selItems     []provider.Model
	selFilter    string
	selCursor    int

	// connect provider dialog
	showConnect     bool
	connectItems    []provider.ProviderInfo
	connectFilter   string
	connectCursor   int
	connectStep     int    // 0 = pick provider, 1 = enter key/endpoint
	connectProvider string // provider ID chosen in step 0

	// command palette (shown when textarea starts with "/")
	cmdItems  []commandDef
	cmdCursor int

	// file mentions (shown when typing @path)
	repoFiles     []string
	mentionItems  []string
	mentionCursor int

	// setup wizard
	showSetup bool
	setup     setupState

	// favorites: set of "provider:model" strings
	favorites map[string]bool

	// pending state
	pendingPlan string
	pendingImgs []string

	// banner (info / error)
	banner     string
	bannerKind string // info | error | warn | success

	// quit protection: require two ctrl+c within 2 seconds
	ctrlCAt time.Time

	// trust dialog
	showTrust   bool
	trustCursor int

	// tool/thinking detail visibility (toggled with ctrl+o)
	showTools bool

	// live tool stream (populated while agent is running)
	liveTools      []ToolItem
	currentTool    *ToolItem
	toolCh         chan agent.ToolTrace
	approvalCh     chan shellApprovalRequestMsg
	cancelAgent    context.CancelFunc // non-nil while an agent goroutine is running
	pendingAuth    *shellApprovalRequestMsg
	approvalCursor int // arrow-key cursor for shell/plan approval dialogs

	// plan approval dialog (shown after plan generation)
	showPlanApproval   bool
	planApprovalCursor int

	// parallel agent tracking
	parallelAgents []parallelAgentEntry
	tickCount      int
	sideCursor     int
	sideScroll     int
	modifiedFiles  []modifiedFileEntry
	showSidePanel  bool
	sessionEdits   map[string]struct{}

	// context window tracking
	totalTokensUsed int

	// conversation persistence
	sessionID string

	// resume dialog
	showResume   bool
	resumeItems  []session.Summary
	resumeCursor int

	// session todos
	todos []session.Todo

	// app deps
	cwd       string
	store     *storage.Store
	providers *provider.Manager
	manifest  config.AgentManifest
	committer agent.CommitAgent
	searcher  agent.SearchAgent
}

// New creates a new bubbletea Model wired to all the internal services.
func New(cwd string, cfg config.UserConfig, store *storage.Store, pm *provider.Manager) Model {
	ta := textarea.New()
	ta.Placeholder = "enter message…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 8000
	ta.SetHeight(3)
	// Remove default cursor-line highlight and prompt glyph that cause
	// a black background band and a white bar on the left side.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle()
	ta.BlurredStyle.Prompt = lipgloss.NewStyle()
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorMuted)

	favs := map[string]bool{}
	for _, f := range cfg.Favorites {
		favs[f] = true
	}

	repoFiles, _ := scanRepoFiles(cwd)

	manifest, _ := config.LoadAgentManifestForProject(cwd)
	defaultMode := manifest.DefaultAgent
	if defaultMode == "" {
		defaultMode = "plan"
	}

	m := Model{
		mode:      defaultMode,
		cfg:       cfg,
		cwd:       cwd,
		store:     store,
		providers: pm,
		manifest:  manifest,
		ta:        ta,
		spin:      sp,
		favorites: favs,
		repoFiles: repoFiles,
		committer: agent.LLMCommitter{
			ProviderManager: pm,
			ProviderName:    func() string { return cfg.ActiveProvider },
			ModelName:       func() string { return cfg.ActiveModel },
		},
		searcher:     agent.RepoSearcher{},
		historyIndex: -1,
	}
	m.refreshModifiedFiles()
	return m
}

func (m Model) currentAgent() (config.AgentSpec, bool) {
	return m.manifest.AgentByID(m.mode)
}

func (m Model) currentColor() lipgloss.Color {
	if spec, ok := m.manifest.AgentByID(m.mode); ok {
		return modeColor(spec.Color)
	}
	return modeColor(m.mode)
}

// ── Init ─────────────────────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tick(),
		m.spin.Tick,
	)
}

func tick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

func agentTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return agentTickMsg{}
	})
}

// ── Update ───────────────────────────────────────────────────────────────────

// Update delegates to update() then always recomputes layout so that
// changes to thinking/banner/cmdItems are immediately reflected in viewport height.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	newModel, cmd := m.update(msg)
	if nm, ok := newModel.(Model); ok {
		nm = nm.recalcLayout()
		return nm, cmd
	}
	return newModel, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.recalcLayout()
		if !m.ready {
			m.ready = true
			// Show trust dialog if this folder hasn't been trusted yet.
			if !config.IsTrusted(m.cwd) {
				m.showTrust = true
			} else {
				m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
			}
			m.refreshViewport()
		}

	case tickMsg:
		m.eyeFrame++
		cmds = append(cmds, tick())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)

	case agentDoneMsg:
		if !m.thinking {
			break // already cancelled by the user
		}
		m.thinking = false
		m.cancelAgent = nil
		m.toolCh = nil
		m.approvalCh = nil
		m.liveTools = nil
		m.currentTool = nil
		m.pendingAuth = nil
		m.parallelAgents = nil
		m.refreshModifiedFiles()
		if msg.tokensUsed > 0 {
			m.totalTokensUsed += msg.tokensUsed
		}
		if msg.err != nil {
			m.showBanner("error: "+msg.err.Error(), "error")
		} else {
			m.syncTodosFromSession()
			main, thinking := stripThinking(msg.content)
			m.messages = append(m.messages, ChatMessage{
				Role:     RoleAssistant,
				Content:  main,
				Thinking: thinking,
				Meta:     msg.meta,
				Tools:    toToolItems(msg.tools),
				At:       time.Now(),
			})
		}
		m.refreshViewport()
		// Auto-compact when approaching context limit (85% threshold)
		if cmd := m.autoCompactIfNeeded(); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case planDoneMsg:
		if !m.thinking {
			break // already cancelled by the user
		}
		m.thinking = false
		m.cancelAgent = nil
		m.toolCh = nil
		m.approvalCh = nil
		m.liveTools = nil
		m.currentTool = nil
		m.pendingAuth = nil
		m.parallelAgents = nil
		m.refreshModifiedFiles()
		if msg.tokensUsed > 0 {
			m.totalTokensUsed += msg.tokensUsed
		}
		if msg.err != nil {
			m.showBanner("plan error: "+msg.err.Error(), "error")
		} else {
			m.syncTodosFromSession()
			m.pendingPlan = msg.plan
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleAssistant,
				Kind:    "plan",
				Content: msg.plan,
				Tools:   toToolItems(msg.tools),
				At:      time.Now(),
			})
			m.showPlanApproval = true
			m.planApprovalCursor = 0
		}
		m.refreshViewport()
		// Auto-compact when approaching context limit (85% threshold)
		if cmd := m.autoCompactIfNeeded(); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case commitDoneMsg:
		if !m.thinking {
			break
		}
		m.thinking = false
		m.cancelAgent = nil
		m.refreshModifiedFiles()
		if msg.err != nil {
			m.showBanner("commit error: "+msg.err.Error(), "error")
		} else {
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleSystem,
				Content: fmt.Sprintf("committed: %s\n\n%s", msg.commitMsg, coAuthorInfo),
				At:      time.Now(),
			})
		}
		m.refreshViewport()

	case searchDoneMsg:
		if !m.thinking {
			break
		}
		m.thinking = false
		m.cancelAgent = nil
		if msg.err != nil {
			m.showBanner("search error: "+msg.err.Error(), "error")
		} else {
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleSystem,
				Content: msg.result,
				At:      time.Now(),
			})
		}
		m.refreshViewport()

	case compactDoneMsg:
		if !m.thinking {
			break
		}
		m.thinking = false
		m.cancelAgent = nil
		if msg.err != nil {
			m.showBanner("compact error: "+msg.err.Error(), "error")
		} else {
			m.autoSave()
			m.sessionID = ""
			m.todos = nil
			m.totalTokensUsed = 0 // reset context counter after compaction
			m.messages = []ChatMessage{{
				Role:    RoleSystem,
				Content: "── conversation compacted ──\n\n" + msg.summary,
				At:      time.Now(),
			}}
		}
		m.refreshViewport()

	case agentTickMsg:
		m.tickCount++
		// Only keep ticking if there are running agents
		for _, a := range m.parallelAgents {
			if a.Status == "running" {
				cmds = append(cmds, agentTickCmd())
				break
			}
		}
		m.vp.SetContent(m.renderMessages())

	case toolProgressMsg:
		if m.thinking {
			t := msg.trace
			m.applyToolTraceToObservability(t)
			if t.Name == "todo-write" && t.Status != "running" {
				m.syncTodosFromSession()
			}
			m.trackSessionEditFromTrace(t)
			if t.Status != "running" {
				switch t.Name {
				case "file-write", "shell-exec", "bash", "agent":
					m.refreshModifiedFiles()
				}
			}
			if t.Status == "running" {
				item := ToolItem{Name: t.Name, Args: t.Args, Status: "running"}
				m.currentTool = &item
			} else {
				m.liveTools = append(m.liveTools, ToolItem{
					Name:   t.Name,
					Status: t.Status,
					Args:   t.Args,
					Output: t.Output,
				})
				m.currentTool = nil
			}
			if m.toolCh != nil {
				cmds = append(cmds, waitForTool(m.toolCh))
			}
			m.vp.SetContent(m.renderMessages())
			m.vp.GotoBottom()
		}

	case shellApprovalRequestMsg:
		if m.thinking {
			m.pendingAuth = &msg
			m.approvalCursor = 0
			m.ta.Reset()
			m.showBanner("command approval required", "warn")
			if m.approvalCh != nil {
				cmds = append(cmds, waitForShellApproval(m.approvalCh))
			}
			m.refreshViewport()
		}

	case bannerClearMsg:
		m.banner = ""
		m.bannerKind = ""

	case quitWarningMsg:
		// Timer expired — clear the quit warning if it's still showing
		if m.banner == "press ctrl+c again to quit" {
			m.banner = ""
			m.bannerKind = ""
			m.ctrlCAt = time.Time{}
		}

	case tea.MouseMsg:
		// Wheel scroll for viewport, selector, and connect dialog
		sideW := m.sidePanelWidth()
		onSidePanel := sideW > 0 && msg.X >= m.paneWidth()+1
		if onSidePanel {
			items := m.sidePanelItems()
			_, rows := m.sideListGeometry()
			maxStart := max(0, len(items)-rows)
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				if m.sideScroll > 0 {
					m.sideScroll--
				}
				if m.sideCursor > 0 {
					m.sideCursor--
				}
				return m, tea.Batch(cmds...)
			case tea.MouseButtonWheelDown:
				if m.sideScroll < maxStart {
					m.sideScroll++
				}
				if m.sideCursor < len(items)-1 {
					m.sideCursor++
				}
				return m, tea.Batch(cmds...)
			case tea.MouseButtonLeft:
				startY, _ := m.sideListGeometry()
				row := msg.Y - startY
				if row >= 0 {
					idx := m.sideScroll + row
					if idx >= 0 && idx < len(items) {
						m.sideCursor = idx
					}
				}
				return m, tea.Batch(cmds...)
			}
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			switch {
			case m.showSelector:
				if m.selCursor > 0 {
					m.selCursor--
				}
			case m.showConnect:
				if m.connectCursor > 0 {
					m.connectCursor--
				}
			default:
				m.vp.LineUp(3)
			}
		case tea.MouseButtonWheelDown:
			switch {
			case m.showSelector:
				if m.selCursor < len(m.selItems)-1 {
					m.selCursor++
				}
			case m.showConnect:
				if m.connectCursor < len(m.connectItems)-1 {
					m.connectCursor++
				}
			default:
				m.vp.LineDown(3)
			}
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		// Dialogs get priority
		if m.showTrust {
			return m.updateTrust(msg)
		}
		if m.showResume {
			return m.updateResume(msg)
		}
		if m.showConnect {
			return m.updateConnect(msg)
		}
		if m.showSelector {
			return m.updateSelector(msg)
		}
		if m.showSetup {
			return m.updateSetup(msg)
		}
		return m.updateMain(msg)
	}

	// Pass remaining input to textarea and viewport when no dialog
	if !m.showTrust && !m.showResume && !m.showSelector && !m.showSetup && !m.showConnect {
		var taCmd tea.Cmd
		m.ta, taCmd = m.ta.Update(msg)
		cmds = append(cmds, taCmd)
		m.syncInputSuggestions()

		var vpCmd tea.Cmd
		m.vp, vpCmd = m.vp.Update(msg)
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

// updateMain handles key events for the main screen.
func (m Model) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showPlanApproval {
		return m.updatePlanApproval(msg)
	}
	if m.pendingAuth != nil {
		return m.updateShellApproval(msg)
	}

	switch msg.String() {

	case "ctrl+c":
		if !m.ctrlCAt.IsZero() && time.Since(m.ctrlCAt) < 2*time.Second {
			return m, tea.Quit
		}
		m.ctrlCAt = time.Now()
		m.showBanner("press ctrl+c again to quit", "warn")
		return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
			return quitWarningMsg{}
		})

	case "ctrl+q":
		return m, tea.Quit

	case "up", "ctrl+p":
		if len(m.cmdItems) > 0 || len(m.mentionItems) > 0 {
			if m.cmdCursor > 0 {
				m.cmdCursor--
			}
			if m.mentionCursor > 0 {
				m.mentionCursor--
			}
			return m, nil
		}
		if m.recallPreviousInput() {
			m.syncInputSuggestions()
			return m, nil
		}

	case "down", "ctrl+n":
		if len(m.cmdItems) > 0 || len(m.mentionItems) > 0 {
			if m.cmdCursor < len(m.cmdItems)-1 {
				m.cmdCursor++
			}
			if m.mentionCursor < len(m.mentionItems)-1 {
				m.mentionCursor++
			}
			return m, nil
		}
		if m.recallNextInput() {
			m.syncInputSuggestions()
			return m, nil
		}

	case "tab":
		if len(m.cmdItems) > 0 {
			m.cmdCursor = (m.cmdCursor + 1) % len(m.cmdItems)
			return m, nil
		}
		if len(m.mentionItems) > 0 {
			m.mentionCursor = (m.mentionCursor + 1) % len(m.mentionItems)
			return m, nil
		}

	case "shift+tab":
		m.mode = nextAgent(m.manifest, m.mode)
		m.showBanner(fmt.Sprintf("switched to %s mode", m.mode), "info")
		return m, nil

	case "ctrl+o":
		m.showTools = !m.showTools
		m.refreshViewport()
		return m, nil

	case "ctrl+b":
		m.showSidePanel = !m.showSidePanel
		m.refreshModifiedFiles()
		m.refreshViewport()
		if m.showSidePanel {
			m.showBanner("activity panel enabled", "info")
		} else {
			m.showBanner("activity panel hidden", "info")
		}
		return m, nil

	case "f2":
		models := m.favoriteModels()
		if len(models) > 0 {
			current := -1
			for i, mod := range models {
				if mod.Provider == m.cfg.ActiveProvider && mod.Name == m.cfg.ActiveModel {
					current = i
					break
				}
			}
			next := models[(current+1)%len(models)]
			m.cfg.ActiveProvider = next.Provider
			m.cfg.ActiveModel = next.Name
			_ = config.Save(m.cfg)
			m.showBanner(fmt.Sprintf("model → %s:%s", next.Provider, next.Name), "success")
		} else {
			m.showBanner("no favorite models — mark one with f in /models", "info")
		}
		return m, nil

	case "shift+f2":
		models := m.favoriteModels()
		if len(models) > 0 {
			current := -1
			for i, mod := range models {
				if mod.Provider == m.cfg.ActiveProvider && mod.Name == m.cfg.ActiveModel {
					current = i
					break
				}
			}
			prev := (current - 1 + len(models)) % len(models)
			m.cfg.ActiveProvider = models[prev].Provider
			m.cfg.ActiveModel = models[prev].Name
			_ = config.Save(m.cfg)
			m.showBanner(fmt.Sprintf("model → %s:%s", models[prev].Provider, models[prev].Name), "success")
		} else {
			m.showBanner("no favorite models — mark one with f in /models", "info")
		}
		return m, nil

	case "enter":
		if m.thinking {
			return m, nil
		}
		// If command palette is open, insert selection into the input first.
		if len(m.cmdItems) > 0 {
			chosen := m.cmdItems[m.cmdCursor].name
			current := strings.TrimSpace(m.ta.Value())
			if current == chosen {
				m.ta.Reset()
				m.cmdItems = nil
				m.cmdCursor = 0
				m.mentionItems = nil
				return m.handleCommand(chosen)
			}
			m.ta.SetValue(chosen + " ")
			m.cmdItems = nil
			m.cmdCursor = 0
			m.syncInputSuggestions()
			return m, nil
		}
		// File mention autocomplete: insert mention into input first.
		if len(m.mentionItems) > 0 {
			m = m.acceptMention()
			m.syncInputSuggestions()
			return m, nil
		}
		input := strings.TrimSpace(m.ta.Value())
		if input == "" {
			return m, nil
		}
		m.pushInputHistory(input)
		m.ta.Reset()
		m.cmdItems = nil
		m.mentionItems = nil
		// If plan edit is pending (user chose "edit" in plan approval dialog), route to plan edit
		if m.pendingPlan != "" && !strings.HasPrefix(input, "/") {
			return m.handlePlanEdit(input)
		}
		if strings.HasPrefix(input, "/") {
			return m.handleCommand(input)
		}
		return m.handlePrompt(input)

	case "esc":
		if m.thinking {
			m.stopAgent()
			m.showBanner("stopped", "warn")
			m.refreshViewport()
			return m, nil
		}
		if len(m.cmdItems) > 0 {
			m.ta.Reset()
			m.cmdItems = nil
			m.cmdCursor = 0
			return m, nil
		}
		if len(m.mentionItems) > 0 {
			m.mentionItems = nil
			m.mentionCursor = 0
			m.syncInputSuggestions()
			return m, nil
		}
		m.ta.Reset()
		m.banner = ""
		return m, nil
	}

	var taCmd tea.Cmd
	m.ta, taCmd = m.ta.Update(msg)
	m.syncInputSuggestions()
	return m, taCmd
}

// handleCommand processes slash commands.
func (m Model) handleCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	cmd := fields[0]

	switch cmd {
	case "/help":
		m.pushSystemMsg(helpText)
	case "/exit", "/quit":
		return m, tea.Quit
	case "/mode", "/next":
		m.mode = nextAgent(m.manifest, m.mode)
		m.showBanner(fmt.Sprintf("switched to %s mode", m.mode), "info")
	case "/connect":
		m = m.openConnect()
	case "/models":
		if len(fields) >= 2 {
			if strings.Contains(fields[1], ":") {
				parts := strings.SplitN(fields[1], ":", 2)
				if !m.providers.HasModel(parts[0], parts[1]) {
					m.showBanner("unknown model: "+fields[1], "error")
				} else {
					m.cfg.ActiveProvider = parts[0]
					m.cfg.ActiveModel = parts[1]
					_ = config.Save(m.cfg)
					if len(fields) >= 3 {
						_ = config.SaveAPIKey(parts[0], fields[2])
					}
					m.showBanner(fmt.Sprintf("model set to %s:%s", parts[0], parts[1]), "success")
				}
			} else {
				m = m.openSelector(fields[1])
			}
		} else {
			m = m.openSelector("")
		}
	case "/permission":
		if len(fields) < 2 {
			m.showBanner("usage: /permission <yolo|restricted|ask-first>", "info")
		} else {
			level := config.PermissionLevel(fields[1])
			switch level {
			case config.PermissionYOLO, config.PermissionRestricted, config.PermissionAskFirst:
				m.cfg.Permission = level
				_ = config.Save(m.cfg)
				m.showBanner(fmt.Sprintf("permission set to %s", level), "success")
			default:
				m.showBanner("invalid permission: use yolo, restricted, or ask-first", "error")
			}
		}
	case "/budget":
		if len(fields) < 2 {
			if m.cfg.TokenBudget <= 0 {
				m.showBanner("token budget: unlimited  usage: /budget <n|0>", "info")
			} else {
				m.showBanner(fmt.Sprintf("token budget: %d  usage: /budget <n|0>", m.cfg.TokenBudget), "info")
			}
		} else {
			var n int
			if _, err := fmt.Sscanf(fields[1], "%d", &n); err != nil || n < 0 {
				m.showBanner("usage: /budget <n|0>", "error")
			} else {
				m.cfg.TokenBudget = n
				_ = config.Save(m.cfg)
				if n == 0 {
					m.showBanner("token budget set to unlimited", "success")
				} else {
					m.showBanner(fmt.Sprintf("token budget set to %d", n), "success")
				}
			}
		}
	case "/approve":
		if m.pendingPlan == "" {
			m.showBanner("no pending plan — run a plan prompt first", "info")
		} else {
			spec, ok := m.manifest.AgentByID("coding")
			if !ok {
				m.showBanner("coding agent not found in manifest", "error")
			} else {
				plan := m.pendingPlan
				m.pendingPlan = ""
				return m.runAgentApproved(spec, plan, nil, nil, true)
			}
		}
	case "/image":
		if len(fields) < 2 {
			m.showBanner("usage: /image <path>", "info")
		} else {
			p := fields[1]
			if !filepath.IsAbs(p) {
				p = filepath.Join(m.cwd, p)
			}
			if _, err := os.Stat(p); err != nil {
				m.showBanner("image not found: "+p, "error")
			} else {
				m.pendingImgs = append(m.pendingImgs, p)
				m.showBanner("image queued for next message", "success")
			}
		}
	case "/init":
		return m.runInit()
	case "/compact":
		focus := strings.TrimSpace(strings.TrimPrefix(input, cmd))
		return m.runCompact(focus)
	case "/clear":
		m.autoSave()
		m.messages = nil
		m.sessionID = ""
		m.todos = nil
		m.pushSystemMsg("conversation cleared")
		m.refreshViewport()
	case "/resume":
		items, err := session.List(m.store.GlobalDir, m.cwd)
		if err != nil || len(items) == 0 {
			m.showBanner("no saved conversations found", "info")
		} else {
			m.showResume = true
			m.resumeItems = items
			m.resumeCursor = 0
		}
	default:
		m.showBanner("unknown command: "+cmd, "error")
	}

	m.refreshViewport()
	return m, nil
}

// handlePrompt dispatches to the correct agent based on mode.
func (m Model) handlePrompt(input string) (tea.Model, tea.Cmd) {
	mentionedFiles := m.extractMentionedFiles(input)
	prompt := injectMentionGuidance(input, mentionedFiles)

	m.parallelAgents = nil
	m.ensureSession()

	m.messages = append(m.messages, ChatMessage{
		Role:    RoleUser,
		Content: input,
		At:      time.Now(),
	})
	m.refreshViewport()

	spec, ok := m.manifest.AgentByID(m.mode)
	if !ok {
		m.showBanner("unknown agent: "+m.mode, "error")
		return m, nil
	}
	return m.runAgent(spec, prompt, mentionedFiles, nil)
}

// runAgent is the unified agent runner for the TUI.
// approved=true bypasses the ask-first guard (used when user explicitly approved the plan).
func (m Model) runAgent(spec config.AgentSpec, input string, mentionedFiles []string, images []string) (tea.Model, tea.Cmd) {
	return m.runAgentApproved(spec, input, mentionedFiles, images, false)
}

func (m Model) runAgentApproved(spec config.AgentSpec, input string, mentionedFiles []string, images []string, approved bool) (tea.Model, tea.Cmd) {
	m.thinking = true
	m.refreshModifiedFiles()
	m.liveTools = nil
	m.currentTool = nil
	m.pendingAuth = nil
	toolCh := make(chan agent.ToolTrace, 64)
	m.toolCh = toolCh
	approvalCh := make(chan shellApprovalRequestMsg, 8)
	m.approvalCh = approvalCh
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAgent = cancel
	m.ensureSession()
	pm := m.providers
	providerName := m.cfg.ActiveProvider
	modelName := m.cfg.ActiveModel
	cwd := m.cwd
	store := m.store
	perm := m.cfg.Permission
	agentID := spec.ID

	manifest := m.manifest
	a := agent.LLMAgent{
		Spec:            spec,
		ProviderManager: pm,
		ProviderName:    func() string { return providerName },
		ModelName:       func() string { return modelName },
		CWD:             cwd,
		MaxTokens:       m.cfg.TokenBudget,
		RequiredReads:   mentionedFiles,
		Images:          images,
		Manifest:        &manifest,
		SessionDir:      session.SessionDir(store.GlobalDir, m.sessionID),
		DelegationDepth: 0,
		ToolCallback:    func(t agent.ToolTrace) { toolCh <- t },
		ShellApproval: func(ctx context.Context, req agent.ShellApprovalRequest) (agent.ShellApprovalDecision, error) {
			respCh := make(chan shellApprovalResponse, 1)
			select {
			case approvalCh <- shellApprovalRequestMsg{request: req, response: respCh}:
			case <-ctx.Done():
				return agent.ShellApprovalDeny, ctx.Err()
			}
			select {
			case resp := <-respCh:
				if resp.err != nil {
					return agent.ShellApprovalDeny, resp.err
				}
				return resp.decision, nil
			case <-ctx.Done():
				return agent.ShellApprovalDeny, ctx.Err()
			}
		},
	}

	return m, tea.Batch(
		m.spin.Tick,
		waitForTool(toolCh),
		waitForShellApproval(approvalCh),
		func() tea.Msg {
			// ask-first means the agent asks before running shell commands,
			// not that it refuses to run. Never block execution here.
			runSpec := spec
			if approved || perm != config.PermissionAskFirst {
				// Use the configured or inherited permission level
				if perm != config.PermissionAskFirst {
					runSpec.Permission = perm
				}
			}
			a.Spec = runSpec
			result, err := a.Run(ctx, input)
			close(toolCh)
			close(approvalCh)
			if err != nil {
				return agentDoneMsg{err: err}
			}
			// Planning agents save their result to PLAN.md
			if agentID == "plan" || spec.Mode == "planning" {
				_ = store.WriteProjectFile("PLAN.md", result.Content)
				return planDoneMsg{plan: result.Content, tools: result.Tools, tokensUsed: result.TokensUsed}
			}
			return agentDoneMsg{content: result.Content, tools: result.Tools, tokensUsed: result.TokensUsed, meta: ""}
		},
	)
}

// runCommitter starts an async commit using the LLMCommitter.
func (m Model) runCommitter() (tea.Model, tea.Cmd) {
	m.thinking = true
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAgent = cancel
	cwd := m.cwd
	pm := m.providers
	providerName := m.cfg.ActiveProvider
	modelName := m.cfg.ActiveModel
	committer := agent.LLMCommitter{
		ProviderManager: pm,
		ProviderName:    func() string { return providerName },
		ModelName:       func() string { return modelName },
	}
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			msg, err := committer.Commit(ctx, cwd)
			return commitDoneMsg{commitMsg: msg, err: err}
		},
	)
}

// runSearcher starts an async repo search.
func (m Model) runSearcher(query string) (tea.Model, tea.Cmd) {
	m.thinking = true
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAgent = cancel
	searcher := m.searcher
	cwd := m.cwd
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			result, err := searcher.Search(ctx, cwd, query)
			return searchDoneMsg{result: result, err: err}
		},
	)
}

// runCompact sends the conversation transcript to the LLM and replaces messages with a summary.
func (m Model) runCompact(focus string) (tea.Model, tea.Cmd) {
	if len(m.messages) == 0 {
		m.showBanner("nothing to compact", "info")
		return m, nil
	}
	m.thinking = true
	pm := m.providers
	providerName := m.cfg.ActiveProvider
	modelName := m.cfg.ActiveModel
	// Build a text summary of the conversation
	var sb strings.Builder
	for _, msg := range m.messages {
		if msg.Role == RoleSystem {
			continue
		}
		sb.WriteString(string(msg.Role))
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n\n")
	}
	transcript := sb.String()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAgent = cancel
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			compactPrompt := "Summarize the following conversation concisely, preserving all key decisions, facts, code snippets, and action items. Output only the summary, no preamble."
			if focus != "" {
				compactPrompt += " Focus especially on: " + focus + "."
			}
			resp, err := pm.Send(ctx, providerName, modelName, provider.Request{
				Prompt: compactPrompt + "\n\n" + transcript,
			})
			if err != nil {
				return compactDoneMsg{err: err}
			}
			return compactDoneMsg{summary: resp.Content}
		},
	)
}

// runInit runs the /init agent: explores the codebase and writes SPETTRO.md.
func (m Model) runInit() (tea.Model, tea.Cmd) {
	spec, ok := m.manifest.AgentByID("docs")
	if !ok {
		spec, ok = m.manifest.AgentByID("coding")
		if !ok {
			m.showBanner("docs/coding agent not found in manifest", "error")
			return m, nil
		}
	}
	task := "Analyze this codebase and create (or improve) a SPETTRO.md file in the repository root."
	return m.runAgent(spec, task, nil, nil)
}

// runExplore starts an async codebase exploration.
func (m Model) runExplore(task string) (tea.Model, tea.Cmd) {
	if strings.TrimSpace(task) == "" {
		task = "Explore this codebase: understand the architecture, key types, conventions, and entry points."
	}
	spec, ok := m.manifest.AgentByID("explore")
	if !ok {
		m.showBanner("explore agent not found in manifest", "error")
		return m, nil
	}
	return m.runAgent(spec, task, nil, nil)
}

// planApprovalOptions defines the choices shown after plan generation.
var planApprovalOptions = []string{
	"Execute plan  (switch to coding agent)",
	"Don't execute",
	"Edit — tell me what to change",
}

func (m Model) updatePlanApproval(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(planApprovalOptions)
	switch msg.String() {
	case "up", "ctrl+p":
		if m.planApprovalCursor > 0 {
			m.planApprovalCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.planApprovalCursor < n-1 {
			m.planApprovalCursor++
		}
		return m, nil
	case "enter":
		choice := m.planApprovalCursor
		m.showPlanApproval = false
		m.planApprovalCursor = 0
		switch choice {
		case 0: // Execute — user explicitly approved, bypass ask-first
			spec, ok := m.manifest.AgentByID("coding")
			if !ok {
				m.showBanner("coding agent not found", "error")
				return m, nil
			}
			m.mode = "coding"
			plan := m.pendingPlan
			m.pendingPlan = ""
			return m.runAgentApproved(spec, plan, nil, nil, true)
		case 1: // Don't execute
			m.pendingPlan = ""
			m.showBanner("plan saved to .spettro/PLAN.md — use /approve later to execute", "info")
			return m, nil
		case 2: // Edit
			m.showBanner("describe your changes and press enter", "info")
			m.ta.Focus()
			return m, nil
		}
		return m, nil
	case "esc":
		m.showPlanApproval = false
		m.planApprovalCursor = 0
		m.showBanner("plan saved — use /approve to execute later", "info")
		return m, nil
	}
	return m, nil
}

// handlePlanEdit is called when user submits text while plan approval is in "edit" mode.
// It re-runs the plan agent with the edit instructions appended to the original prompt.
func (m Model) handlePlanEdit(editInstruction string) (tea.Model, tea.Cmd) {
	if m.pendingPlan == "" {
		m.showBanner("no pending plan to edit", "warn")
		return m, nil
	}
	spec, ok := m.manifest.AgentByID("plan")
	if !ok {
		m.showBanner("plan agent not found", "error")
		return m, nil
	}
	task := m.pendingPlan + "\n\n---\nUser requested the following changes to the plan:\n" + editInstruction
	m.pendingPlan = ""
	return m.runAgent(spec, task, nil, nil)
}

// shellApprovalOptions lists the choices for the bash approval dialog.
var shellApprovalOptions = []string{
	"Allow once",
	"Allow always  (remember this command)",
	"Deny",
	"Tell the agent what to do instead",
}

func (m Model) updateShellApproval(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingAuth == nil {
		return m, nil
	}
	// "Tell agent instead" mode: capture free text then submit
	if m.approvalCursor == 3 {
		switch msg.String() {
		case "enter":
			raw := strings.TrimSpace(m.ta.Value())
			if raw == "" {
				m.showBanner("type what the agent should do instead, then press enter", "warn")
				return m, nil
			}
			return m.resolveShellApprovalAlternative(raw), nil
		case "esc":
			m.approvalCursor = 0
			m.ta.Reset()
			return m, nil
		default:
			var taCmd tea.Cmd
			m.ta, taCmd = m.ta.Update(msg)
			return m, taCmd
		}
	}
	n := len(shellApprovalOptions)
	switch msg.String() {
	case "up", "ctrl+p":
		if m.approvalCursor > 0 {
			m.approvalCursor--
		}
		return m, nil
	case "down", "ctrl+n":
		if m.approvalCursor < n-1 {
			m.approvalCursor++
		}
		return m, nil
	case "enter":
		switch m.approvalCursor {
		case 0:
			return m.resolveShellApproval(agent.ShellApprovalAllowOnce, "command approved once"), nil
		case 1:
			return m.resolveShellApproval(agent.ShellApprovalAllowAlways, "command approved and saved"), nil
		case 2:
			return m.resolveShellApproval(agent.ShellApprovalDeny, "command denied"), nil
		case 3:
			m.ta.Reset()
			m.showBanner("type what the agent should do instead, then press enter", "info")
			return m, nil
		}
	case "esc":
		return m.resolveShellApproval(agent.ShellApprovalDeny, "command denied"), nil
	}
	return m, nil
}

func (m Model) resolveShellApproval(decision agent.ShellApprovalDecision, banner string) Model {
	if m.pendingAuth != nil {
		select {
		case m.pendingAuth.response <- shellApprovalResponse{decision: decision}:
		default:
		}
	}
	m.pendingAuth = nil
	m.approvalCursor = 0
	m.ta.Reset()
	m.showBanner(banner, "info")
	m.refreshViewport()
	return m
}

func (m Model) resolveShellApprovalAlternative(instruction string) Model {
	if m.pendingAuth != nil {
		select {
		case m.pendingAuth.response <- shellApprovalResponse{
			decision: agent.ShellApprovalDeny,
			err:      fmt.Errorf("shell-exec denied by user; do this instead: %s", instruction),
		}:
		default:
		}
	}
	m.pendingAuth = nil
	m.approvalCursor = 0
	m.ta.Reset()
	m.showBanner("alternative instruction sent", "info")
	m.refreshViewport()
	return m
}

// ── Setup wizard ─────────────────────────────────────────────────────────────

func (m Model) updateSetup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" || strings.ToLower(m.ta.Value()) == "/cancel" {
		m.showSetup = false
		m.ta.Reset()
		m.showBanner("setup cancelled", "info")
		return m, nil
	}
	if msg.String() != "enter" {
		var cmd tea.Cmd
		m.ta, cmd = m.ta.Update(msg)
		return m, cmd
	}

	input := strings.TrimSpace(m.ta.Value())
	m.ta.Reset()

	switch m.setup.step {
	case 0: // choose provider
		providerIDs := m.providers.ProviderNames()
		// Accept number or name
		if n, err := fmt.Sscanf(input, "%d", new(int)); n == 1 && err == nil {
			var idx int
			fmt.Sscanf(input, "%d", &idx)
			idx-- // 1-based to 0-based
			if idx < 0 || idx >= len(providerIDs) {
				m.pushSystemMsg(fmt.Sprintf("invalid choice — enter 1-%d or provider name", len(providerIDs)))
				m.refreshViewport()
				return m, nil
			}
			m.setup.provider = providerIDs[idx]
		} else {
			found := false
			for _, id := range providerIDs {
				if strings.EqualFold(id, input) {
					m.setup.provider = id
					found = true
					break
				}
			}
			if !found {
				m.pushSystemMsg(fmt.Sprintf("unknown provider — enter 1-%d or provider name", len(providerIDs)))
				m.refreshViewport()
				return m, nil
			}
		}
		m.setup.step = 1
		var names []string
		for _, mod := range m.providers.Models() {
			if mod.Provider == m.setup.provider {
				displayName := mod.DisplayName
				if displayName == "" {
					displayName = mod.Name
				}
				tag := mod.Tag()
				line := "  " + mod.Name
				if displayName != mod.Name {
					line += " (" + displayName + ")"
				}
				if tag != "" {
					line += "  " + tag
				}
				names = append(names, line)
			}
		}
		m.pushSystemMsg("choose model:\n" + strings.Join(names, "\n"))

	case 1: // choose model
		if !m.providers.HasModel(m.setup.provider, input) {
			m.pushSystemMsg("unknown model for " + m.setup.provider + " — try again")
			m.refreshViewport()
			return m, nil
		}
		m.setup.model = input
		m.setup.step = 2
		m.pushSystemMsg("paste API key:")

	case 2: // API key
		if input == "" {
			m.pushSystemMsg("key cannot be empty")
			m.refreshViewport()
			return m, nil
		}
		_ = config.SaveAPIKey(m.setup.provider, input)
		if m.cfg.APIKeys == nil {
			m.cfg.APIKeys = map[string]string{}
		}
		m.cfg.APIKeys[m.setup.provider] = input
		m.cfg.ActiveProvider = m.setup.provider
		m.cfg.ActiveModel = m.setup.model
		m.setup.step = 3
		m.pushSystemMsg("choose permission:\n  1) ask-first\n  2) restricted\n  3) yolo")

	case 3: // permission
		switch input {
		case "1", "ask-first":
			m.cfg.Permission = config.PermissionAskFirst
		case "2", "restricted":
			m.cfg.Permission = config.PermissionRestricted
		case "3", "yolo":
			m.cfg.Permission = config.PermissionYOLO
		default:
			m.pushSystemMsg("invalid — enter 1, 2 or 3")
			m.refreshViewport()
			return m, nil
		}
		_ = config.Save(m.cfg)
		m.showSetup = false
		m.pushSystemMsg(fmt.Sprintf("setup complete ✓  %s:%s  perm:%s",
			m.cfg.ActiveProvider, m.cfg.ActiveModel, m.cfg.Permission))
	}

	m.refreshViewport()
	return m, nil
}

// ── Connect provider dialog ───────────────────────────────────────────────────

func (m Model) openConnect() Model {
	m.showConnect = true
	m.connectFilter = ""
	m.connectCursor = 0
	m.connectStep = 0
	m.connectProvider = ""
	m.connectItems = m.filterProviders("")
	return m
}

// suggestedProviderIDs are pinned to the top of the connect dialog.
var suggestedProviderIDs = []string{localConnectProviderID, "anthropic", "openai", "mistral", "x-ai", "zai"}

func isSuggested(id string) bool {
	for _, s := range suggestedProviderIDs {
		if s == id {
			return true
		}
	}
	return false
}

func (m Model) filterProviders(filter string) []provider.ProviderInfo {
	all := m.providers.AllProviderInfos()
	all = append([]provider.ProviderInfo{{
		ID:   localConnectProviderID,
		Name: "Local endpoint (LM Studio/Ollama)",
	}}, all...)

	if filter != "" {
		q := strings.ToLower(filter)
		var out []provider.ProviderInfo
		for _, pi := range all {
			if strings.Contains(strings.ToLower(pi.ID), q) || strings.Contains(strings.ToLower(pi.Name), q) {
				out = append(out, pi)
			}
		}
		all = out
	}

	// Partition into suggested (in declared order) then the rest (already alpha-sorted).
	suggOrder := make(map[string]int, len(suggestedProviderIDs))
	for i, id := range suggestedProviderIDs {
		suggOrder[id] = i
	}
	sugg := make([]provider.ProviderInfo, len(suggestedProviderIDs))
	suggFilled := make([]bool, len(suggestedProviderIDs))
	var rest []provider.ProviderInfo
	for _, pi := range all {
		if idx, ok := suggOrder[pi.ID]; ok {
			sugg[idx] = pi
			suggFilled[idx] = true
		} else {
			rest = append(rest, pi)
		}
	}
	var out []provider.ProviderInfo
	for i, pi := range sugg {
		if suggFilled[i] {
			out = append(out, pi)
		}
	}
	return append(out, rest...)
}

func (m Model) localEndpointConnected() bool {
	return len(m.cfg.LocalEndpoints) > 0
}

func (m Model) hasLocalEndpoint(endpoint string) bool {
	for _, existing := range m.cfg.LocalEndpoints {
		if existing == endpoint {
			return true
		}
	}
	return false
}

func (m Model) updateConnect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.connectStep {
	case 0: // pick provider from list
		switch msg.String() {
		case "esc", "ctrl+c":
			m.showConnect = false
			return m, nil
		case "up", "ctrl+p", "shift+tab":
			if m.connectCursor > 0 {
				m.connectCursor--
			}
		case "down", "ctrl+n", "tab":
			if m.connectCursor < len(m.connectItems)-1 {
				m.connectCursor++
			}
		case "enter":
			if len(m.connectItems) > 0 {
				m.connectProvider = m.connectItems[m.connectCursor].ID
				m.connectStep = 1
				m.ta.Reset()
				m.ta.Focus()
			}
		case "backspace":
			if len(m.connectFilter) > 0 {
				m.connectFilter = m.connectFilter[:len(m.connectFilter)-1]
				m.connectItems = m.filterProviders(m.connectFilter)
				m.connectCursor = 0
			}
		default:
			if len(msg.String()) == 1 {
				m.connectFilter += msg.String()
				m.connectItems = m.filterProviders(m.connectFilter)
				m.connectCursor = 0
			}
		}

	case 1: // enter API key
		switch msg.String() {
		case "esc":
			m.connectStep = 0
			m.ta.Reset()
		case "enter":
			if m.connectProvider == localConnectProviderID {
				endpoint := strings.TrimSpace(m.ta.Value())
				if endpoint == "" {
					m.showBanner("endpoint cannot be empty", "error")
					return m, nil
				}
				localModels, err := provider.ProbeLocalServer(context.Background(), endpoint)
				if err != nil {
					m.showBanner("local endpoint error: "+err.Error(), "error")
					return m, nil
				}
				m.providers.AddLocalModels(localModels)
				normalized := localModels[0].Provider
				if !m.hasLocalEndpoint(normalized) {
					m.cfg.LocalEndpoints = append(m.cfg.LocalEndpoints, normalized)
				}
				_ = config.Save(m.cfg)
				m.showConnect = false
				m.ta.Reset()
				m.ta.Focus()
				m.showBanner(fmt.Sprintf("connected %s ✓", provider.LocalProviderName(normalized)), "success")
				return m, nil
			}
			key := strings.TrimSpace(m.ta.Value())
			if key == "" {
				m.showBanner("key cannot be empty", "error")
				return m, nil
			}
			_ = config.SaveAPIKey(m.connectProvider, key)
			if m.cfg.APIKeys == nil {
				m.cfg.APIKeys = map[string]string{}
			}
			m.cfg.APIKeys[m.connectProvider] = key
			m.providers.SetAPIKeys(m.cfg.APIKeys)
			m.showConnect = false
			m.ta.Reset()
			m.ta.Focus()
			m.showBanner(fmt.Sprintf("connected %s ✓", m.connectProvider), "success")
			return m, nil
		default:
			var cmd tea.Cmd
			m.ta, cmd = m.ta.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

// ── Model selector ────────────────────────────────────────────────────────────

func (m Model) openSelector(prefix string) Model {
	m.showSelector = true
	m.selFilter = strings.ToLower(strings.TrimSpace(prefix))
	m.selCursor = 0
	m.selItems = m.filterModels(m.selFilter)
	return m
}

// filterModels returns connected models with favorites sorted to the top.
func (m Model) filterModels(prefix string) []provider.Model {
	all := m.providers.ConnectedModels(m.cfg.APIKeys)
	// Fall back to full list when nothing is connected yet
	if len(all) == 0 {
		all = nil // keep nil so selector shows the "no providers" message
	}

	// Favorites first
	var favs, rest []provider.Model
	for _, mod := range all {
		if m.favorites[mod.Provider+":"+mod.Name] {
			favs = append(favs, mod)
		} else {
			rest = append(rest, mod)
		}
	}
	combined := append(favs, rest...)

	if prefix == "" {
		return combined
	}
	q := strings.ToLower(prefix)
	var out []provider.Model
	for _, mod := range combined {
		hay := strings.ToLower(mod.Provider + " " + mod.ProviderName + " " + mod.Name + " " + mod.DisplayName)
		if strings.Contains(hay, q) {
			out = append(out, mod)
		}
	}
	return out
}

func (m Model) favoriteModels() []provider.Model {
	all := m.providers.ConnectedModels(m.cfg.APIKeys)
	out := make([]provider.Model, 0, len(all))
	for _, mod := range all {
		if m.favorites[mod.Provider+":"+mod.Name] {
			out = append(out, mod)
		}
	}
	return out
}

// saveFavorites persists the favorites set back to config.
func (m *Model) saveFavorites() {
	favList := make([]string, 0, len(m.favorites))
	for k, v := range m.favorites {
		if v {
			favList = append(favList, k)
		}
	}
	m.cfg.Favorites = favList
	_ = config.Save(m.cfg)
}

// syncInputSuggestions refreshes command or file-mention suggestions.
func (m *Model) syncInputSuggestions() {
	val := m.ta.Value()
	if strings.HasPrefix(val, "/") {
		// When typing "/permission" with optional trailing filter, show permission sub-options
		if strings.HasPrefix(val, "/permission") && len(val) > len("/permission") {
			filter := strings.TrimPrefix(val, "/permission")
			filter = strings.TrimPrefix(filter, " ")
			var items []commandDef
			for _, c := range permissionCommands {
				if filter == "" || strings.Contains(c.name, filter) || strings.Contains(c.desc, filter) {
					items = append(items, c)
				}
			}
			m.cmdItems = items
			if m.cmdCursor >= len(m.cmdItems) {
				m.cmdCursor = 0
			}
			m.mentionItems = nil
			m.mentionCursor = 0
			return
		}
		query := val[1:] // text after the /
		m.cmdItems = filterCommands(query)
		if m.cmdCursor >= len(m.cmdItems) {
			m.cmdCursor = 0
		}
		m.mentionItems = nil
		m.mentionCursor = 0
		return
	}

	m.cmdItems = nil
	m.cmdCursor = 0

	query, ok := activeMentionQuery(val)
	if !ok {
		m.mentionItems = nil
		m.mentionCursor = 0
		return
	}

	m.mentionItems = filterMentionFiles(m.repoFiles, query, 8)
	if m.mentionCursor >= len(m.mentionItems) {
		m.mentionCursor = 0
	}
}

func activeMentionQuery(input string) (string, bool) {
	lastSpace := strings.LastIndexAny(input, " \n\t")
	token := input
	if lastSpace >= 0 {
		token = input[lastSpace+1:]
	}
	if !strings.HasPrefix(token, "@") {
		return "", false
	}
	return strings.TrimPrefix(token, "@"), true
}

func filterMentionFiles(files []string, query string, limit int) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	// Collect dirs first, then files — both filtered by query
	var dirs, regular []string
	for _, f := range files {
		if q != "" && !strings.Contains(strings.ToLower(f), q) {
			continue
		}
		if strings.HasSuffix(f, "/") {
			dirs = append(dirs, f)
		} else {
			regular = append(regular, f)
		}
	}
	out := append(dirs, regular...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m Model) acceptMention() Model {
	if len(m.mentionItems) == 0 {
		return m
	}
	chosen := m.mentionItems[m.mentionCursor]
	current := m.ta.Value()
	lastSpace := strings.LastIndexAny(current, " \n\t")
	prefix := ""
	if lastSpace >= 0 {
		prefix = current[:lastSpace+1]
	}
	m.ta.SetValue(prefix + "@" + chosen + " ")
	m.mentionItems = nil
	m.mentionCursor = 0
	return m
}

func (m *Model) pushInputHistory(input string) {
	if strings.TrimSpace(input) == "" {
		return
	}
	m.inputHistory = append(m.inputHistory, input)
	m.historyBrowsing = false
	m.historyIndex = -1
	m.historyDraft = ""
}

func (m *Model) recallPreviousInput() bool {
	if len(m.inputHistory) == 0 {
		return false
	}
	if !m.historyBrowsing {
		m.historyDraft = m.ta.Value()
		m.historyIndex = len(m.inputHistory) - 1
		m.historyBrowsing = true
	} else if m.historyIndex > 0 {
		m.historyIndex--
	}
	m.ta.SetValue(m.inputHistory[m.historyIndex])
	return true
}

func (m *Model) recallNextInput() bool {
	if !m.historyBrowsing || len(m.inputHistory) == 0 {
		return false
	}
	if m.historyIndex < len(m.inputHistory)-1 {
		m.historyIndex++
		m.ta.SetValue(m.inputHistory[m.historyIndex])
		return true
	}
	m.ta.SetValue(m.historyDraft)
	m.historyBrowsing = false
	m.historyIndex = -1
	m.historyDraft = ""
	return true
}

func scanRepoFiles(root string) ([]string, error) {
	gi := newGitignoreMatcher(root)
	var entries []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Always skip these regardless of .gitignore
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".spettro":
				return filepath.SkipDir
			}
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if gi.Ignored(relSlash, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			entries = append(entries, relSlash+"/")
		} else {
			entries = append(entries, relSlash)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}

func (m Model) extractMentionedFiles(input string) []string {
	seen := map[string]struct{}{}
	for _, part := range strings.Fields(input) {
		if !strings.HasPrefix(part, "@") {
			continue
		}
		p := strings.TrimPrefix(part, "@")
		p = strings.TrimSpace(strings.Trim(p, `"'.,;:!?()[]{}<>`))
		if p == "" {
			continue
		}
		resolved := resolveMentionPaths(m.cwd, p)
		for _, rel := range resolved {
			seen[rel] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for rel := range seen {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// resolveMentionPaths returns the file paths a mention expands to.
// For a file it returns a single path; for a directory it returns all files within it.
func resolveMentionPaths(cwd, p string) []string {
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(cwd, strings.TrimSuffix(p, "/")))
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return []string{filepath.ToSlash(rel)}
	}
	// Expand directory: collect all files within it, respecting .gitignore
	gi := newGitignoreMatcher(cwd)
	var files []string
	_ = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		frel, err := filepath.Rel(cwd, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(frel)
		if gi.Ignored(relSlash, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			files = append(files, relSlash)
		}
		return nil
	})
	return files
}

func injectMentionGuidance(input string, mentionedFiles []string) string {
	if len(mentionedFiles) == 0 {
		return input
	}
	var sb strings.Builder
	sb.WriteString(input)
	sb.WriteString("\n\nReferenced paths from @mentions (read these before making decisions):\n")
	for _, p := range mentionedFiles {
		sb.WriteString("- ")
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	return sb.String()
}

func (m Model) updateSelector(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.showSelector = false
		return m, nil

	case "up", "ctrl+p", "shift+tab":
		if m.selCursor > 0 {
			m.selCursor--
		}

	case "down", "ctrl+n", "tab":
		if m.selCursor < len(m.selItems)-1 {
			m.selCursor++
		}

	case "enter":
		if len(m.selItems) > 0 {
			sel := m.selItems[m.selCursor]
			m.cfg.ActiveProvider = sel.Provider
			m.cfg.ActiveModel = sel.Name
			_ = config.Save(m.cfg)
			m.showSelector = false
			m.showBanner(fmt.Sprintf("model → %s:%s", sel.Provider, sel.Name), "success")
		}

	case "f":
		// Toggle favorite for the highlighted model
		if len(m.selItems) > 0 {
			sel := m.selItems[m.selCursor]
			key := sel.Provider + ":" + sel.Name
			if m.favorites == nil {
				m.favorites = map[string]bool{}
			}
			m.favorites[key] = !m.favorites[key]
			m.saveFavorites()
			m.selItems = m.filterModels(m.selFilter)
			// keep cursor in bounds
			if m.selCursor >= len(m.selItems) {
				m.selCursor = len(m.selItems) - 1
			}
			if m.selCursor < 0 {
				m.selCursor = 0
			}
		}

	case "c":
		// Switch to connect provider dialog
		m.showSelector = false
		m = m.openConnect()
		return m, nil

	case "backspace":
		if len(m.selFilter) > 0 {
			m.selFilter = m.selFilter[:len(m.selFilter)-1]
			m.selItems = m.filterModels(m.selFilter)
			m.selCursor = 0
		}

	default:
		if len(msg.String()) == 1 {
			m.selFilter += msg.String()
			m.selItems = m.filterModels(m.selFilter)
			m.selCursor = 0
		}
	}

	return m, nil
}

// ── View ─────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	if !m.ready {
		return lipgloss.NewStyle().Foreground(colorMuted).Render("\n  loading…")
	}

	if m.showTrust {
		return m.viewTrust()
	}

	if m.showResume {
		return m.viewResume()
	}

	if m.showConnect {
		return m.viewConnect()
	}

	if m.showSelector {
		return m.viewSelector()
	}

	header := m.viewHeader()
	paneW := m.paneWidth()
	eyes := renderEyes(m.mode, m.eyeFrame, m.thinking, paneW)
	sep := m.viewSep(paneW)
	content := m.vp.View()
	inputArea := m.viewInput(paneW)
	statusBar := m.viewStatusBar(paneW)

	parts := []string{
		eyes,
		sep,
		content,
		sep,
	}
	sideW := m.sidePanelWidth()
	if sideW <= 0 {
		if pa := m.renderParallelAgents(); pa != "" {
			parts = append(parts, pa)
		}
	}
	parts = append(parts, inputArea, statusBar)
	mainPane := lipgloss.JoinVertical(lipgloss.Left, parts...)

	if sideW <= 0 {
		return lipgloss.JoinVertical(lipgloss.Left, header, mainPane)
	}
	sidePane := m.viewSidePanel(sideW)
	divider := lipgloss.NewStyle().Foreground(colorDim).Render("│")
	body := lipgloss.JoinHorizontal(lipgloss.Top, mainPane, divider, sidePane)
	return lipgloss.JoinVertical(lipgloss.Left, header, body)
}

// viewHeader renders the top bar.
func (m Model) viewHeader() string {
	mc := m.currentColor()

	// Left: logo
	logo := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ spettro")

	// Center: only the 3 primary mode tabs (plan, coding, ask)
	primaryIDs := []string{"plan", "coding", "ask"}
	var tabs []string
	for _, id := range primaryIDs {
		ag, ok := m.manifest.AgentByID(id)
		if !ok {
			continue
		}
		agColor := modeColor(ag.Color)
		if ag.ID == m.mode {
			tabs = append(tabs, lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#0D0D0D")).
				Background(agColor).
				PaddingLeft(1).PaddingRight(1).
				Render(ag.ID))
		} else {
			tabs = append(tabs, lipgloss.NewStyle().
				Foreground(colorMuted).
				PaddingLeft(1).PaddingRight(1).
				Render(ag.ID))
		}
	}
	center := strings.Join(tabs, " ")

	// Right: model display name · provider name · permission
	modelLabel := m.cfg.ActiveModel
	provLabel := m.cfg.ActiveProvider
	for _, mod := range m.providers.Models() {
		if mod.Provider == m.cfg.ActiveProvider && mod.Name == m.cfg.ActiveModel {
			if mod.DisplayName != "" {
				modelLabel = mod.DisplayName
			}
			if mod.ProviderName != "" {
				provLabel = mod.ProviderName
			}
			break
		}
	}
	// Truncate model name to 12 characters max
	if len(modelLabel) > 12 {
		modelLabel = modelLabel[:12]
	}
	permText := string(m.cfg.Permission)
	logoW := lipgloss.Width(logo)
	permW := lipgloss.Width(permText)
	maxMetaWidth := m.width - logoW - permW - 8
	if maxMetaWidth < 0 {
		maxMetaWidth = 0
	}
	metaText := truncateLabel(modelLabel+"  "+provLabel, maxMetaWidth)
	right := lipgloss.NewStyle().Foreground(mc).Render(permText)
	if metaText != "" {
		right = styleMuted.Render(metaText) + "  " + right
	}

	rightW := lipgloss.Width(right)
	availableCenter := m.width - logoW - rightW - 2
	if availableCenter < 0 {
		availableCenter = 0
	}
	if availableCenter > 0 && lipgloss.Width(center) > availableCenter {
		center = lipgloss.NewStyle().Foreground(mc).Bold(true).Render(m.mode)
	}
	centerBlock := ""
	if availableCenter > 0 {
		centerBlock = lipgloss.PlaceHorizontal(availableCenter, lipgloss.Center, center)
	}

	row := logo + " " + centerBlock
	if right != "" {
		row += " " + right
	}

	return lipgloss.NewStyle().
		Width(m.width).
		MaxWidth(m.width).
		Background(lipgloss.Color("#0D0D0D")).
		Render(row)
}

// viewSep renders a horizontal separator line.
func (m Model) viewSep(width int) string {
	return lipgloss.NewStyle().
		Foreground(colorDim).
		Render(strings.Repeat("─", width))
}

// viewCommandPalette renders the command autocomplete overlay.
func (m Model) viewCommandPalette(width int) string {
	if len(m.cmdItems) == 0 {
		return ""
	}
	mc := m.currentColor()
	var rows []string
	for i, cmd := range m.cmdItems {
		var nameStyle, descStyle lipgloss.Style
		if i == m.cmdCursor {
			nameStyle = lipgloss.NewStyle().Foreground(mc).Bold(true)
			descStyle = lipgloss.NewStyle().Foreground(colorText)
		} else {
			nameStyle = lipgloss.NewStyle().Foreground(colorText)
			descStyle = lipgloss.NewStyle().Foreground(colorMuted)
		}
		rows = append(rows, nameStyle.Render(fmt.Sprintf("%-14s", cmd.name))+"  "+descStyle.Render(cmd.desc))
	}
	body := strings.Join(rows, "\n")
	hint := styleMuted.Render("enter inserts  enter again runs")
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Width(width - 4).
		PaddingLeft(2).PaddingRight(2).
		Render(body + "\n\n" + hint)
}

func (m Model) viewMentionPalette(width int) string {
	if len(m.mentionItems) == 0 {
		return ""
	}
	mc := m.currentColor()
	var rows []string
	for i, item := range m.mentionItems {
		style := lipgloss.NewStyle().Foreground(colorMuted)
		prefix := "  "
		if i == m.mentionCursor {
			prefix = lipgloss.NewStyle().Foreground(mc).Bold(true).Render("› ")
			style = lipgloss.NewStyle().Foreground(colorText).Bold(true)
		}
		rows = append(rows, prefix+style.Render(item))
	}
	title := lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render("available files")
	hint := styleMuted.Render("↑↓ navigate  enter inserts mention")
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Width(width - 4).
		PaddingLeft(2).PaddingRight(2).
		Render(title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + hint)
}

// viewInput renders the input area with prompt prefix.
func (m Model) viewInput(width int) string {
	mc := m.currentColor()
	agentLabel := m.mode
	if spec, ok := m.manifest.AgentByID(m.mode); ok {
		agentLabel = spec.ID
	}
	prompt := modePrompt(m.mode)
	label := lipgloss.NewStyle().Foreground(mc).Bold(true).Render(prompt + " " + agentLabel)

	lines := []string{label}
	if m.showPlanApproval {
		lines = append(lines, m.renderApprovalPicker(
			"Execute this plan?",
			planApprovalOptions,
			m.planApprovalCursor,
			mc,
		))
		// No textarea in plan approval mode (unless editing)
		if m.pendingPlan != "" {
			// "edit" mode: show textarea for instructions
			lines = append(lines, m.ta.View())
		}
	} else if m.pendingAuth != nil {
		cmd := m.pendingAuth.request.Command
		lines = append(lines, styleWarn.Render("  $ "+cmd))
		if m.approvalCursor == 3 {
			// "tell agent instead" free text mode
			lines = append(lines, styleMuted.Render("  type what to do instead, then press enter:"))
			lines = append(lines, m.ta.View())
		} else {
			lines = append(lines, m.renderApprovalPicker(
				"allow this command?",
				shellApprovalOptions,
				m.approvalCursor,
				lipgloss.Color("#F59E0B"),
			))
		}
	} else {
		lines = append(lines, m.ta.View())
	}
	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(width - 2).
		PaddingLeft(1).PaddingRight(1)

	inner := strings.Join(lines, "\n")
	inputBox := boxStyle.Render(inner)

	palette := m.viewCommandPalette(width)
	mentionPalette := m.viewMentionPalette(width)
	if palette == "" && mentionPalette == "" {
		return inputBox
	}
	var blocks []string
	if palette != "" {
		blocks = append(blocks, palette)
	}
	if mentionPalette != "" {
		blocks = append(blocks, mentionPalette)
	}
	blocks = append(blocks, inputBox)
	return lipgloss.JoinVertical(lipgloss.Left, blocks...)
}

// renderParallelAgents renders the parallel agent progress display.
func (m Model) renderParallelAgents() string {
	active := make([]parallelAgentEntry, 0, len(m.parallelAgents))
	for _, a := range m.parallelAgents {
		if a.Status == "running" {
			active = append(active, a)
		}
	}
	if len(active) == 0 && len(m.todos) == 0 {
		return ""
	}
	frame := spinnerFrames[m.tickCount%len(spinnerFrames)]
	var lines []string
	if len(active) > 0 {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(colorMuted).Render("  agents"))
		lines = append(lines, lipgloss.NewStyle().Foreground(m.currentColor()).Render("  ● orchestrator: "+m.mode+" (running)"))
	}
	for _, a := range active {
		var dot string
		var style lipgloss.Style
		agentColor := modeColor("")
		if spec, ok := m.manifest.AgentByID(a.ID); ok {
			agentColor = modeColor(spec.Color)
		}
		switch a.Status {
		case "running":
			dot = frame
			style = lipgloss.NewStyle().Foreground(agentColor).Bold(true)
		case "done":
			dot = "●"
			style = lipgloss.NewStyle().Foreground(agentColor)
		case "error", "failed":
			dot = "✗"
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
		default:
			dot = "○"
			style = lipgloss.NewStyle().Foreground(colorMuted)
		}
		label := a.ID
		if a.Instance > 1 {
			label = fmt.Sprintf("%s [%d]", a.ID, a.Instance)
		}
		task := a.Task
		if len(task) > 50 {
			task = task[:47] + "..."
		}
		line := style.Render(fmt.Sprintf("  %s %-18s", dot, label)) +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render(task)
		lines = append(lines, line)
	}
	if len(m.todos) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(colorMuted).Render("  todos"))
		for _, td := range m.todos {
			icon := "○"
			color := colorMuted
			switch td.Status {
			case "completed", "done":
				icon = "✓"
				color = lipgloss.Color("#10B981")
			case "in_progress", "running":
				icon = frame
				color = lipgloss.Color("#F59E0B")
			case "blocked", "failed", "cancelled":
				icon = "!"
				color = lipgloss.Color("#EF4444")
			}
			label := td.Content
			if len(label) > 56 {
				label = label[:53] + "..."
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(color).Render("  "+icon+" ")+styleMuted.Render(label))
		}
	}
	return strings.Join(lines, "\n")
}

// contextWindow returns the context window size for the active model (0 = unknown).
func (m Model) contextWindow() int {
	for _, mod := range m.providers.Models() {
		if mod.Provider == m.cfg.ActiveProvider && mod.Name == m.cfg.ActiveModel {
			return mod.Context
		}
	}
	return 0
}

// contextWindowDefault returns a sensible default context window when the model's limit is unknown.
func contextWindowDefault(providerName string) int {
	switch providerName {
	case "anthropic":
		return 200_000
	case "openai":
		return 128_000
	case "google":
		return 1_000_000
	default:
		return 128_000
	}
}

// autoCompactIfNeeded triggers a compact if tokens used exceeds 85% of the context window.
// Returns a tea.Cmd if compaction should start, nil otherwise.
func (m Model) autoCompactIfNeeded() tea.Cmd {
	if m.thinking || m.totalTokensUsed == 0 {
		return nil
	}
	window := m.contextWindow()
	if window == 0 {
		window = contextWindowDefault(m.cfg.ActiveProvider)
	}
	threshold := int(float64(window) * 0.85)
	if m.totalTokensUsed < threshold {
		return nil
	}
	// Only auto-compact if there's enough conversation to compact
	if len(m.messages) < 3 {
		return nil
	}
	_, cmd := m.runCompact("preserve all key decisions, code changes, and action items")
	return cmd
}

// formatTokenCount formats a token count as "12.4k" or "1.2M".
func formatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// viewStatusBar renders the bottom help bar with context usage in the bottom right.
func (m Model) viewStatusBar(width int) string {
	left := strings.Join([]string{
		styleMuted.Render("shift+tab: mode"),
		styleMuted.Render("f2: model"),
		styleMuted.Render("ctrl+b: panel"),
		styleMuted.Render("/help"),
	}, styleDim.Render("  ·  "))

	// Context window indicator (right side)
	window := m.contextWindow()
	if window == 0 {
		window = contextWindowDefault(m.cfg.ActiveProvider)
	}
	used := m.totalTokensUsed
	pct := float64(used) / float64(window)
	var ctxColor lipgloss.Color
	switch {
	case pct >= 0.85:
		ctxColor = lipgloss.Color("#EF4444") // red
	case pct >= 0.65:
		ctxColor = lipgloss.Color("#F59E0B") // yellow
	default:
		ctxColor = lipgloss.Color("#6B7280") // muted
	}
	ctxLabel := fmt.Sprintf("%s / %s ctx", formatTokenCount(used), formatTokenCount(window))
	right := lipgloss.NewStyle().Foreground(ctxColor).Render(ctxLabel)

	// Pad left side to fill width, right-align the context indicator
	leftWidth := width - lipgloss.Width(right) - 2
	if leftWidth < 0 {
		leftWidth = 0
	}
	leftPadded := lipgloss.NewStyle().Width(leftWidth).Render(left)

	bar := leftPadded + right + " "
	return lipgloss.NewStyle().
		Width(width).
		Background(lipgloss.Color("#0D0D0D")).
		PaddingLeft(1).
		Render(bar)
}

func (m Model) sidePanelWidth() int {
	if !m.showSidePanel {
		return 0
	}
	hasActive := false
	for _, a := range m.parallelAgents {
		if a.Status == "running" {
			hasActive = true
			break
		}
	}
	if !hasActive && len(m.modifiedFiles) == 0 {
		return 0
	}
	if m.width < 110 {
		return 0
	}
	w := m.width / 3
	if w < 34 {
		w = 34
	}
	if w > 54 {
		w = 54
	}
	return w
}

func (m Model) paneWidth() int {
	sw := m.sidePanelWidth()
	if sw <= 0 {
		return m.width
	}
	w := m.width - sw - 1
	if w < 40 {
		return m.width
	}
	return w
}

func (m Model) sidePanelItems() []sidePanelItem {
	items := make([]sidePanelItem, 0, len(m.parallelAgents)+len(m.modifiedFiles))
	for _, a := range m.parallelAgents {
		if a.Status != "running" {
			continue
		}
		label := a.ID
		if a.Instance > 1 {
			label = fmt.Sprintf("%s [%d]", a.ID, a.Instance)
		}
		items = append(items, sidePanelItem{
			Kind:   "agent",
			ID:     a.ID,
			Title:  label,
			Detail: strings.TrimSpace(a.Task),
			Status: a.Status,
		})
	}
	for _, f := range m.modifiedFiles {
		if strings.TrimSpace(f.Path) == "" {
			continue
		}
		detail := fmt.Sprintf("+%d  -%d", f.Added, f.Deleted)
		if f.Untracked {
			detail = "untracked"
		}
		items = append(items, sidePanelItem{
			Kind:   "file",
			ID:     f.Path,
			Title:  f.Path,
			Detail: detail,
			Status: "changed",
		})
	}
	return items
}

func (m Model) sideListGeometry() (startY, rows int) {
	rows = m.height - 13
	if rows < 4 {
		rows = 4
	}
	return 5, rows
}

func (m Model) viewSidePanel(width int) string {
	items := m.sidePanelItems()
	if len(items) == 0 {
		box := lipgloss.NewStyle().
			Width(width).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1).
			Render(lipgloss.NewStyle().Bold(true).Render("Activity") + "\n\n" + styleMuted.Render("No active agents or modified files."))
		return box
	}

	cursor := m.sideCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(items) {
		cursor = len(items) - 1
	}
	_, rows := m.sideListGeometry()
	start := m.sideScroll
	maxStart := max(0, len(items)-rows)
	if start > maxStart {
		start = maxStart
	}
	if cursor < start {
		start = cursor
	}
	if cursor >= start+rows {
		start = cursor - rows + 1
	}

	var lines []string
	for idx := start; idx < len(items) && len(lines) < rows; idx++ {
		it := items[idx]
		prefix := "  "
		titleStyle := lipgloss.NewStyle().Foreground(colorMuted)
		if idx == cursor {
			prefix = lipgloss.NewStyle().Foreground(m.currentColor()).Bold(true).Render("› ")
			titleStyle = lipgloss.NewStyle().Foreground(colorText).Bold(true)
		}
		detail := styleDim.Render(it.Detail)
		if it.Kind == "file" {
			detail = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render(it.Detail)
		}
		label := truncateLabel(it.Title, max(8, width-14))
		lines = append(lines, prefix+titleStyle.Render(label)+" "+detail)
	}

	selected := items[cursor]
	details := []string{
		lipgloss.NewStyle().Bold(true).Foreground(colorMuted).Render("Details"),
		styleMuted.Render("type: " + selected.Kind),
		styleMuted.Render("id: " + selected.ID),
		styleMuted.Render(truncateLabel(selected.Detail, max(12, width-4))),
	}
	if selected.Kind == "agent" {
		details = append(details, styleMuted.Render(truncateLabel(selected.Detail, max(12, width-4))))
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Render("Activity"),
		styleMuted.Render("Active agents and session edits"),
		"",
		strings.Join(lines, "\n"),
		"",
		strings.Join(details, "\n"),
	)

	return lipgloss.NewStyle().
		Width(width).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(0, 1).
		Render(content)
}

// viewSelector renders the model selector dialog.
// Only shows models from connected providers (those with an API key set).
func (m Model) viewSelector() string {
	mc := m.currentColor()

	title := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ select model")

	// No connected providers — prompt to connect
	if len(m.providers.ConnectedModels(m.cfg.APIKeys)) == 0 {
		msg := lipgloss.JoinVertical(lipgloss.Left,
			title,
			"",
			styleMuted.Render("no providers connected yet"),
			"",
			styleSuccess.Render("press c to connect a provider"),
			styleMuted.Render("or use /connect from the main screen"),
		)
		dialog := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(mc).
			Width(50).
			Padding(2, 4).
			Render(msg)
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			dialog,
			lipgloss.WithWhitespaceChars(" "),
			lipgloss.WithWhitespaceForeground(colorDim),
		)
	}

	// Search bar
	cursor := lipgloss.NewStyle().Foreground(mc).Render("_")
	filterLine := styleMuted.Render("search  ") +
		lipgloss.NewStyle().Foreground(colorText).Render(m.selFilter) +
		cursor

	// Model rows grouped by provider
	var rows []string
	currentProvider := ""
	for i, mod := range m.selItems {
		// Provider section header
		if mod.Provider != currentProvider {
			currentProvider = mod.Provider
			if len(rows) > 0 {
				rows = append(rows, "")
			}
			provLabel := mod.ProviderName
			if provLabel == "" {
				provLabel = mod.Provider
			}
			rows = append(rows, lipgloss.NewStyle().
				Foreground(colorMuted).Bold(true).
				Render("  ─ "+provLabel))
		}

		isSelected := i == m.selCursor
		isCurrent := mod.Provider == m.cfg.ActiveProvider && mod.Name == m.cfg.ActiveModel
		isFav := m.favorites[mod.Provider+":"+mod.Name]

		var prefix string
		var nameStyle, tagStyle lipgloss.Style
		if isSelected {
			prefix = lipgloss.NewStyle().Foreground(mc).Bold(true).Render("› ")
			nameStyle = lipgloss.NewStyle().Foreground(colorText).Bold(true)
			tagStyle = lipgloss.NewStyle().Foreground(colorMuted)
		} else {
			prefix = "  "
			nameStyle = lipgloss.NewStyle().Foreground(colorMuted)
			tagStyle = lipgloss.NewStyle().Foreground(colorDim)
		}

		displayName := mod.DisplayName
		if displayName == "" {
			displayName = mod.Name
		}

		var badges string
		if isFav {
			badges += lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24")).Render("★ ")
		}
		if isCurrent {
			badges += lipgloss.NewStyle().Foreground(mc).Render("● ")
		}

		tag := tagStyle.Render(mod.Tag())
		row := prefix + badges + nameStyle.Render(displayName)
		if tag != "" {
			row += "  " + tag
		}
		rows = append(rows, row)
	}
	if len(m.selItems) == 0 {
		rows = append(rows, styleMuted.Render("  no matches"))
	}

	hint := styleMuted.Render("↑↓ navigate  enter select  f favorite  c connect  esc close")

	dialogWidth := 70
	if m.width < dialogWidth+4 {
		dialogWidth = m.width - 4
	}
	if dialogWidth < 30 {
		dialogWidth = 30
	}

	maxRows := m.height - 12
	if maxRows < 4 {
		maxRows = 4
	}
	start := 0
	if len(rows) > maxRows {
		start = m.selCursor - maxRows/2
		if start < 0 {
			start = 0
		}
		if start+maxRows > len(rows) {
			start = len(rows) - maxRows
		}
		rows = rows[start : start+maxRows]
	}

	dialog := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(dialogWidth).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			title,
			"",
			filterLine,
			"",
			strings.Join(rows, "\n"),
			"",
			hint,
		))

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(colorDim),
	)
}

// viewConnect renders the connect-provider dialog.
// Step 0: searchable list of ALL providers from the catalog.
// Step 1: API key input for the chosen provider.
func (m Model) viewConnect() string {
	mc := m.currentColor()

	dialogWidth := 60
	if m.width < dialogWidth+4 {
		dialogWidth = m.width - 4
	}
	if dialogWidth < 30 {
		dialogWidth = 30
	}

	if m.connectStep == 1 {
		// ── API key entry ─────────────────────────────────────────────────────
		provName := m.connectProvider
		envHint := ""
		prompt := "paste your API key and press enter:"
		if m.connectProvider == localConnectProviderID {
			provName = "Local endpoint"
			prompt = "enter local endpoint (e.g. localhost:1234) and press enter:"
		}
		for _, pi := range m.providers.AllProviderInfos() {
			if pi.ID == m.connectProvider {
				if pi.Name != "" {
					provName = pi.Name
				}
				if pi.Env != "" {
					envHint = styleMuted.Render("env var: " + pi.Env)
				}
				break
			}
		}

		title := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ connect " + provName)

		inner := lipgloss.JoinVertical(lipgloss.Left,
			title, "",
			envHint,
			styleMuted.Render(prompt),
			"",
			m.ta.View(),
			"",
			styleMuted.Render("esc: back"),
		)
		dialog := lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(mc).
			Width(dialogWidth).
			Padding(1, 2).
			Render(inner)

		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			dialog,
			lipgloss.WithWhitespaceChars(" "),
			lipgloss.WithWhitespaceForeground(colorDim),
		)
	}

	// ── Provider picker ───────────────────────────────────────────────────────
	title := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ connect provider")
	cursor := lipgloss.NewStyle().Foreground(mc).Render("_")
	filterLine := styleMuted.Render("search  ") +
		lipgloss.NewStyle().Foreground(colorText).Render(m.connectFilter) +
		cursor

	var rows []string
	inSuggested := true // first items are suggested (if any)
	for i, pi := range m.connectItems {
		// Section header transitions
		nowSugg := isSuggested(pi.ID)
		if i == 0 {
			if nowSugg {
				rows = append(rows, lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render("  ─ suggested"))
			} else {
				rows = append(rows, lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render("  ─ all providers"))
				inSuggested = false
			}
		} else if inSuggested && !nowSugg {
			inSuggested = false
			rows = append(rows, "")
			rows = append(rows, lipgloss.NewStyle().Foreground(colorMuted).Bold(true).Render("  ─ all providers"))
		}

		isSelected := i == m.connectCursor
		isConnected := m.cfg.APIKeys[pi.ID] != ""
		if pi.ID == localConnectProviderID {
			isConnected = m.localEndpointConnected()
		}

		name := pi.Name
		if name == "" {
			name = pi.ID
		}

		var prefix string
		var nameStyle lipgloss.Style
		if isSelected {
			prefix = lipgloss.NewStyle().Foreground(mc).Bold(true).Render("› ")
			nameStyle = lipgloss.NewStyle().Foreground(colorText).Bold(true)
		} else {
			prefix = "  "
			nameStyle = lipgloss.NewStyle().Foreground(colorMuted)
		}

		suffix := ""
		if isConnected {
			suffix = "  " + lipgloss.NewStyle().Foreground(colorSuccess).Render("✓ connected")
		}

		rows = append(rows, prefix+nameStyle.Render(name)+suffix)
	}
	if len(m.connectItems) == 0 {
		rows = append(rows, styleMuted.Render("  no matches"))
	}

	hint := styleMuted.Render("↑↓ navigate  enter connect  esc close")

	maxRows := m.height - 12
	if maxRows < 4 {
		maxRows = 4
	}
	start := 0
	if len(rows) > maxRows {
		start = m.connectCursor - maxRows/2
		if start < 0 {
			start = 0
		}
		if start+maxRows > len(rows) {
			start = len(rows) - maxRows
		}
		rows = rows[start : start+maxRows]
	}

	dialog := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(dialogWidth).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			title, "",
			filterLine, "",
			strings.Join(rows, "\n"),
			"",
			hint,
		))

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(colorDim),
	)
}

// ── viewport helpers ──────────────────────────────────────────────────────────

// stopAgent cancels any running agent goroutine and clears all live-tool state.
func (m *Model) stopAgent() {
	if m.cancelAgent != nil {
		m.cancelAgent()
		m.cancelAgent = nil
	}
	if m.pendingAuth != nil {
		select {
		case m.pendingAuth.response <- shellApprovalResponse{decision: agent.ShellApprovalDeny}:
		default:
		}
	}
	m.thinking = false
	m.toolCh = nil
	m.approvalCh = nil
	m.liveTools = nil
	m.currentTool = nil
	m.pendingAuth = nil
	m.approvalCursor = 0
}

func (m *Model) pushSystemMsg(content string) {
	m.messages = append(m.messages, ChatMessage{
		Role:    RoleSystem,
		Content: content,
		At:      time.Now(),
	})
}

func (m *Model) showBanner(text, kind string) {
	m.banner = text
	m.bannerKind = kind
}

func (m *Model) ensureSession() {
	if m.sessionID == "" {
		m.sessionID = session.NewID(m.cwd)
	}
}

func (m *Model) syncTodosFromSession() {
	if m.sessionID == "" {
		return
	}
	todos, err := session.LoadTodos(m.store.GlobalDir, m.sessionID)
	if err == nil {
		m.todos = todos
	}
}

func parseNumstat(text string, totals map[string][2]int) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		deleted, _ := strconv.Atoi(parts[1])
		path := strings.TrimSpace(parts[2])
		if strings.Contains(path, " -> ") {
			segs := strings.Split(path, " -> ")
			path = strings.TrimSpace(segs[len(segs)-1])
		}
		if path == "" {
			continue
		}
		curr := totals[path]
		totals[path] = [2]int{curr[0] + added, curr[1] + deleted}
	}
}

func normalizeWorkspacePath(cwd, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(cwd, p)
		if err == nil && !strings.HasPrefix(rel, "..") {
			p = rel
		}
	}
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

func (m *Model) markSessionEdit(path string) {
	path = normalizeWorkspacePath(m.cwd, path)
	if path == "" {
		return
	}
	if m.sessionEdits == nil {
		m.sessionEdits = map[string]struct{}{}
	}
	m.sessionEdits[path] = struct{}{}
}

func (m *Model) trackSessionEditFromTrace(t agent.ToolTrace) {
	if t.Name != "file-write" || t.Status == "running" {
		return
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(t.Args), &args); err != nil {
		return
	}
	m.markSessionEdit(args.Path)
}

func (m *Model) refreshModifiedFiles() {
	if len(m.sessionEdits) == 0 {
		m.modifiedFiles = nil
		return
	}

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = m.cwd
	out, err := cmd.Output()
	if err != nil {
		m.modifiedFiles = nil
		return
	}

	stat := make(map[string]modifiedFileEntry)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			segs := strings.Split(path, " -> ")
			path = strings.TrimSpace(segs[len(segs)-1])
		}
		if path == "" {
			continue
		}
		normPath := normalizeWorkspacePath(m.cwd, path)
		if normPath == "" {
			continue
		}
		if _, ok := m.sessionEdits[normPath]; !ok {
			continue
		}
		entry := stat[normPath]
		entry.Path = normPath
		entry.Untracked = strings.HasPrefix(line, "??")
		stat[normPath] = entry
	}

	numTotals := make(map[string][2]int)
	for _, args := range [][]string{{"diff", "--numstat"}, {"diff", "--cached", "--numstat"}} {
		d := exec.Command("git", args...)
		d.Dir = m.cwd
		data, derr := d.Output()
		if derr == nil {
			parseNumstat(string(data), numTotals)
		}
	}

	m.modifiedFiles = m.modifiedFiles[:0]
	for path, entry := range stat {
		if v, ok := numTotals[path]; ok {
			entry.Added, entry.Deleted = v[0], v[1]
		}
		m.modifiedFiles = append(m.modifiedFiles, entry)
	}
	sort.Slice(m.modifiedFiles, func(i, j int) bool {
		return m.modifiedFiles[i].Path < m.modifiedFiles[j].Path
	})
}

func (m *Model) applyToolTraceToObservability(t agent.ToolTrace) {
	if t.Name != "agent" {
		return
	}
	var args struct {
		Target         string `json:"target"`
		ID             string `json:"id"`
		Task           string `json:"task"`
		ExpectedOutput string `json:"expected_output"`
		ParentAgentID  string `json:"parent_agent_id"`
	}
	_ = json.Unmarshal([]byte(t.Args), &args)
	agentID := args.Target
	if agentID == "" {
		agentID = args.ID
	}
	if agentID == "" {
		return
	}
	task := args.Task
	if t.Status == "running" {
		instance := 0
		for _, a := range m.parallelAgents {
			if a.ID == agentID {
				instance++
			}
		}
		kind := "worker"
		if parent, ok := m.manifest.AgentByID(args.ParentAgentID); ok && parent.Mode == "worker" {
			kind = "microagent"
		}
		entry := parallelAgentEntry{
			ID:       agentID,
			Label:    agentID,
			Kind:     kind,
			Instance: instance + 1,
			Task:     task,
			Status:   "running",
		}
		m.parallelAgents = append(m.parallelAgents, entry)
		m.ensureSession()
		_ = session.AppendEvent(m.store.GlobalDir, m.sessionID, session.AgentEvent{
			AgentID:       agentID,
			AgentType:     kind,
			ParentAgentID: args.ParentAgentID,
			Task:          task,
			Status:        "running",
		})
		return
	}
	for i, a := range m.parallelAgents {
		if a.ID == agentID && a.Status == "running" {
			m.parallelAgents = append(m.parallelAgents[:i], m.parallelAgents[i+1:]...)
			break
		}
	}
	m.ensureSession()
	status := "done"
	if t.Status == "error" {
		status = "failed"
	}
	_ = session.AppendEvent(m.store.GlobalDir, m.sessionID, session.AgentEvent{
		AgentID:       agentID,
		AgentType:     "worker",
		ParentAgentID: args.ParentAgentID,
		Task:          task,
		Status:        status,
		Summary:       t.Output,
	})
}

func (m *Model) autoSave() {
	hasContent := false
	for _, msg := range m.messages {
		if msg.Role == RoleUser || msg.Role == RoleAssistant {
			hasContent = true
			break
		}
	}
	if !hasContent {
		return
	}
	m.ensureSession()
	msgs := make([]session.Message, len(m.messages))
	for i, msg := range m.messages {
		msgs[i] = session.Message{
			Role:     string(msg.Role),
			Content:  msg.Content,
			Thinking: msg.Thinking,
			Meta:     msg.Meta,
			At:       msg.At,
		}
	}
	_ = session.Save(m.store.GlobalDir, session.State{
		Metadata: session.Metadata{
			ID:          m.sessionID,
			ProjectPath: m.cwd,
			ProjectHash: session.ProjectHash(m.cwd),
			StartedAt:   msgs[0].At,
		},
		Messages: msgs,
		Todos:    m.todos,
	})
}

func (m *Model) refreshViewport() {
	m.autoSave()
	m.vp.SetContent(m.renderMessages())
	m.vp.GotoBottom()
}

// renderPlanMessage renders a plan ChatMessage as a distinct bordered block.
func (m Model) renderPlanMessage(msg ChatMessage, mc lipgloss.Color) string {
	innerW := m.paneWidth() - 8 // account for indent + border
	if innerW < 10 {
		innerW = 10
	}

	// Header label
	header := lipgloss.NewStyle().
		Foreground(mc).Bold(true).
		Render("◈ plan")

	// Tools (if any) above the plan body
	var bodyParts []string
	if len(msg.Tools) > 0 {
		bodyParts = append(bodyParts, renderToolGroups(msg.Tools, m.showTools, mc))
	}

	// Plan body — markdown aware rendering
	planBody := renderMarkdown(strings.TrimSpace(msg.Content), innerW)
	bodyParts = append(bodyParts, planBody)

	inner := strings.Join(bodyParts, "\n")

	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(innerW+2).
		Padding(0, 1).
		Render(inner)

	// Indent the whole block
	indented := indent(header+"\n"+box, "  ")
	return indented
}

func (m Model) renderMessages() string {
	if len(m.messages) == 0 {
		return styleMuted.Render("  no messages yet — type a prompt or /help")
	}

	mc := m.currentColor()
	var parts []string

	for _, msg := range m.messages {
		switch msg.Role {
		case RoleUser:
			prefix := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  › ")
			text := lipgloss.NewStyle().Foreground(colorText).Render(msg.Content)
			parts = append(parts, prefix+text)

		case RoleAssistant:
			if msg.Kind == "plan" {
				// Plan messages rendered as a distinct bordered block
				parts = append(parts, m.renderPlanMessage(msg, mc))
				continue
			}
			bullet := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  ●")
			body := renderMarkdown(msg.Content, m.paneWidth()-8)
			var entryLines []string
			if len(msg.Tools) > 0 {
				entryLines = append(entryLines, renderToolGroups(msg.Tools, m.showTools, mc))
			}
			if strings.TrimSpace(msg.Content) != "" {
				entryLines = append(entryLines, prefixBlockWithBullet(bullet, body))
			}
			if m.showTools && msg.Thinking != "" {
				thinkStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
				entryLines = append(entryLines, thinkStyle.Render("  ┌─ thinking ─┐\n"+indent(msg.Thinking, "  │ ")+"\n  └────────────┘"))
			}
			if msg.Meta != "" {
				entryLines = append(entryLines, styleMuted.Render("  "+msg.Meta))
			}
			parts = append(parts, strings.Join(entryLines, "\n"))

		case RoleSystem:
			s := lipgloss.NewStyle().
				Foreground(colorMuted).
				PaddingLeft(4).
				Width(m.paneWidth() - 4).
				Render(msg.Content)
			parts = append(parts, s)
		}
	}

	// Live tool activity shown at the bottom while the agent is running
	if m.thinking && (len(m.liveTools) > 0 || m.currentTool != nil) {
		bullet := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  ●")
		var liveLines []string
		for _, t := range m.liveTools {
			label := formatToolLabel(t.Name, t.Args)
			suffix := lipgloss.NewStyle().Foreground(colorSuccess).Render(" ✓")
			if t.Status == "error" {
				suffix = lipgloss.NewStyle().Foreground(colorError).Render(" ✗")
			}
			liveLines = append(liveLines, bullet+" "+styleMuted.Render(label)+suffix)
		}
		if m.currentTool != nil {
			label := formatRunningLabel(m.currentTool.Name, m.currentTool.Args)
			liveLines = append(liveLines, bullet+" "+styleMuted.Render(label)+" "+m.spin.View())
			if p := extractToolPath(m.currentTool.Name, m.currentTool.Args); p != "" {
				liveLines = append(liveLines, styleMuted.Render("    ⎿  "+p))
			}
		}
		if len(liveLines) > 0 {
			parts = append(parts, strings.Join(liveLines, "\n"))
		}
	}

	if m.banner != "" {
		var bs lipgloss.Style
		prefix := "  • "
		switch m.bannerKind {
		case "error":
			bs = styleError
			prefix = "  ✗ "
		case "warn":
			bs = styleWarn
			prefix = "  ! "
		case "success":
			bs = styleSuccess
			prefix = "  ✓ "
		default:
			bs = styleMuted
		}
		parts = append(parts, bs.Render(prefix+m.banner))
	}

	return strings.Join(parts, "\n\n")
}

// recalcLayout updates sub-model sizes based on current terminal dimensions
// and dynamic state (approval pickers and command palettes).
func (m Model) recalcLayout() Model {
	eyesH := len(eyesActing) // 9 lines
	headerH := 1
	sepH := 2 // two separators
	statusH := 1

	// Input box: border top(1) + label(1) + textarea(3) + border bottom(1) = 6 base
	inputH := 6
	if m.showPlanApproval {
		inputH += 2 + len(planApprovalOptions) // title + options
	} else if m.pendingAuth != nil {
		inputH += 2 + len(shellApprovalOptions) // command line + options
	}
	// Command palette above input box: border(2) + title/hint(2) + items
	if len(m.cmdItems) > 0 {
		inputH += 4 + len(m.cmdItems)
	}
	// Mention palette above input box: border(2) + title/hint(3) + items
	if len(m.mentionItems) > 0 {
		inputH += 5 + len(m.mentionItems)
	}

	parallelH := 0
	if m.sidePanelWidth() <= 0 {
		if pa := m.renderParallelAgents(); pa != "" {
			parallelH = lipgloss.Height(pa)
		}
	}

	fixed := headerH + eyesH + sepH + inputH + statusH + parallelH

	contentH := m.height - fixed
	if contentH < 3 {
		contentH = 3
	}
	vpW := m.paneWidth() - 2
	if vpW < 10 {
		vpW = 10
	}

	m.vp.Width = vpW
	m.vp.Height = contentH
	m.ta.SetWidth(m.paneWidth() - 6)

	return m
}

// ── Resume dialog ─────────────────────────────────────────────────────────────

func (m Model) loadSessionSummary(sel session.Summary) (session.State, error) {
	if !sel.Legacy {
		return session.Load(m.store.GlobalDir, sel.ID)
	}
	conv, err := conversation.Load(sel.Path)
	if err != nil {
		return session.State{}, err
	}
	state := session.StateFromLegacy(conv, m.cwd)
	if state.Metadata.ID == "" {
		state.Metadata.ID = session.NewID(m.cwd)
	}
	_ = session.Save(m.store.GlobalDir, state)
	return state, nil
}

func (m Model) updateResume(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.showResume = false
	case "up", "ctrl+p", "shift+tab":
		if m.resumeCursor > 0 {
			m.resumeCursor--
		}
	case "down", "ctrl+n", "tab":
		if m.resumeCursor < len(m.resumeItems)-1 {
			m.resumeCursor++
		}
	case "enter":
		if len(m.resumeItems) > 0 {
			sel := m.resumeItems[m.resumeCursor]
			state, err := m.loadSessionSummary(sel)
			if err != nil {
				m.showResume = false
				m.showBanner("failed to load conversation: "+err.Error(), "error")
				return m, nil
			}
			m.sessionID = state.Metadata.ID
			m.todos = state.Todos
			m.parallelAgents = nil
			m.messages = make([]ChatMessage, 0, len(state.Messages))
			for _, cm := range state.Messages {
				m.messages = append(m.messages, ChatMessage{
					Role:     Role(cm.Role),
					Content:  cm.Content,
					Thinking: cm.Thinking,
					Meta:     cm.Meta,
					At:       cm.At,
				})
			}
			m.showResume = false
			m.refreshViewport()
			m.showBanner(fmt.Sprintf("resumed conversation from %s", state.Metadata.StartedAt.Format("2006-01-02 15:04")), "success")
		}
	}
	return m, nil
}

func (m Model) viewResume() string {
	mc := m.currentColor()
	title := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ resume conversation")

	var rows []string
	for i, s := range m.resumeItems {
		isSelected := i == m.resumeCursor
		timeStr := s.StartedAt.Format("2006-01-02 15:04")
		preview := s.Preview
		if preview == "" {
			preview = "(empty)"
		}
		var prefix string
		var timeStyle, previewStyle lipgloss.Style
		if isSelected {
			prefix = lipgloss.NewStyle().Foreground(mc).Bold(true).Render("› ")
			timeStyle = lipgloss.NewStyle().Foreground(colorText).Bold(true)
			previewStyle = lipgloss.NewStyle().Foreground(colorMuted)
		} else {
			prefix = "  "
			timeStyle = lipgloss.NewStyle().Foreground(colorMuted)
			previewStyle = lipgloss.NewStyle().Foreground(colorDim)
		}
		rows = append(rows, prefix+timeStyle.Render(timeStr)+"  "+previewStyle.Render(preview))
	}
	if len(rows) == 0 {
		rows = append(rows, styleMuted.Render("  no saved conversations"))
	}

	hint := styleMuted.Render("↑↓ navigate  enter load  esc close")

	dialogWidth := 72
	if m.width < dialogWidth+4 {
		dialogWidth = m.width - 4
	}
	if dialogWidth < 30 {
		dialogWidth = 30
	}

	maxRows := m.height - 12
	if maxRows < 4 {
		maxRows = 4
	}
	if len(rows) > maxRows {
		start := m.resumeCursor - maxRows/2
		if start < 0 {
			start = 0
		}
		if start+maxRows > len(rows) {
			start = len(rows) - maxRows
		}
		rows = rows[start : start+maxRows]
	}

	dialog := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(dialogWidth).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left,
			title, "",
			strings.Join(rows, "\n"),
			"",
			hint,
		))

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(colorDim),
	)
}

// ── Trust dialog ──────────────────────────────────────────────────────────────

func (m Model) updateTrust(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "ctrl+p", "shift+tab":
		if m.trustCursor > 0 {
			m.trustCursor--
		}
	case "down", "ctrl+n", "tab":
		if m.trustCursor < 2 {
			m.trustCursor++
		}
	case "enter":
		switch m.trustCursor {
		case 0: // Yes, this session
			m.showTrust = false
			m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
			m.refreshViewport()
		case 1: // Yes and remember
			_ = config.AddTrusted(m.cwd)
			m.showTrust = false
			m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
			m.refreshViewport()
		case 2: // No
			return m, tea.Quit
		}
	case "1", "y", "Y":
		m.showTrust = false
		m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
		m.refreshViewport()
	case "2":
		_ = config.AddTrusted(m.cwd)
		m.showTrust = false
		m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
		m.refreshViewport()
	case "3", "n", "N", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) viewTrust() string {
	mc := m.currentColor()
	title := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ confirm folder trust")
	pathStyle := lipgloss.NewStyle().Foreground(colorText).Bold(true)
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24"))

	options := []string{
		"Yes, trust this session",
		"Yes, and remember this folder",
		"No, exit",
	}

	var optLines []string
	for i, opt := range options {
		var prefix string
		var style lipgloss.Style
		if i == m.trustCursor {
			prefix = lipgloss.NewStyle().Foreground(mc).Bold(true).Render("› ")
			style = lipgloss.NewStyle().Foreground(colorText).Bold(true)
		} else {
			prefix = "  "
			style = lipgloss.NewStyle().Foreground(colorMuted)
		}
		optLines = append(optLines, prefix+style.Render(fmt.Sprintf("%d  %s", i+1, opt)))
	}

	inner := lipgloss.JoinVertical(lipgloss.Left,
		title, "",
		pathStyle.Render("  "+m.cwd),
		"",
		warnStyle.Render("  Spettro may read files and run commands in this folder."),
		styleMuted.Render("  Only trust folders you own and control."),
		"",
		strings.Join(optLines, "\n"),
		"",
		styleMuted.Render("  ↑↓ navigate  enter confirm  1/2/3 direct select"),
	)

	dialogWidth := 64
	if m.width < dialogWidth+4 {
		dialogWidth = m.width - 4
	}
	if dialogWidth < 30 {
		dialogWidth = 30
	}

	dialog := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(dialogWidth).
		Padding(1, 2).
		Render(inner)

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		dialog,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(colorDim),
	)
}

// ── helpers ───────────────────────────────────────────────────────────────────

// stripThinking extracts <think>...</think> blocks from content.
// Returns the cleaned content and the concatenated thinking text.
func stripThinking(content string) (main, thinking string) {
	var sb, tb strings.Builder
	remaining := content
	for {
		start := strings.Index(remaining, "<think>")
		if start == -1 {
			sb.WriteString(remaining)
			break
		}
		sb.WriteString(remaining[:start])
		remaining = remaining[start+len("<think>"):]
		end := strings.Index(remaining, "</think>")
		if end == -1 {
			tb.WriteString(remaining)
			break
		}
		tb.WriteString(remaining[:end])
		remaining = remaining[end+len("</think>"):]
	}
	return strings.TrimSpace(sb.String()), strings.TrimSpace(tb.String())
}

// waitForTool returns a tea.Cmd that blocks until a tool trace arrives on ch.
func waitForTool(ch chan agent.ToolTrace) tea.Cmd {
	return func() tea.Msg {
		t, ok := <-ch
		if !ok {
			return nil
		}
		return toolProgressMsg{trace: t}
	}
}

func waitForShellApproval(ch chan shellApprovalRequestMsg) tea.Cmd {
	return func() tea.Msg {
		req, ok := <-ch
		if !ok {
			return nil
		}
		return req
	}
}

// renderApprovalPicker renders an arrow-key navigable list of choices.
func (m Model) renderApprovalPicker(title string, options []string, cursor int, mc lipgloss.Color) string {
	var sb strings.Builder
	sb.WriteString(styleMuted.Render("  "+title) + "\n")
	for i, opt := range options {
		if i == cursor {
			sb.WriteString(lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  › " + opt))
		} else {
			sb.WriteString(styleMuted.Render("    " + opt))
		}
		if i < len(options)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// formatToolLabel returns a human-readable label for a completed tool call.
func formatToolLabel(name, argsJSON string) string {
	switch name {
	case "file-read":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return "Read " + args.Path
		}
		return "Read file"
	case "file-write":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return "Write " + args.Path
		}
		return "Write file"
	case "repo-search":
		var args struct {
			Query string `json:"query"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Query != "" {
			q := args.Query
			if len(q) > 50 {
				q = q[:47] + "..."
			}
			return fmt.Sprintf("Search %q", q)
		}
		return "Search"
	case "shell-exec":
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Command != "" {
			cmd := args.Command
			if len(cmd) > 60 {
				cmd = cmd[:57] + "..."
			}
			return "$ " + cmd
		}
		return "Run command"
	case "glob":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			p := args.Pattern
			if len(p) > 50 {
				p = p[:47] + "..."
			}
			return fmt.Sprintf("Glob %q", p)
		}
		return "Glob"
	case "grep":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			p := args.Pattern
			if len(p) > 50 {
				p = p[:47] + "..."
			}
			return fmt.Sprintf("Grep %q", p)
		}
		return "Grep"
	}
	return name
}

// formatRunningLabel returns the in-progress version of the tool label.
func formatRunningLabel(name, argsJSON string) string {
	switch name {
	case "file-read":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return "Reading " + args.Path + "…"
		}
		return "Reading…"
	case "file-write":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Path != "" {
			return "Writing " + args.Path + "…"
		}
		return "Writing…"
	case "repo-search":
		var args struct {
			Query string `json:"query"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Query != "" {
			q := args.Query
			if len(q) > 50 {
				q = q[:47] + "..."
			}
			return fmt.Sprintf("Searching %q…", q)
		}
		return "Searching…"
	case "shell-exec":
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Command != "" {
			cmd := args.Command
			if len(cmd) > 60 {
				cmd = cmd[:57] + "..."
			}
			return "Running $ " + cmd + "…"
		}
		return "Running…"
	case "glob":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			p := args.Pattern
			if len(p) > 50 {
				p = p[:47] + "..."
			}
			return fmt.Sprintf("Globbing %q…", p)
		}
		return "Globbing…"
	case "grep":
		var args struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil && args.Pattern != "" {
			p := args.Pattern
			if len(p) > 50 {
				p = p[:47] + "..."
			}
			return fmt.Sprintf("Grepping %q…", p)
		}
		return "Grepping…"
	}
	return name + "…"
}

// extractToolPath returns the primary file path for path-based tools.
func extractToolPath(name, argsJSON string) string {
	switch name {
	case "file-read", "file-write":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(argsJSON), &args) == nil {
			return args.Path
		}
	}
	return ""
}

// toolActionVerb returns the action verb for a tool ("Read", "Write", etc.).
func toolActionVerb(name string) string {
	switch name {
	case "file-read":
		return "Read"
	case "file-write":
		return "Write"
	case "repo-search":
		return "Search"
	case "shell-exec":
		return "Run"
	case "glob":
		return "Glob"
	case "grep":
		return "Grep"
	}
	return name
}

// toolNounCount returns a count noun phrase like "4 files" or "2 searches".
func toolNounCount(name string, count int) string {
	switch name {
	case "file-read", "file-write":
		if count == 1 {
			return "1 file"
		}
		return fmt.Sprintf("%d files", count)
	case "repo-search":
		if count == 1 {
			return "1 search"
		}
		return fmt.Sprintf("%d searches", count)
	case "shell-exec":
		if count == 1 {
			return "1 command"
		}
		return fmt.Sprintf("%d commands", count)
	case "glob":
		if count == 1 {
			return "1 pattern"
		}
		return fmt.Sprintf("%d patterns", count)
	case "grep":
		if count == 1 {
			return "1 pattern"
		}
		return fmt.Sprintf("%d patterns", count)
	}
	if count == 1 {
		return "1 call"
	}
	return fmt.Sprintf("%d calls", count)
}

// renderToolGroups renders tool traces grouped by consecutive same-type tools.
func renderToolGroups(tools []ToolItem, showTools bool, mc lipgloss.Color) string {
	if len(tools) == 0 {
		return ""
	}
	bullet := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  ●")
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	outputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#4B5563")).Italic(true)
	var lines []string

	i := 0
	for i < len(tools) {
		// Find consecutive same-name tools
		j := i
		for j < len(tools) && tools[j].Name == tools[i].Name {
			j++
		}
		group := tools[i:j]
		count := len(group)
		name := group[0].Name

		if count == 1 {
			item := group[0]
			label := formatToolLabel(name, item.Args)
			if item.Status == "error" {
				label = errStyle.Render(label)
			} else {
				label = styleMuted.Render(label)
			}
			lines = append(lines, bullet+" "+label)
			if showTools {
				if p := extractToolPath(name, item.Args); p != "" {
					icon := "✓"
					if item.Status == "error" {
						icon = "✗"
					}
					lines = append(lines, styleMuted.Render(fmt.Sprintf("    ⎿  %s %s", p, icon)))
				}
				if out := trimToolOutput(item.Output, 20); out != "" {
					for _, ol := range strings.Split(out, "\n") {
						lines = append(lines, outputStyle.Render("       "+ol))
					}
				}
			}
		} else {
			noun := toolNounCount(name, count)
			label := fmt.Sprintf("%s %s", toolActionVerb(name), noun)
			if !showTools {
				label += "  " + styleMuted.Render("(ctrl+o to expand)")
			}
			lines = append(lines, bullet+" "+styleMuted.Render(label))
			if showTools {
				for _, gt := range group {
					var detail string
					if p := extractToolPath(gt.Name, gt.Args); p != "" {
						icon := "✓"
						if gt.Status == "error" {
							icon = "✗"
						}
						detail = fmt.Sprintf("    ⎿  %s %s", p, icon)
					} else {
						detail = "    ⎿  " + formatToolLabel(gt.Name, gt.Args)
					}
					lines = append(lines, styleMuted.Render(detail))
					if out := trimToolOutput(gt.Output, 8); out != "" {
						for _, ol := range strings.Split(out, "\n") {
							lines = append(lines, outputStyle.Render("       "+ol))
						}
					}
				}
			}
		}

		i = j
	}
	return strings.Join(lines, "\n")
}

// trimToolOutput returns the first maxLines lines of output, adding a truncation
// notice if there are more. Returns empty string for blank output.
func trimToolOutput(output string, maxLines int) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	if len(lines) <= maxLines {
		return output
	}
	remaining := len(lines) - maxLines
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n  … %d more lines", remaining)
}

func toToolItems(traces []agent.ToolTrace) []ToolItem {
	if len(traces) == 0 {
		return nil
	}
	out := make([]ToolItem, 0, len(traces))
	for _, t := range traces {
		out = append(out, ToolItem{
			Name:   t.Name,
			Status: t.Status,
			Args:   t.Args,
			Output: t.Output,
		})
	}
	return out
}

// indent prefixes each line of s with the given prefix string.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func truncateLabel(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// nextAgent cycles through primary modes shown in the header.
// It consults the project manifest so optional agents (for example
// "architect") can be included in the cycle when enabled.
func nextAgent(manifest config.AgentManifest, current string) string {
	order := []string{"plan", "coding", "ask"}
	var primary []string
	for _, id := range order {
		if spec, ok := manifest.AgentByID(id); ok && spec.Enabled {
			primary = append(primary, id)
		}
	}
	// Fallback to the canonical trio if manifest doesn't enable any of the above.
	if len(primary) == 0 {
		primary = []string{"plan", "coding", "ask"}
	}
	for i, id := range primary {
		if id == current {
			return primary[(i+1)%len(primary)]
		}
	}
	return primary[0]
}

// nextMode is kept as a legacy shim; callers in updateMain/handleCommand
// now use the manifest-aware nextAgent instead.
func nextMode(mode string) string {
	switch mode {
	case "plan":
		return "coding"
	case "coding":
		return "ask"
	default:
		return "plan"
	}
}

func prevMode(mode string) string {
	switch mode {
	case "plan":
		return "ask"
	case "coding":
		return "plan"
	default:
		return "coding"
	}
}

const helpText = `commands:
  /help          this message
  /exit /quit    quit spettro  (or ctrl+c twice)
  /mode          cycle to next mode  (or shift+tab)
  /setup         run setup wizard
  /models        open model selector (connected providers only)
  /models p:m    set model directly
  /connect       connect a provider or local endpoint
  /permission    set permission: yolo | restricted | ask-first
  /approve       approve and execute pending plan (coding mode)
  /image <path>  queue image for next chat message
  /images        list queued images
  /index         index project files → .spettro/index.json
  /coauthor      show co-author info for git commits
  /compact [x]   summarize conversation (optional focus instruction)
  /clear         clear conversation history (auto-saves first)
  /resume        resume a previous saved conversation

keys:
  shift+tab      cycle mode (plan → coding → ask)
  f2             cycle to next favorite model
  shift+f2       cycle to previous favorite model

in model selector:
  f              toggle favorite (★) for highlighted model
  c              open connect provider dialog`
