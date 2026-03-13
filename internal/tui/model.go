package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	"spettro/internal/indexer"
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
	{"/setup", "setup wizard"},
	{"/mode", "cycle mode"},
	{"/approve", "execute pending plan"},
	{"/permission", "set permission level"},
	{"/image", "attach image to next message"},
	{"/images", "list queued images"},
	{"/commit", "commit changes (LLM message + Spettro co-author)"},
	{"/search", "search repo files  usage: /search [query]"},
	{"/index", "index project files"},
	{"/coauthor", "show co-author git trailer"},
	{"/compact", "summarize conversation to save context"},
	{"/clear", "clear conversation history"},
	{"/resume", "resume a previous conversation"},
	{"/exit", "exit spettro"},
}

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
	content string
	meta    string // hint shown below the message
	err     error
}

type planDoneMsg struct {
	plan string
	err  error
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
	connectStep     int    // 0 = pick provider, 1 = enter key
	connectProvider string // provider ID chosen in step 0

	// command palette (shown when textarea starts with "/")
	cmdItems  []commandDef
	cmdCursor int

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

	// thinking block visibility (toggled with ctrl+o)
	showThinking bool

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
	planner   agent.PlanningAgent
	coder     agent.CodingAgent
	chatter   agent.ChatAgent
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

	return Model{
		mode:      "planning",
		cfg:       cfg,
		cwd:       cwd,
		store:     store,
		providers: pm,
		ta:        ta,
		spin:      sp,
		favorites: favs,
		convDir:   conversation.ProjectDir(store.GlobalDir, cwd),
		planner: agent.LLMPlanner{
			ProviderManager: pm,
			ProviderName:    func() string { return cfg.ActiveProvider },
			ModelName:       func() string { return cfg.ActiveModel },
			CWD:             cwd,
		},
		coder: agent.Coder{},
		chatter: agent.Chatter{
			ProviderManager: pm,
			ProviderName:    func() string { return cfg.ActiveProvider },
			ModelName:       func() string { return cfg.ActiveModel },
		},
		committer: agent.LLMCommitter{
			ProviderManager: pm,
			ProviderName:    func() string { return cfg.ActiveProvider },
			ModelName:       func() string { return cfg.ActiveModel },
		},
		searcher: agent.RepoSearcher{},
	}
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
		m.thinking = false
		if msg.err != nil {
			m.showBanner("error: "+msg.err.Error(), "error")
		} else {
			main, thinking := stripThinking(msg.content)
			m.messages = append(m.messages, ChatMessage{
				Role:     RoleAssistant,
				Content:  main,
				Thinking: thinking,
				Meta:     msg.meta,
				At:       time.Now(),
			})
		}
		m.refreshViewport()

	case planDoneMsg:
		m.thinking = false
		if msg.err != nil {
			m.showBanner("planning error: "+msg.err.Error(), "error")
		} else {
			m.pendingPlan = msg.plan
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleAssistant,
				Content: "Plan generated. Saved to .spettro/PLAN.md\nSwitch to coding mode (/mode) then /approve to execute.",
				At:      time.Now(),
			})
		}
		m.refreshViewport()

	case commitDoneMsg:
		m.thinking = false
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
		m.thinking = false
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
		m.thinking = false
		if msg.err != nil {
			m.showBanner("compact error: "+msg.err.Error(), "error")
		} else {
			m.autoSave() // save full history before compacting
			m.convID = "" // new conversation after compact
			m.messages = []ChatMessage{{
				Role:    RoleSystem,
				Content: "── conversation compacted ──\n\n" + msg.summary,
				At:      time.Now(),
			}}
		}
		m.refreshViewport()

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
		m.syncCommandPalette()

		var vpCmd tea.Cmd
		m.vp, vpCmd = m.vp.Update(msg)
		cmds = append(cmds, vpCmd)
	}

	return m, tea.Batch(cmds...)
}

