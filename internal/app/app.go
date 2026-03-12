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
)

type App struct {
	in  io.Reader
	out io.Writer

	mode        Mode
	cwd         string
	cfg         config.UserConfig
	store       *storage.Store
	providers   *provider.Manager
	planner     agent.PlanningAgent
	coder       agent.CodingAgent
	chatter     agent.ChatAgent
	pendingPlan string
	pendingImgs []string
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

	pm := provider.NewManager()
	app := &App{
		in:        in,
		out:       out,
		mode:      ModePlanning,
		cwd:       cwd,
		cfg:       cfg,
		store:     store,
		providers: pm,
		planner:   agent.Planner{},
		coder:     agent.Coder{},
	}
	app.chatter = agent.Chatter{
		ProviderManager: pm,
		ProviderName: func() string {
			return app.cfg.ActiveProvider
		},
		ModelName: func() string {
			return app.cfg.ActiveModel
		},
	}
	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(a.in)
	a.printLine("Spettro CLI MVP. Ctrl+Tab (or /next) switches mode. /help for commands.")
	a.printStatus()

	for {
		fmt.Fprintf(a.out, "[%s %s/%s] > ", a.mode, a.cfg.ActiveProvider, a.cfg.ActiveModel)
		if !scanner.Scan() {
			return scanner.Err()
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if IsCtrlTabInput(line) {
			a.mode = a.mode.Next()
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
		a.printLine("/next, /mode, /models <provider>:<model> [api_key], /permission <yolo|restricted|ask-first>, /image <path>, /images, /index, /approve, /exit")
	case "/exit", "/quit":
		return io.EOF
	case "/mode":
		a.mode = a.mode.Next()
		a.printStatus()
	case "/models":
		a.printModels()
		if len(fields) < 2 {
			a.printLine("usage: /models <provider>:<model> [api_key]")
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
		result, err := a.coder.Execute(context.Background(), a.pendingPlan, a.cfg.Permission, true)
		if err != nil {
			return err
		}
		if err := a.store.AppendProjectFile("AGENT.md", result+"\n"); err != nil {
			return err
		}
		a.printLine("approved and executed. output stored in .spettro/AGENT.md")
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
			a.printLine("no queued images")
			return nil
		}
		a.printLine("queued images:")
		for _, p := range a.pendingImgs {
			a.printLine("- " + p)
		}
	case "/index":
		snapshot, err := indexer.Build(a.cwd)
		if err != nil {
			return err
		}
		if err := indexer.WriteJSON(snapshot, filepath.Join(a.store.ProjectDir, "index.json")); err != nil {
			return err
		}
		a.printLine(fmt.Sprintf("index generated: %d files -> .spettro/index.json", len(snapshot.Entries)))
	default:
		return fmt.Errorf("unknown command: %s", fields[0])
	}
	return nil
}

func (a *App) handlePlanning(ctx context.Context, prompt string) error {
	plan, err := a.planner.Plan(ctx, prompt)
	if err != nil {
		return err
	}
	if err := a.store.WriteProjectFile("PLAN.md", plan); err != nil {
		return err
	}
	a.pendingPlan = plan
	a.printLine("plan generated in .spettro/PLAN.md. run /approve to execute in coding agent.")
	return nil
}

func (a *App) handleCoding(ctx context.Context, prompt string) error {
	if a.cfg.Permission == config.PermissionAskFirst {
		a.printLine("ask-first mode: generate plan in planning mode, then use /approve")
		return nil
	}
	result, err := a.coder.Execute(ctx, prompt, a.cfg.Permission, true)
	if err != nil {
		return err
	}
	if err := a.store.AppendProjectFile("AGENT.md", result+"\n"); err != nil {
		return err
	}
	a.printLine("coding action logged to .spettro/AGENT.md")
	return nil
}

func (a *App) handleChat(ctx context.Context, prompt string) error {
	resp, err := a.chatter.Reply(ctx, prompt, a.pendingImgs)
	if err != nil {
		return err
	}
	a.pendingImgs = nil
	a.printLine(resp.Content)
	a.printLine(fmt.Sprintf("(provider=%s model=%s est_tokens=%d)", resp.Provider, resp.Model, resp.EstimatedTokens))
	return nil
}

func (a *App) printModels() {
	a.printLine("available models:")
	for _, m := range a.providers.Models() {
		a.printLine(fmt.Sprintf("- %s:%s (vision=%t)", m.Provider, m.Name, m.Vision))
	}
}

func (a *App) printStatus() {
	a.printLine(fmt.Sprintf("mode=%s permission=%s", a.mode, a.cfg.Permission))
}

func (a *App) printLine(s string) {
	fmt.Fprintln(a.out, s)
}
