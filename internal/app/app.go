package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"spettro/internal/agent"
	"spettro/internal/config"
	"spettro/internal/indexer"
	"spettro/internal/provider"
	"spettro/internal/storage"
	"spettro/internal/ui"
)

type App struct {
	in  io.Reader
	out io.Writer

	mode        Mode
	cwd         string
	cfg         config.UserConfig
	store       *storage.Store
	providers   *provider.Manager
	manifest    config.AgentManifest
	pendingPlan string
	pendingImgs []string
	ui          *ui.Renderer
	setup       *setupWizard
	modelPicker *modelPicker
	reader      *bufio.Reader
}

type setupWizard struct {
	step     int
	provider string
	model    string
}

type modelPicker struct {
	filter string
	items  []provider.Model
}

func New(in io.Reader, out io.Writer, cwdFn func() (string, error)) (*App, error) {
	cwd, err := cwdFn()
	if err != nil {
		return nil, err
	}

	store, err := storage.New(cwd)
	if err != nil {
		return nil, err
	}

	cfg, err := config.LoadOrCreate()
	if err != nil {
		return nil, err
	}
	keys, err := config.LoadAPIKeys()
	if err != nil {
		return nil, err
	}
	cfg.APIKeys = keys

	pm := provider.NewManager()
	for _, endpoint := range cfg.LocalEndpoints {
		localModels, err := provider.ProbeLocalServer(context.Background(), endpoint)
		if err != nil {
			continue
		}
		pm.AddLocalModels(localModels)
	}
	manifest, _ := config.LoadAgentManifestForProject(cwd)
	app := &App{
		in:        in,
		out:       out,
		mode:      ModePlanning,
		cwd:       cwd,
		cfg:       cfg,
		store:     store,
		providers: pm,
		manifest:  manifest,
		ui:        ui.NewRenderer(),
	}
	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	a.reader = bufio.NewReader(a.in)
	reader := a.reader
	a.printLine(a.ui.Welcome())
	a.printLine(a.ui.Info(a.ui.Stage(string(a.mode))))
	a.printStatus()
	if strings.TrimSpace(a.cfg.APIKeys[a.cfg.ActiveProvider]) == "" {
		a.printLine(a.ui.Panel(string(a.mode), "Setup Required", "Run /setup to configure provider, model and encrypted API key storage."))
	}

	for {
		fmt.Fprintf(a.out, "%s ", a.ui.Prompt(string(a.mode), a.cfg.ActiveProvider, a.cfg.ActiveModel))
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if a.setup != nil {
			if err := a.handleSetupInput(line); err != nil {
				a.printLine("setup error: " + err.Error())
			}
			continue
		}

		if a.modelPicker != nil {
			if err := a.handleModelPickerInput(line); err != nil {
				a.printLine("models error: " + err.Error())
			}
			continue
		}

		if IsModeSwitchInput(line) {
			a.mode = a.mode.Next()
			a.printLine(a.ui.Info(a.ui.Stage(string(a.mode))))
			a.printStatus()
			continue
		}

		if strings.HasPrefix(line, "/") {
			if err := a.handleCommand(line); err != nil {
				if err == io.EOF {
					return nil
				}
				a.printLine("error: " + err.Error())
			}
			continue
		}

		switch a.mode {
		case ModePlanning:
			if err := a.handlePlanning(ctx, line); err != nil {
				a.printLine("planning error: " + err.Error())
			}
		case ModeCoding:
			if err := a.handleCoding(ctx, line); err != nil {
				a.printLine("coding error: " + err.Error())
			}
		case ModeChat:
			if err := a.handleChat(ctx, line); err != nil {
				a.printLine("chat error: " + err.Error())
			}
		}
	}
}