// updateMain handles key events for the main screen.
func (m Model) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		if len(m.cmdItems) > 0 {
			if m.cmdCursor > 0 {
				m.cmdCursor--
			}
			return m, nil
		}

	case "down", "ctrl+n":
		if len(m.cmdItems) > 0 {
			if m.cmdCursor < len(m.cmdItems)-1 {
				m.cmdCursor++
			}
			return m, nil
		}

	case "tab":
		if len(m.cmdItems) > 0 {
			m.cmdCursor = (m.cmdCursor + 1) % len(m.cmdItems)
			return m, nil
		}

	case "shift+tab":
		m.mode = nextMode(m.mode)
		m.showBanner(fmt.Sprintf("switched to %s mode", m.mode), "info")
		return m, nil

	case "ctrl+o":
		m.showThinking = !m.showThinking
		m.refreshViewport()
		return m, nil

	case "f2":
		// Cycle to next model among connected providers
		models := m.providers.ConnectedModels(m.cfg.APIKeys)
		if len(models) == 0 {
			models = m.providers.Models()
		}
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
		}
		return m, nil

	case "shift+f2":
		// Cycle to previous model among connected providers
		models := m.providers.ConnectedModels(m.cfg.APIKeys)
		if len(models) == 0 {
			models = m.providers.Models()
		}
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
		}
		return m, nil

	case "enter":
		if m.thinking {
			return m, nil
		}
		// If command palette is open and has a selection, run that command
		if len(m.cmdItems) > 0 {
			chosen := m.cmdItems[m.cmdCursor].name
			m.ta.Reset()
			m.cmdItems = nil
			m.cmdCursor = 0
			return m.handleCommand(chosen)
		}
		input := strings.TrimSpace(m.ta.Value())
		if input == "" {
			return m, nil
		}
		m.ta.Reset()
		m.cmdItems = nil
		if strings.HasPrefix(input, "/") {
			return m.handleCommand(input)
		}
		return m.handlePrompt(input)

	case "esc":
		if len(m.cmdItems) > 0 {
			m.ta.Reset()
			m.cmdItems = nil
			m.cmdCursor = 0
			return m, nil
		}
		m.ta.Reset()
		m.banner = ""
		return m, nil
	}

	var taCmd tea.Cmd
	m.ta, taCmd = m.ta.Update(msg)
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
		m.mode = nextMode(m.mode)
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
	case "/approve":
		if m.pendingPlan == "" {
			m.showBanner("no pending plan — run a planning prompt first", "info")
		} else if m.mode != "coding" {
			m.showBanner("switch to coding mode first (shift+tab)", "info")
		} else {
			return m.runCoder(m.pendingPlan, true)
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
	case "/images":
		if len(m.pendingImgs) == 0 {
			m.pushSystemMsg("no queued images")
		} else {
			m.pushSystemMsg("queued images:\n" + strings.Join(m.pendingImgs, "\n"))
		}
	case "/index":
		snap, err := indexer.Build(m.cwd)
		if err != nil {
			m.showBanner("index error: "+err.Error(), "error")
		} else {
			dst := filepath.Join(m.store.ProjectDir, "index.json")
			if err := indexer.WriteJSON(snap, dst); err != nil {
				m.showBanner("index write error: "+err.Error(), "error")
			} else {
				m.showBanner(fmt.Sprintf("indexed %d files → .spettro/index.json", len(snap.Entries)), "success")
			}
		}
	case "/commit":
		return m.runCommitter()
	case "/search":
		query := ""
		if len(fields) >= 2 {
			query = strings.Join(fields[1:], " ")
		}
		return m.runSearcher(query)
	case "/coauthor":
		m.pushSystemMsg("spettro co-author trailer:\n  " + coAuthorInfo + "\n\nThis trailer is appended automatically by /commit.")
	case "/compact":
		return m.runCompact()
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
	m.messages = append(m.messages, ChatMessage{
		Role:    RoleUser,
		Content: input,
		At:      time.Now(),
	})
	m.refreshViewport()

	switch m.mode {
	case "planning":
		return m.runPlanner(input)
	case "coding":
		return m.runCoder(input, false)
	case "chat":
		return m.runChatter(input)
	}
	return m, nil
}

// runPlanner starts an async planning call.
func (m Model) runPlanner(prompt string) (tea.Model, tea.Cmd) {
	m.thinking = true
	store := m.store
	cwd := m.cwd
	pm := m.providers
	providerName := m.cfg.ActiveProvider
	modelName := m.cfg.ActiveModel
	planner := agent.LLMPlanner{
		ProviderManager: pm,
		ProviderName:    func() string { return providerName },
		ModelName:       func() string { return modelName },
		CWD:             cwd,
	}
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			plan, err := planner.Plan(context.Background(), prompt)
			if err != nil {
				return planDoneMsg{err: err}
			}
			_ = store.WriteProjectFile("PLAN.md", plan)
			return planDoneMsg{plan: plan}
		},
	)
}

// runCoder starts an async coding call.
func (m Model) runCoder(input string, approved bool) (tea.Model, tea.Cmd) {
	m.thinking = true
	store := m.store
	coder := m.coder
	perm := m.cfg.Permission
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			if perm == config.PermissionAskFirst && !approved {
				return agentDoneMsg{content: "ask-first mode: generate a plan then use /approve"}
			}
			result, err := coder.Execute(context.Background(), input, perm, approved)
			if err != nil {
				return agentDoneMsg{err: err}
			}
			_ = store.AppendProjectFile("AGENT.md", result+"\n\n"+coAuthorInfo+"\n")
			return agentDoneMsg{content: result}
		},
	)
}

