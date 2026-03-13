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
	"spettro/internal/indexer"
	"spettro/internal/provider"
	"spettro/internal/storage"
)

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
	Role    Role
	Content string
	Tools   []ToolItem
	At      time.Time
}

// ── tea messages ─────────────────────────────────────────────────────────────

type tickMsg time.Time

type agentDoneMsg struct {
	content string
	err     error
}

type planDoneMsg struct {
	plan string
	err  error
}

type bannerClearMsg struct{}

type quitWarningMsg struct{}

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
	vp      viewport.Model
	ta      textarea.Model
	spin    spinner.Model

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

	// setup wizard
	showSetup bool
	setup     setupState

	// pending state
	pendingPlan string
	pendingImgs []string

	// banner (info / error)
	banner     string
	bannerKind string // info | error | warn | success

	// quit protection: require two ctrl+c within 2 seconds
	ctrlCAt time.Time

	// app deps
	cwd       string
	store     *storage.Store
	providers *provider.Manager
	planner   agent.PlanningAgent
	coder     agent.CodingAgent
	chatter   agent.ChatAgent
}

// New creates a new bubbletea Model wired to all the internal services.
func New(cwd string, cfg config.UserConfig, store *storage.Store, pm *provider.Manager) Model {
	ta := textarea.New()
	ta.Placeholder = "enter message…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 8000
	ta.SetHeight(3)
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorMuted)

	return Model{
		mode:      "planning",
		cfg:       cfg,
		cwd:       cwd,
		store:     store,
		providers: pm,
		ta:        ta,
		spin:      sp,
		planner:   agent.Planner{},
		coder:     agent.Coder{},
		chatter: agent.Chatter{
			ProviderManager: pm,
			ProviderName:    func() string { return cfg.ActiveProvider },
			ModelName:       func() string { return cfg.ActiveModel },
		},
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.recalcLayout()
		if !m.ready {
			m.ready = true
			m.pushSystemMsg("spettro ready — /help for commands, shift+tab to switch mode")
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
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleAssistant,
				Content: msg.content,
				At:      time.Now(),
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

	case tea.KeyMsg:
		// Dialogs get priority
		if m.showSelector {
			return m.updateSelector(msg)
		}
		if m.showSetup {
			return m.updateSetup(msg)
		}
		return m.updateMain(msg)
	}

	// Pass remaining input to textarea and viewport when no dialog
	if !m.showSelector && !m.showSetup {
		var taCmd tea.Cmd
		m.ta, taCmd = m.ta.Update(msg)
		cmds = append(cmds, taCmd)

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

	case "shift+tab":
		m.mode = nextMode(m.mode)
		m.showBanner(fmt.Sprintf("switched to %s mode", m.mode), "info")
		return m, nil

	case "tab":
		m.mode = prevMode(m.mode)
		m.showBanner(fmt.Sprintf("switched to %s mode", m.mode), "info")
		return m, nil

	case "f2":
		// Cycle to next model (opencode model_cycle_recent pattern)
		models := m.providers.Models()
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
		// Cycle to previous model
		models := m.providers.Models()
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
		input := strings.TrimSpace(m.ta.Value())
		if input == "" {
			return m, nil
		}
		m.ta.Reset()

		if strings.HasPrefix(input, "/") {
			return m.handleCommand(input)
		}
		return m.handlePrompt(input)

	case "esc":
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
	case "/coauthor":
		m.pushSystemMsg("co-author: Claude (claude.ai)\nAdd to your commit:\n  Co-Authored-By: Claude <noreply@anthropic.com>")
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
	planner := m.planner
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
			_ = store.AppendProjectFile("AGENT.md", result+"\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n")
			return agentDoneMsg{content: result}
		},
	)
}