func (a *App) handleCommand(line string) error {
	fields := strings.Fields(line)
	switch fields[0] {
	case "/help":
		a.printLine(a.ui.Panel(string(a.mode), "Commands", "/setup, /next (Shift+Tab), /mode, /models [provider:model] [api_key], /permission <yolo|restricted|ask-first>, /image <path>, /images, /index, /approve, /exit\nUse /models with no args for interactive picker."))
	case "/exit", "/quit":
		return io.EOF
	case "/setup":
		return a.startSetup()
	case "/login":
		a.printLine("deprecated: use /setup")
		return a.startSetup()
	case "/mode":
		a.mode = a.mode.Next()
		a.printLine(a.ui.Info(a.ui.Stage(string(a.mode))))
		a.printStatus()
	case "/models":
		if len(fields) < 2 {
			a.startModelPicker("")
			return nil
		}
		if !strings.Contains(fields[1], ":") {
			a.startModelPicker(fields[1])
			return nil
		}
		pair := strings.SplitN(fields[1], ":", 2)
		if len(pair) != 2 {
			return fmt.Errorf("invalid model selector")
		}
		if !a.providers.HasModel(pair[0], pair[1]) {
			return fmt.Errorf("unknown provider/model pair: %s", fields[1])
		}
		a.cfg.ActiveProvider = pair[0]
		a.cfg.ActiveModel = pair[1]
		if len(fields) >= 3 {
			if err := config.SaveAPIKey(pair[0], fields[2]); err != nil {
				return err
			}
			a.cfg.APIKeys[pair[0]] = fields[2]
		}
		if err := config.Save(a.cfg); err != nil {
			return err
		}
		a.printStatus()
	case "/permission":
		if len(fields) < 2 {
			return fmt.Errorf("usage: /permission <yolo|restricted|ask-first>")
		}
		level := config.PermissionLevel(fields[1])
		switch level {
		case config.PermissionYOLO, config.PermissionRestricted, config.PermissionAskFirst:
		default:
			return fmt.Errorf("invalid permission level")
		}
		a.cfg.Permission = level
		return config.Save(a.cfg)
	case "/approve":
		if strings.TrimSpace(a.pendingPlan) == "" {
			a.printLine("no pending plan to approve")
			return nil
		}
		spec, ok := a.manifest.AgentByID("coding")
		if !ok {
			return fmt.Errorf("coding agent not found")
		}
		ag := agent.LLMAgent{
			Spec:            spec,
			ProviderManager: a.providers,
			ProviderName:    func() string { return a.cfg.ActiveProvider },
			ModelName:       func() string { return a.cfg.ActiveModel },
			CWD:             a.cwd,
			ShellApproval:   a.promptShellApproval,
		}
		ag.Spec.Permission = a.cfg.Permission
		result, err := ag.Run(context.Background(), a.pendingPlan)
		if err != nil {
			return err
		}
		a.printLine(a.ui.Panel(string(a.mode), "Assistant", result.Content))
		a.pendingPlan = ""
	case "/image":
		if len(fields) < 2 {
			return fmt.Errorf("usage: /image <path>")
		}
		target := fields[1]
		if !filepath.IsAbs(target) {
			target = filepath.Join(a.cwd, target)
		}
		if _, err := os.Stat(target); err != nil {
			return fmt.Errorf("image path error: %w", err)
		}
		a.pendingImgs = append(a.pendingImgs, target)
		a.printLine("queued image for next chat request")
	case "/images":
		if len(a.pendingImgs) == 0 {
			a.printLine(a.ui.Info("no queued images"))
			return nil
		}
		a.printLine(a.ui.Panel(string(a.mode), "Queued Images", strings.Join(a.pendingImgs, "\n")))
	case "/index":
		snapshot, err := indexer.Build(a.cwd)
		if err != nil {
			return err
		}
		if err := indexer.WriteJSON(snapshot, filepath.Join(a.store.ProjectDir, "index.json")); err != nil {
			return err
		}
		a.printLine(a.ui.Panel(string(a.mode), "Indexer", fmt.Sprintf("Indexed %d files into .spettro/index.json", len(snapshot.Entries))))
	default:
		return fmt.Errorf("unknown command: %s", fields[0])
	}
	return nil
}

func (a *App) startSetup() error {
	a.setup = &setupWizard{}
	a.printLine(a.ui.Panel(string(a.mode), "Initial Setup", "Let's configure Spettro.\nType /cancel to abort setup at any step."))
	a.printLine("Select provider:")
	a.printLine("1) openai-compatible")
	a.printLine("2) anthropic")
	a.printLine("Enter provider name or number:")
	return nil
}

