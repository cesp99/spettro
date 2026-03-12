package ui

import (
	"fmt"
	"strings"
)

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	blue   = "\033[38;5;39m"
	cyan   = "\033[38;5;45m"
	green  = "\033[38;5;42m"
	yellow = "\033[38;5;220m"
	gray   = "\033[38;5;246m"
	red    = "\033[38;5;203m"
)

type Theme struct {
	Label  string
	Accent string
	Prompt string
}

type Renderer struct {
	themes map[string]Theme
}

func NewRenderer() *Renderer {
	return &Renderer{
		themes: map[string]Theme{
			"planning": {Label: "Planning Agent", Accent: cyan, Prompt: "◈"},
			"coding":   {Label: "Coding Agent", Accent: green, Prompt: "◆"},
			"chat":     {Label: "Chat Agent", Accent: yellow, Prompt: "●"},
		},
	}
}

func (r *Renderer) Welcome() string {
	lines := []string{
		fmt.Sprintf("%s%sSPETTRO%s  %sfast multi-agent coding CLI%s", bold, blue, reset, dim, reset),
		fmt.Sprintf("%sShift+Tab%s switches agents. %s/models%s changes model.", gray, reset, gray, reset),
	}
	return strings.Join(lines, "\n")
}

func (r *Renderer) Prompt(mode, provider, model string) string {
	t := r.theme(mode)
	return fmt.Sprintf("%s%s %s%s%s/%s%s >", t.Accent, t.Prompt, bold, mode, reset, provider, model)
}

func (r *Renderer) Status(mode, permission string) string {
	t := r.theme(mode)
	return fmt.Sprintf("%s[%s]%s %s%s%s  %sperm:%s %s%s%s", t.Accent, t.Label, reset, bold, strings.ToUpper(mode), reset, gray, reset, red, permission, reset)
}

func (r *Renderer) Panel(mode, title, body string) string {
	t := r.theme(mode)
	header := fmt.Sprintf("%s┌─ %s%s%s", t.Accent, bold, title, reset)
	content := fmt.Sprintf("%s│ %s%s", t.Accent, reset, body)
	footer := fmt.Sprintf("%s└─%s", t.Accent, reset)
	return strings.Join([]string{header, content, footer}, "\n")
}

func (r *Renderer) Info(s string) string {
	return fmt.Sprintf("%s%s%s", gray, s, reset)
}

func (r *Renderer) theme(mode string) Theme {
	if t, ok := r.themes[mode]; ok {
		return t
	}
	return Theme{Label: "Unknown", Accent: blue, Prompt: "•"}
}