// runChatter starts an async chat call.
func (m Model) runChatter(input string) (tea.Model, tea.Cmd) {
	m.thinking = true
	imgs := append([]string(nil), m.pendingImgs...)
	m.pendingImgs = nil
	chatter := m.chatter
	return m, tea.Batch(
		m.spin.Tick,
		func() tea.Msg {
			resp, err := chatter.Reply(context.Background(), input, imgs)
			if err != nil {
				return agentDoneMsg{err: err}
			}
			return agentDoneMsg{content: fmt.Sprintf("%s\n\n%s",
				resp.Content,
				lipgloss.NewStyle().Foreground(colorMuted).Render(
					fmt.Sprintf("provider:%s model:%s ~%d tokens", resp.Provider, resp.Model, resp.EstimatedTokens),
				),
			)}
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

// ── Model selector ────────────────────────────────────────────────────────────

func (m Model) openSelector(prefix string) Model {
	m.showSelector = true
	m.selFilter = strings.ToLower(strings.TrimSpace(prefix))
	m.selCursor = 0
	m.selItems = m.filterModels(m.selFilter)
	return m
}

func (m Model) filterModels(prefix string) []provider.Model {
	out := make([]provider.Model, 0)
	for _, mod := range m.providers.Models() {
		full := strings.ToLower(mod.Provider + ":" + mod.Name)
		if prefix == "" || strings.Contains(full, prefix) {
			out = append(out, mod)
		}
	}
	return out
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

	// Right: model + permission
	modelStr := styleMuted.Render(m.cfg.ActiveProvider + ":" + m.cfg.ActiveModel)
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
	return boxStyle.Render(inner)
}

// viewStatusBar renders the bottom help bar.
func (m Model) viewStatusBar() string {
	parts := []string{
		styleMuted.Render("shift+tab: mode"),
		styleMuted.Render("f2: cycle model"),
		styleMuted.Render("/models: selector"),
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

// viewSelector renders the model selector dialog as a full-screen overlay.
// Layout is inspired by opencode's dialog-model: provider sections, fuzzy
// search, tag badges (img / think / ctx size).
func (m Model) viewSelector() string {
	mc := modeColor(m.mode)

	title := lipgloss.NewStyle().Bold(true).Foreground(mc).Render("◈ select model")

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

		// Selected vs normal row
		isSelected := i == m.selCursor
		isCurrent := mod.Provider == m.cfg.ActiveProvider && mod.Name == m.cfg.ActiveModel

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
		if isCurrent {
			displayName += lipgloss.NewStyle().Foreground(mc).Render(" ●")
		}

		tag := tagStyle.Render(mod.Tag())
		row := prefix + nameStyle.Render(displayName)
		if tag != "" {
			row += "  " + tag
		}
		rows = append(rows, row)
	}
	if len(m.selItems) == 0 {
		rows = append(rows, styleMuted.Render("  no matches"))
	}

	hint := styleMuted.Render("↑↓ navigate  enter select  esc close  f2 cycle")

	dialogWidth := 70
	if m.width < dialogWidth+4 {
		dialogWidth = m.width - 4
	}
	if dialogWidth < 30 {
		dialogWidth = 30
	}

	// Cap visible rows to avoid overflowing the screen
	maxRows := m.height - 12
	if maxRows < 4 {
		maxRows = 4
	}
	// Scroll window around cursor
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

func (m *Model) refreshViewport() {
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
			body := lipgloss.NewStyle().
				Foreground(colorText).
				Width(m.width - 6).
				Render(msg.Content)
			parts = append(parts, label+"\n"+body)

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

// recalcLayout updates sub-model sizes when the terminal is resized.
func (m Model) recalcLayout() Model {
	eyesH := len(eyesActing) // 9 lines
	headerH := 1
	sepH := 2 // two separators
	inputH := 7 // border + label + textarea(3) + optional thinking/banner
	statusH := 1
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

// ── helpers ───────────────────────────────────────────────────────────────────

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
  /models        open model selector dialog
  /models p:m    set model directly
  /permission    set permission: yolo | restricted | ask-first
  /approve       approve and execute pending plan (coding mode)
  /image <path>  queue image for next chat message
  /images        list queued images
  /index         index project files → .spettro/index.json
  /coauthor      show co-author info for git commits

keys:
  shift+tab      cycle mode (planning → coding → chat)
  f2             cycle to next model
  shift+f2       cycle to previous model`