// runChatter starts an async chat call.
func (m Model) runChatter(input string) (tea.Model, tea.Cmd) {
	m.thinking = true
	imgs := append([]string(nil), m.pendingImgs...)
	m.pendingImgs = nil
	pm := m.providers
	providerName := m.cfg.ActiveProvider
	modelName := m.cfg.ActiveModel
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			resp, err := pm.Send(context.Background(), providerName, modelName, provider.Request{
				Prompt: input,
				Images: imgs,
			})
			if err != nil {
				return agentDoneMsg{err: err}
			}
			return agentDoneMsg{
				content: resp.Content,
				meta:    "ctrl+o for details",
			}
		},
	)
}

// runCommitter starts an async commit using the LLMCommitter.
func (m Model) runCommitter() (tea.Model, tea.Cmd) {
	m.thinking = true
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
			msg, err := committer.Commit(context.Background(), cwd)
			return commitDoneMsg{commitMsg: msg, err: err}
		},
	)
}

// runSearcher starts an async repo search.
func (m Model) runSearcher(query string) (tea.Model, tea.Cmd) {
	m.thinking = true
	searcher := m.searcher
	cwd := m.cwd
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			result, err := searcher.Search(context.Background(), cwd, query)
			return searchDoneMsg{result: result, err: err}
		},
	)
}

// runCompact sends the conversation transcript to the LLM and replaces messages with a summary.
func (m Model) runCompact() (tea.Model, tea.Cmd) {
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
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			resp, err := pm.Send(context.Background(), providerName, modelName, provider.Request{
				Prompt: "Summarize the following conversation concisely, preserving all key decisions, facts, code snippets, and action items. Output only the summary, no preamble.\n\n" + transcript,
			})
			if err != nil {
				return compactDoneMsg{err: err}
			}
			return compactDoneMsg{summary: resp.Content}
		},
	)
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
var suggestedProviderIDs = []string{"anthropic", "openai", "mistral", "x-ai", "zai"}

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

// syncCommandPalette refreshes the command palette whenever the textarea changes.
func (m *Model) syncCommandPalette() {
	val := m.ta.Value()
	if strings.HasPrefix(val, "/") {
		query := val[1:] // text after the /
		m.cmdItems = filterCommands(query)
		if m.cmdCursor >= len(m.cmdItems) {
			m.cmdCursor = 0
		}
	} else {
		m.cmdItems = nil
		m.cmdCursor = 0
	}
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

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		eyes,
		sep,
		content,
		sep,
		inputArea,
		statusBar,
	)
}

