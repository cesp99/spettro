package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"spettro/internal/agent"
	"spettro/internal/budget"
	"spettro/internal/config"
	"spettro/internal/conversation"
	"spettro/internal/provider"
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
	{"/budget", "set token budget per request  usage: /budget <n>"},
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
	Instance int    // 1-based instance number (for multiple of same agent)
	Task     string // truncated task description
	Status   string // "running" | "done" | "error"
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
	liveTools   []ToolItem
	currentTool *ToolItem
	toolCh      chan agent.ToolTrace
	approvalCh  chan shellApprovalRequestMsg
	cancelAgent context.CancelFunc // non-nil while an agent goroutine is running
	pendingAuth *shellApprovalRequestMsg
	approvalAltMode bool

	// parallel agent tracking
	parallelAgents []parallelAgentEntry
	tickCount      int

	// context window tracking
	totalTokensUsed int

	// conversation persistence
	convID  string // current conversation ID (set on first message)
	convDir string // path to .spettro/conversations/

	// resume dialog
	showResume   bool
	resumeItems  []conversation.Summary
	resumeCursor int

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
		defaultMode = "planning"
	}

	return Model{
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
		convDir:   conversation.ProjectDir(store.GlobalDir, cwd),
		committer: agent.LLMCommitter{
			ProviderManager: pm,
			ProviderName:    func() string { return cfg.ActiveProvider },
			ModelName:       func() string { return cfg.ActiveModel },
		},
		searcher: agent.RepoSearcher{},
	}
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
		if msg.tokensUsed > 0 {
			m.totalTokensUsed += msg.tokensUsed
		}
		if msg.err != nil {
			m.showBanner("error: "+msg.err.Error(), "error")
		} else {
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
		if msg.tokensUsed > 0 {
			m.totalTokensUsed += msg.tokensUsed
		}
		if msg.err != nil {
			m.showBanner("planning error: "+msg.err.Error(), "error")
		} else {
			m.pendingPlan = msg.plan
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleAssistant,
				Content: "Plan generated. Saved to .spettro/PLAN.md\nSwitch to coding mode (/mode) then /approve to execute.",
				Tools:   toToolItems(msg.tools),
				At:      time.Now(),
			})
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
			m.autoSave()  // save full history before compacting
			m.convID = "" // new conversation after compact
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
			// Handle "agent" tool specially for parallel agent display
			if t.Name == "agent" {
				var agentArgs struct {
					ID   string `json:"id"`
					Task string `json:"task"`
				}
				_ = json.Unmarshal([]byte(t.Args), &agentArgs)
				if agentArgs.ID != "" {
					if t.Status == "running" {
						// Count existing instances of this agent ID
						instance := 0
						for _, a := range m.parallelAgents {
							if a.ID == agentArgs.ID {
								instance++
							}
						}
						m.parallelAgents = append(m.parallelAgents, parallelAgentEntry{
							ID:       agentArgs.ID,
							Instance: instance + 1,
							Task:     agentArgs.Task,
							Status:   "running",
						})
						cmds = append(cmds, agentTickCmd())
					} else {
						// Update first running instance of this agent
						for i, a := range m.parallelAgents {
							if a.ID == agentArgs.ID && a.Status == "running" {
								if t.Status == "error" {
									m.parallelAgents[i].Status = "error"
								} else {
									m.parallelAgents[i].Status = "done"
								}
								break
							}
						}
					}
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
			m.approvalAltMode = false
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
		m.ta.Reset()
		m.cmdItems = nil
		m.mentionItems = nil
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
	case "/setup":
		m.showSetup = true
		m.setup = setupState{}
		providerIDs := m.providers.ProviderNames()
		var plines []string
		for i, id := range providerIDs {
			plines = append(plines, fmt.Sprintf("  %d) %s", i+1, id))
		}
		m.pushSystemMsg("setup wizard — choose provider:\n" + strings.Join(plines, "\n"))
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
			current := m.cfg.TokenBudget
			if current == 0 {
				current = budget.DefaultMax
			}
			m.showBanner(fmt.Sprintf("token budget: %d  usage: /budget <n>", current), "info")
		} else {
			var n int
			if _, err := fmt.Sscanf(fields[1], "%d", &n); err != nil || n < 1000 {
				m.showBanner("usage: /budget <n>  (minimum 1000)", "error")
			} else {
				m.cfg.TokenBudget = n
				_ = config.Save(m.cfg)
				m.showBanner(fmt.Sprintf("token budget set to %d", n), "success")
			}
		}
	case "/approve":
		if m.pendingPlan == "" {
			m.showBanner("no pending plan — run a planning prompt first", "info")
		} else {
			spec, ok := m.manifest.AgentByID("coding")
			if !ok {
				m.showBanner("coding agent not found in manifest", "error")
			} else {
				return m.runAgent(spec, m.pendingPlan, nil, nil)
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
	case "/explore":
		rest := strings.TrimSpace(strings.TrimPrefix(input, cmd))
		return m.runExplore(rest)
	case "/search":
		query := ""
		if len(fields) >= 2 {
			query = strings.Join(fields[1:], " ")
		}
		return m.runSearcher(query)
	case "/compact":
		focus := strings.TrimSpace(strings.TrimPrefix(input, cmd))
		return m.runCompact(focus)
	case "/clear":
		m.autoSave()
		m.messages = nil
		m.convID = ""
		m.pushSystemMsg("conversation cleared")
		m.refreshViewport()
	case "/resume":
		items, err := conversation.List(m.convDir)
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
func (m Model) runAgent(spec config.AgentSpec, input string, mentionedFiles []string, images []string) (tea.Model, tea.Cmd) {
	m.thinking = true
	m.liveTools = nil
	m.currentTool = nil
	m.pendingAuth = nil
	toolCh := make(chan agent.ToolTrace, 64)
	m.toolCh = toolCh
	approvalCh := make(chan shellApprovalRequestMsg, 8)
	m.approvalCh = approvalCh
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelAgent = cancel
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
			// For ask-first + coding agents: require approval
			if perm == config.PermissionAskFirst && spec.Mode == "coding" {
				close(toolCh)
				close(approvalCh)
				return agentDoneMsg{content: "ask-first mode: generate a plan then use /approve"}
			}
			// Override permission from global config if more permissive
			runSpec := spec
			if perm != config.PermissionAskFirst {
				runSpec.Permission = perm
			}
			a.Spec = runSpec
			result, err := a.Run(ctx, input)
			close(toolCh)
			close(approvalCh)
			if err != nil {
				return agentDoneMsg{err: err}
			}
			// Planning agents save their result to PLAN.md
			if agentID == "planning" || spec.Mode == "planning" {
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
	spec, ok := m.manifest.AgentByID("init")
	if !ok {
		m.showBanner("init agent not found in manifest", "error")
		return m, nil
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

func (m Model) updateShellApproval(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingAuth == nil {
		return m, nil
	}
	shortcutAllowed := !m.approvalAltMode && strings.TrimSpace(m.ta.Value()) == ""
	if shortcutAllowed {
		switch msg.String() {
		case "1", "y", "Y":
			return m.resolveShellApproval(agent.ShellApprovalAllowOnce, "command approved once"), nil
		case "2", "a", "A":
			return m.resolveShellApproval(agent.ShellApprovalAllowAlways, "command approved and saved"), nil
		case "3", "n", "N", "esc", "ctrl+c":
			return m.resolveShellApproval(agent.ShellApprovalDeny, "command denied"), nil
		case "4":
			m.approvalAltMode = true
			m.ta.Reset()
			m.showBanner("type what the agent should do instead, then press enter", "info")
			return m, nil
		}
	}
	switch msg.String() {
	case "enter":
		raw := strings.TrimSpace(m.ta.Value())
		val := strings.ToLower(raw)
		if m.approvalAltMode {
			if raw == "" {
				m.showBanner("type what the agent should do instead, then press enter", "warn")
				return m, nil
			}
			return m.resolveShellApprovalAlternative(raw), nil
		}
		switch val {
		case "1", "y", "yes":
			return m.resolveShellApproval(agent.ShellApprovalAllowOnce, "command approved once"), nil
		case "2", "a", "always", "yes and don't ask again", "yes and dont ask again":
			return m.resolveShellApproval(agent.ShellApprovalAllowAlways, "command approved and saved"), nil
		case "3", "n", "no":
			return m.resolveShellApproval(agent.ShellApprovalDeny, "command denied"), nil
		case "4":
			m.showBanner("type what the agent should do instead, then press enter", "info")
			return m, nil
		default:
			if raw == "" {
				m.showBanner("choose 1, 2, 3, or type an alternative instruction", "warn")
				return m, nil
			}
			return m.resolveShellApprovalAlternative(raw), nil
		}
	}
	var taCmd tea.Cmd
	m.ta, taCmd = m.ta.Update(msg)
	return m, taCmd
}

func (m Model) resolveShellApproval(decision agent.ShellApprovalDecision, banner string) Model {
	if m.pendingAuth != nil {
		select {
		case m.pendingAuth.response <- shellApprovalResponse{decision: decision}:
		default:
		}
	}
	m.pendingAuth = nil
	m.approvalAltMode = false
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
	m.approvalAltMode = false
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
	out := make([]string, 0, limit)
	for _, f := range files {
		if q == "" || strings.Contains(strings.ToLower(f), q) {
			out = append(out, f)
			if len(out) >= limit {
				break
			}
		}
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

func scanRepoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".spettro":
				return filepath.SkipDir
			}
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
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
		rel, ok := resolveMentionPath(m.cwd, p)
		if !ok {
			continue
		}
		seen[rel] = struct{}{}
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

func resolveMentionPath(cwd, p string) (string, bool) {
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(cwd, p))
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func injectMentionGuidance(input string, mentionedFiles []string) string {
	if len(mentionedFiles) == 0 {
		return input
	}
	var sb strings.Builder
	sb.WriteString(input)
	sb.WriteString("\n\nReferenced files from @mentions:\n")
	for _, p := range mentionedFiles {
		sb.WriteString("- ")
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	sb.WriteString("Inspect these files before making decisions.")
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
	eyes := renderEyes(m.mode, m.eyeFrame, m.thinking, m.width)
	sep := m.viewSep()
	content := m.vp.View()
	inputArea := m.viewInput()
	statusBar := m.viewStatusBar()

	parts := []string{
		header,
		eyes,
		sep,
		content,
		sep,
	}
	if pa := m.renderParallelAgents(); pa != "" {
		parts = append(parts, pa)
	}
	parts = append(parts, inputArea, statusBar)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// viewHeader renders the top bar.
func (m Model) viewHeader() string {
	mc := m.currentColor()

	// Left: logo
	logo := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ spettro")

	// Center: only the 3 primary mode tabs (planning, coding, ask)
	primaryIDs := []string{"planning", "coding", "ask"}
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
	centerBlock := lipgloss.PlaceHorizontal(availableCenter, lipgloss.Center, center)

	row := logo + " " + centerBlock + " " + right

	return lipgloss.NewStyle().
		Width(m.width).
		MaxWidth(m.width).
		Background(lipgloss.Color("#0D0D0D")).
		Render(row)
}

// viewSep renders a horizontal separator line.
func (m Model) viewSep() string {
	return lipgloss.NewStyle().
		Foreground(colorDim).
		Render(strings.Repeat("─", m.width))
}

// viewCommandPalette renders the command autocomplete overlay.
func (m Model) viewCommandPalette() string {
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
		Width(m.width - 4).
		PaddingLeft(2).PaddingRight(2).
		Render(body + "\n\n" + hint)
}

func (m Model) viewMentionPalette() string {
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
		Width(m.width - 4).
		PaddingLeft(2).PaddingRight(2).
		Render(title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + hint)
}

// viewInput renders the input area with prompt prefix.
func (m Model) viewInput() string {
	mc := m.currentColor()
	agentLabel := m.mode
	if spec, ok := m.manifest.AgentByID(m.mode); ok {
		agentLabel = spec.ID
	}
	prompt := modePrompt(m.mode)
	label := lipgloss.NewStyle().Foreground(mc).Bold(true).Render(prompt + " " + agentLabel)

	lines := []string{label}
	if m.pendingAuth != nil {
		lines = append(lines, styleWarn.Render(formatShellApprovalPrompt(m.pendingAuth.request.Command)))
	}
	lines = append(lines, m.ta.View())
	if m.thinking {
		lines = append(lines, "  "+m.spin.View()+styleMuted.Render(" thinking…"))
	}
	if m.banner != "" {
		var bs lipgloss.Style
		switch m.bannerKind {
		case "error":
			bs = styleError
		case "warn":
			bs = styleWarn
		case "success":
			bs = styleSuccess
		default:
			bs = styleMuted
		}
		lines = append(lines, "  "+bs.Render(m.banner))
	}

	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(m.width - 2).
		PaddingLeft(1).PaddingRight(1)

	inner := strings.Join(lines, "\n")
	inputBox := boxStyle.Render(inner)

	palette := m.viewCommandPalette()
	mentionPalette := m.viewMentionPalette()
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
	if len(m.parallelAgents) == 0 {
		return ""
	}
	frame := spinnerFrames[m.tickCount%len(spinnerFrames)]
	var lines []string
	for _, a := range m.parallelAgents {
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
		case "error":
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
func (m Model) viewStatusBar() string {
	left := strings.Join([]string{
		styleMuted.Render("shift+tab: mode"),
		styleMuted.Render("f2: model"),
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
	leftWidth := m.width - lipgloss.Width(right) - 2
	if leftWidth < 0 {
		leftWidth = 0
	}
	leftPadded := lipgloss.NewStyle().Width(leftWidth).Render(left)

	bar := leftPadded + right + " "
	return lipgloss.NewStyle().
		Width(m.width).
		Background(lipgloss.Color("#0D0D0D")).
		PaddingLeft(1).
		Render(bar)
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
	m.approvalAltMode = false
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

func (m *Model) autoSave() {
	// only save if there are substantive messages
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
	if m.convID == "" {
		m.convID = conversation.NewID()
	}
	msgs := make([]conversation.Message, len(m.messages))
	for i, msg := range m.messages {
		msgs[i] = conversation.Message{
			Role:     string(msg.Role),
			Content:  msg.Content,
			Thinking: msg.Thinking,
			Meta:     msg.Meta,
			At:       msg.At,
		}
	}
	_ = conversation.Save(m.convDir, conversation.Conversation{
		ID:        m.convID,
		StartedAt: msgs[0].At,
		Messages:  msgs,
	})
}

func (m *Model) refreshViewport() {
	m.autoSave()
	m.vp.SetContent(m.renderMessages())
	m.vp.GotoBottom()
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
			bullet := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  ●")
			body := lipgloss.NewStyle().Foreground(colorText).Render(msg.Content)
			var entryLines []string
			if len(msg.Tools) > 0 {
				entryLines = append(entryLines, renderToolGroups(msg.Tools, m.showTools, mc))
			}
			if strings.TrimSpace(msg.Content) != "" {
				entryLines = append(entryLines, bullet+" "+body)
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
				Width(m.width - 4).
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

	return strings.Join(parts, "\n\n")
}

// recalcLayout updates sub-model sizes based on current terminal dimensions
// and dynamic state (thinking indicator, banner, command palette).
func (m Model) recalcLayout() Model {
	eyesH := len(eyesActing) // 9 lines
	headerH := 1
	sepH := 2 // two separators
	statusH := 1

	// Input box: border top(1) + label(1) + textarea(3) + border bottom(1) = 6 base
	inputH := 6
	if m.thinking {
		inputH++ // thinking line
	}
	if m.banner != "" {
		inputH++ // banner line
	}
	if m.pendingAuth != nil {
		inputH += len(strings.Split(formatShellApprovalPrompt(m.pendingAuth.request.Command), "\n"))
	}
	// Command palette above input box: border(2) + title/hint(2) + items
	if len(m.cmdItems) > 0 {
		inputH += 4 + len(m.cmdItems)
	}
	// Mention palette above input box: border(2) + title/hint(3) + items
	if len(m.mentionItems) > 0 {
		inputH += 5 + len(m.mentionItems)
	}

	// Parallel agents display
	parallelH := len(m.parallelAgents)

	fixed := headerH + eyesH + sepH + inputH + statusH + parallelH

	contentH := m.height - fixed
	if contentH < 3 {
		contentH = 3
	}
	vpW := m.width - 2
	if vpW < 10 {
		vpW = 10
	}

	m.vp.Width = vpW
	m.vp.Height = contentH
	m.ta.SetWidth(m.width - 6)

	return m
}

// ── Resume dialog ─────────────────────────────────────────────────────────────

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
			conv, err := conversation.Load(sel.Path)
			if err != nil {
				m.showResume = false
				m.showBanner("failed to load conversation: "+err.Error(), "error")
				return m, nil
			}
			m.convID = conv.ID
			m.messages = make([]ChatMessage, 0, len(conv.Messages))
			for _, cm := range conv.Messages {
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
			m.showBanner(fmt.Sprintf("resumed conversation from %s", conv.StartedAt.Format("2006-01-02 15:04")), "success")
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

func formatShellApprovalPrompt(command string) string {
	return strings.Join([]string{
		"spettro wants to run this command:",
		"  Bash(" + command + ")",
		"",
		"choose an action:",
		"  1) yes",
		"  2) yes and don't ask again",
		"  3) no",
		"  4) tell the agent what to do instead",
	}, "\n")
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
			label := formatToolLabel(name, group[0].Args)
			if group[0].Status == "error" {
				label = lipgloss.NewStyle().Foreground(colorError).Render(label)
			} else {
				label = styleMuted.Render(label)
			}
			lines = append(lines, bullet+" "+label)
			if p := extractToolPath(name, group[0].Args); p != "" && showTools {
				lines = append(lines, styleMuted.Render("    ⎿  "+p))
			}
		} else {
			noun := toolNounCount(name, count)
			label := fmt.Sprintf("%s %s", toolActionVerb(name), noun)
			if !showTools {
				label += styleMuted.Render(" (ctrl+o to expand)")
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
				}
			}
		}

		i = j
	}
	return strings.Join(lines, "\n")
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

// nextAgent cycles only through the 3 primary modes shown in the header.
func nextAgent(_ config.AgentManifest, current string) string {
	primary := []string{"planning", "coding", "ask"}
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
	case "planning":
		return "coding"
	case "coding":
		return "ask"
	default:
		return "planning"
	}
}

func prevMode(mode string) string {
	switch mode {
	case "planning":
		return "ask"
	case "coding":
		return "planning"
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
  shift+tab      cycle mode (planning → coding → chat)
  f2             cycle to next favorite model
  shift+f2       cycle to previous favorite model

in model selector:
  f              toggle favorite (★) for highlighted model
  c              open connect provider dialog`
