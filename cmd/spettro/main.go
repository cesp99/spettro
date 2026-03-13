package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"spettro/internal/config"
	"spettro/internal/provider"
	"spettro/internal/storage"
	"spettro/internal/tui"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fatal("cwd error: %v", err)
	}

	store, err := storage.New(cwd)
	if err != nil {
		fatal("storage error: %v", err)
	}

	cfg, err := config.LoadOrCreate()
	if err != nil {
		fatal("config error: %v", err)
	}
	keys, err := config.LoadAPIKeys()
	if err != nil {
		fatal("keys error: %v", err)
	}
	cfg.APIKeys = keys

	pm := provider.NewManager()
	m := tui.New(cwd, cfg, store, pm)

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fatal("runtime error: %v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