// viewHeader renders the top bar.
func (m Model) viewHeader() string {
	mc := modeColor(m.mode)

	// Left: logo
	logo := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ spettro")

	// Center: mode tabs
	modes := []string{"planning", "coding", "chat"}
	tabs := make([]string, len(modes))
	for i, mo := range modes {
		if mo == m.mode {
			tabs[i] = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#0D0D0D")).
				Background(modeColor(mo)).
				PaddingLeft(1).PaddingRight(1).
				Render(mo)
		} else {
			tabs[i] = lipgloss.NewStyle().
				Foreground(colorMuted).
				PaddingLeft(1).PaddingRight(1).
				Render(mo)
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
	modelStr := styleMuted.Render(modelLabel + "  " + provLabel)
	permStr := lipgloss.NewStyle().Foreground(mc).Render(string(m.cfg.Permission))
	right := modelStr + "  " + permStr

	// Layout using widths
	totalWidth := m.width
	logoW := lipgloss.Width(logo)
	rightW := lipgloss.Width(right)
	centerW := lipgloss.Width(center)
	padLeft := (totalWidth/2 - centerW/2) - logoW
	if padLeft < 1 {
		padLeft = 1
	}
	padRight := totalWidth - logoW - padLeft - centerW - rightW
	if padRight < 1 {
		padRight = 1
	}

	row := logo +
		strings.Repeat(" ", padLeft) +
		center +
		strings.Repeat(" ", padRight) +
		right

	return lipgloss.NewStyle().
		Width(m.width).
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
	mc := modeColor(m.mode)
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
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Width(m.width - 4).
		PaddingLeft(2).PaddingRight(2).
		Render(strings.Join(rows, "\n"))
}

// viewInput renders the input area with prompt prefix.
func (m Model) viewInput() string {
	mc := modeColor(m.mode)
	prompt := modePrompt(m.mode)
	label := lipgloss.NewStyle().Foreground(mc).Bold(true).Render(prompt + " " + m.mode)

	var thinkingLine string
	if m.thinking {
		thinkingLine = "\n  " + m.spin.View() + styleMuted.Render(" thinking…")
	}

	var bannerLine string
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
		bannerLine = "\n  " + bs.Render(m.banner)
	}

	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(mc).
		Width(m.width - 2).
		PaddingLeft(1).PaddingRight(1)

	inner := label + "\n" + m.ta.View() + thinkingLine + bannerLine
	inputBox := boxStyle.Render(inner)

	palette := m.viewCommandPalette()
	if palette == "" {
		return inputBox
	}
	return lipgloss.JoinVertical(lipgloss.Left, palette, inputBox)
}

// viewStatusBar renders the bottom help bar.
func (m Model) viewStatusBar() string {
	parts := []string{
		styleMuted.Render("shift+tab: mode"),
		styleMuted.Render("f2: cycle model"),
		styleMuted.Render("/models: selector"),
		styleMuted.Render("/connect: providers"),
		styleMuted.Render("/help"),
		styleMuted.Render("ctrl+c ×2: quit"),
	}
	bar := strings.Join(parts, styleDim.Render("  ·  "))
	return lipgloss.NewStyle().
		Width(m.width).
		Background(lipgloss.Color("#0D0D0D")).
		PaddingLeft(1).
		Render(bar)
}

// viewSelector renders the model selector dialog.
// Only shows models from connected providers (those with an API key set).
func (m Model) viewSelector() string {
	mc := modeColor(m.mode)

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
	mc := modeColor(m.mode)

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
			styleMuted.Render("paste your API key and press enter:"),
			"",
			m.ta.View(),
			"",
			styleMuted.Render("esc: back  ctrl+c: cancel"),
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

	mc := modeColor(m.mode)
	var parts []string

	for _, msg := range m.messages {
		switch msg.Role {
		case RoleUser:
			prefix := lipgloss.NewStyle().Foreground(mc).Bold(true).Render("  › ")
			text := lipgloss.NewStyle().Foreground(colorText).Render(msg.Content)
			parts = append(parts, prefix+text)

		case RoleAssistant:
			label := lipgloss.NewStyle().Foreground(mc).Render("  ◈ ")
			body := lipgloss.NewStyle().Foreground(colorText).Render(msg.Content)
			entry := label + "\n" + body
			if m.showThinking && msg.Thinking != "" {
				thinkStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
				entry += "\n" + thinkStyle.Render("  ┌─ thinking ─┐\n"+indent(msg.Thinking, "  │ ")+"\n  └────────────┘")
			}
			if msg.Meta != "" {
				entry += "\n" + styleMuted.Render("  "+msg.Meta)
			}
			parts = append(parts, entry)

		case RoleSystem:
			s := lipgloss.NewStyle().
				Foreground(colorMuted).
				PaddingLeft(4).
				Width(m.width - 4).
				Render(msg.Content)
			parts = append(parts, s)
		}
	}

	return strings.Join(parts, "\n\n")
}

// recalcLayout updates sub-model sizes based on current terminal dimensions
// and dynamic state (thinking indicator, banner, command palette).
func (m Model) recalcLayout() Model {
	eyesH := len(eyesActing) // 9 lines
	headerH := 1
	sepH := 2    // two separators
	statusH := 1

	// Input box: border top(1) + label(1) + textarea(3) + border bottom(1) = 6 base
	inputH := 6
	if m.thinking {
		inputH++ // thinking line
	}
	if m.banner != "" {
		inputH++ // banner line
	}
	// Command palette above input box: border(2) + items
	if len(m.cmdItems) > 0 {
		inputH += 2 + len(m.cmdItems)
	}

	fixed := headerH + eyesH + sepH + inputH + statusH

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
	mc := modeColor(m.mode)
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
	mc := modeColor(m.mode)
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

// indent prefixes each line of s with the given prefix string.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func nextMode(mode string) string {
	switch mode {
	case "planning":
		return "coding"
	case "coding":
		return "chat"
	default:
		return "planning"
	}
}

func prevMode(mode string) string {
	switch mode {
	case "planning":
		return "chat"
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
  /connect       connect a provider (shows all available providers)
  /permission    set permission: yolo | restricted | ask-first
  /approve       approve and execute pending plan (coding mode)
  /image <path>  queue image for next chat message
  /images        list queued images
  /index         index project files → .spettro/index.json
  /coauthor      show co-author info for git commits
  /compact       summarize conversation to save context
  /clear         clear conversation history (auto-saves first)
  /resume        resume a previous saved conversation

keys:
  shift+tab      cycle mode (planning → coding → chat)
  f2             cycle to next model
  shift+f2       cycle to previous model

in model selector:
  f              toggle favorite (★) for highlighted model
  c              open connect provider dialog`
