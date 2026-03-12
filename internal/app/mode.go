package app

import "strings"

type Mode string

const (
	ModePlanning Mode = "planning"
	ModeCoding   Mode = "coding"
	ModeChat     Mode = "chat"
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
	switch normalized {
	case "/next", "shift+tab", ":next":
		return true
	case "\x1b[z", "\x1b[Z":
		return true
	default:
		return false
	}
}
