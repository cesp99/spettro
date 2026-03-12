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

func IsCtrlTabInput(s string) bool {
	normalized := strings.TrimSpace(strings.ToLower(s))
	switch normalized {
	case "/next", "ctrl+tab", ":next":
		return true
	case "\x1b[27;5;9~", "\x1b[9;5u":
		return true
	default:
		return false
	}
}
