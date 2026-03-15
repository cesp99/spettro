package app

import "strings"

type Mode string

const (
	ModePlanning Mode = "plan"
	ModeCoding   Mode = "coding"
	ModeChat     Mode = "ask"
)

func (m Mode) Next() Mode {
	switch m {
	case ModePlanning:
		return ModeCoding
	case ModeCoding:
		return ModeChat
	default:
		return ModePlanning
	}
}

func IsModeSwitchInput(s string) bool {
	normalized := strings.TrimSpace(strings.ToLower(s))
	switch {
	case normalized == "/next", normalized == "shift+tab", normalized == ":next":
		return true
	case strings.Contains(s, "\x1b[Z"), strings.Contains(s, "\x1b[z"), strings.Contains(normalized, "^[z"):
		return true
	default:
		return false
	}
}