func (a *App) handleSetupInput(line string) error {
	if strings.EqualFold(line, "/cancel") {
		a.setup = nil
		a.printLine("setup canceled")
		return nil
	}

	switch a.setup.step {
	case 0:
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "1", "openai-compatible":
			a.setup.provider = "openai-compatible"
		case "2", "anthropic":
			a.setup.provider = "anthropic"
		default:
			return fmt.Errorf("invalid provider, choose 1/2 or provider name")
		}
		a.setup.step = 1
		a.printLine("Select model:")
		for _, m := range a.providers.Models() {
			if m.Provider == a.setup.provider {
				a.printLine(fmt.Sprintf("- %s", m.Name))
			}
		}
		a.printLine("Enter model name:")
		return nil
	case 1:
		model := strings.TrimSpace(line)
		if !a.providers.HasModel(a.setup.provider, model) {
			return fmt.Errorf("unknown model for provider %s", a.setup.provider)
		}
		a.setup.model = model
		a.setup.step = 2
		a.printLine("Paste API key (input is not masked in current terminal):")
		return nil
	case 2:
		key := strings.TrimSpace(line)
		if key == "" {
			return fmt.Errorf("api key cannot be empty")
		}
		if err := config.SaveAPIKey(a.setup.provider, key); err != nil {
			return err
		}
		if a.cfg.APIKeys == nil {
			a.cfg.APIKeys = map[string]string{}
		}
		a.cfg.APIKeys[a.setup.provider] = key
		a.cfg.ActiveProvider = a.setup.provider
		a.cfg.ActiveModel = a.setup.model
		a.setup.step = 3
		a.printLine("Choose default permission:")
		a.printLine("1) ask-first")
		a.printLine("2) restricted")
		a.printLine("3) yolo")
		a.printLine("Enter value:")
		return nil
	case 3:
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "1", "ask-first":
			a.cfg.Permission = config.PermissionAskFirst
		case "2", "restricted":
			a.cfg.Permission = config.PermissionRestricted
		case "3", "yolo":
			a.cfg.Permission = config.PermissionYOLO
		default:
			return fmt.Errorf("invalid permission, choose ask-first/restricted/yolo")
		}

		if err := config.Save(a.cfg); err != nil {
			return err
		}
		a.setup = nil
		a.printLine(a.ui.Panel(string(a.mode), "Setup Complete", fmt.Sprintf("Active provider/model: %s:%s", a.cfg.ActiveProvider, a.cfg.ActiveModel)))
		a.printStatus()
		return nil
	default:
		return fmt.Errorf("invalid setup state")
	}
}

func (a *App) handlePlanning(ctx context.Context, prompt string) error {
	spec, ok := a.manifest.AgentByID("plan")
	if !ok {
		return fmt.Errorf("plan agent not found")
	}
	ag := agent.LLMAgent{
		Spec:            spec,
		ProviderManager: a.providers,
		ProviderName:    func() string { return a.cfg.ActiveProvider },
		ModelName:       func() string { return a.cfg.ActiveModel },
		CWD:             a.cwd,
	}
	result, err := ag.Run(ctx, prompt)
	if err != nil {
		return err
	}
	if err := a.store.WriteProjectFile("PLAN.md", result.Content); err != nil {
		return err
	}
	a.pendingPlan = result.Content
	a.printLine(a.ui.Panel(string(a.mode), "Plan Generated", "Saved to .spettro/PLAN.md.\nUse /approve in coding flow to execute."))
	return nil
}

func (a *App) handleCoding(ctx context.Context, prompt string) error {
	if a.cfg.Permission == config.PermissionAskFirst {
		a.printLine("ask-first mode: generate plan in planning mode, then use /approve")
		return nil
	}
	spec, ok := a.manifest.AgentByID("coding")
	if !ok {
		return fmt.Errorf("coding agent not found")
	}
	ag := agent.LLMAgent{
		Spec:            spec,
		ProviderManager: a.providers,
		ProviderName:    func() string { return a.cfg.ActiveProvider },
		ModelName:       func() string { return a.cfg.ActiveModel },
		CWD:             a.cwd,
		ShellApproval:   a.promptShellApproval,
	}
	ag.Spec.Permission = a.cfg.Permission
	result, err := ag.Run(ctx, prompt)
	if err != nil {
		return err
	}
	a.printLine(a.ui.Panel(string(a.mode), "Assistant", result.Content))
	return nil
}

func (a *App) handleChat(ctx context.Context, prompt string) error {
	spec, ok := a.manifest.AgentByID("ask")
	if !ok {
		return fmt.Errorf("ask agent not found")
	}
	ag := agent.LLMAgent{
		Spec:            spec,
		ProviderManager: a.providers,
		ProviderName:    func() string { return a.cfg.ActiveProvider },
		ModelName:       func() string { return a.cfg.ActiveModel },
		CWD:             a.cwd,
		Images:          a.pendingImgs,
	}
	result, err := ag.Run(ctx, prompt)
	if err != nil {
		return err
	}
	a.pendingImgs = nil
	a.printLine(a.ui.Panel(string(a.mode), "Assistant", result.Content))
	return nil
}

func (a *App) printModels() {
	a.printLine("available models:")
	for _, m := range a.providers.Models() {
		a.printLine(a.ui.Info(fmt.Sprintf("- %s:%s (vision=%t)", m.Provider, m.Name, m.Vision)))
	}
}

func (a *App) startModelPicker(prefix string) {
	a.modelPicker = &modelPicker{filter: strings.ToLower(strings.TrimSpace(prefix))}
	a.modelPicker.items = a.modelPickerMatches(a.modelPicker.filter)
	if len(a.modelPicker.items) == 0 {
		a.printLine("no model matches found")
		a.modelPicker = nil
		return
	}
	a.printLine(a.ui.Panel(string(a.mode), "Model Picker", "Type a number to select model.\nType text to filter.\nType /cancel to close picker."))
	for i, m := range a.modelPicker.items {
		a.printLine(a.ui.Info(fmt.Sprintf("%d) %s:%s (vision=%t)", i+1, m.Provider, m.Name, m.Vision)))
	}
}

func (a *App) handleModelPickerInput(line string) error {
	if strings.EqualFold(line, "/cancel") {
		a.modelPicker = nil
		a.printLine("model picker closed")
		return nil
	}
	if n, err := parseSelection(line); err == nil {
		if n < 1 || n > len(a.modelPicker.items) {
			return fmt.Errorf("selection out of range")
		}
		selected := a.modelPicker.items[n-1]
		a.cfg.ActiveProvider = selected.Provider
		a.cfg.ActiveModel = selected.Name
		if err := config.Save(a.cfg); err != nil {
			return err
		}
		a.modelPicker = nil
		a.printStatus()
		return nil
	}
	a.startModelPicker(line)
	return nil
}

func parseSelection(line string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &n)
	return n, err
}

func (a *App) modelPickerMatches(prefix string) []provider.Model {
	matches := make([]provider.Model, 0)
	for _, m := range a.providers.Models() {
		full := strings.ToLower(m.Provider + ":" + m.Name)
		if prefix == "" || strings.Contains(full, prefix) {
			matches = append(matches, m)
		}
	}
	return matches
}

func (a *App) printStatus() {
	a.printLine(a.ui.Status(string(a.mode), string(a.cfg.Permission)))
}

func (a *App) printLine(s string) {
	fmt.Fprintln(a.out, s)
}

func (a *App) promptShellApproval(ctx context.Context, req agent.ShellApprovalRequest) (agent.ShellApprovalDecision, error) {
	if a.reader == nil {
		a.reader = bufio.NewReader(a.in)
	}
	for {
		if err := ctx.Err(); err != nil {
			return agent.ShellApprovalDeny, err
		}
		a.printLine(formatShellApprovalPrompt(req.Command))
		fmt.Fprint(a.out, "> ")
		line, err := a.reader.ReadString('\n')
		if err != nil {
			return agent.ShellApprovalDeny, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "1", "y", "yes":
			return agent.ShellApprovalAllowOnce, nil
		case "2", "a", "always", "yes and don't ask again", "yes and dont ask again":
			return agent.ShellApprovalAllowAlways, nil
		case "3", "n", "no":
			return agent.ShellApprovalDeny, nil
		case "4":
			a.printLine("type what the agent should do instead:")
			fmt.Fprint(a.out, "> ")
			instead, rerr := a.reader.ReadString('\n')
			if rerr != nil {
				return agent.ShellApprovalDeny, rerr
			}
			instead = strings.TrimSpace(instead)
			if instead == "" {
				a.printLine("empty alternative instruction; command denied")
				return agent.ShellApprovalDeny, nil
			}
			return agent.ShellApprovalDeny, fmt.Errorf("shell-exec denied by user; do this instead: %s", instead)
		default:
			text := strings.TrimSpace(line)
			if text != "" {
				return agent.ShellApprovalDeny, fmt.Errorf("shell-exec denied by user; do this instead: %s", text)
			}
			a.printLine("invalid choice; use 1, 2, 3, or 4")
		}
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
