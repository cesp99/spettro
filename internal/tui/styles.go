package tui

import "github.com/charmbracelet/lipgloss"

// Color palette
var (
	colorPlanning = lipgloss.Color("#A78BFA")
	colorCoding   = lipgloss.Color("#34D399")
	colorChat     = lipgloss.Color("#60A5FA")

	colorText    = lipgloss.Color("#F9FAFB")
	colorMuted   = lipgloss.Color("#6B7280")
	colorDim     = lipgloss.Color("#374151")
	colorBorder  = lipgloss.Color("#4B5563")
	colorSuccess = lipgloss.Color("#10B981")
	colorError   = lipgloss.Color("#EF4444")
	colorWarn    = lipgloss.Color("#F59E0B")

	colorToolPend = lipgloss.Color("#F59E0B")
	colorToolRun  = lipgloss.Color("#60A5FA")
	colorToolOK   = lipgloss.Color("#10B981")
	colorToolErr  = lipgloss.Color("#EF4444")
)

func modeColor(mode string) lipgloss.Color {
	switch mode {
	case "planning":
		return colorPlanning
	case "coding":
		return colorCoding
	case "chat":
		return colorChat
	default:
		return colorChat
	}
}

func modePrompt(mode string) string {
	switch mode {
	case "planning":
		return "◈"
	case "coding":
		return "◆"
	case "chat":
		return "●"
	default:
		return "›"
	}
}

func modeLabel(mode string) string {
	switch mode {
	case "planning":
		return "planning"
	case "coding":
		return "coding"
	case "chat":
		return "chat"
	default:
		return mode
	}
}

var (
	styleBold = lipgloss.NewStyle().Bold(true)

	styleMuted = lipgloss.NewStyle().Foreground(colorMuted)
	styleDim   = lipgloss.NewStyle().Foreground(colorDim)
	styleText  = lipgloss.NewStyle().Foreground(colorText)

	styleSuccess = lipgloss.NewStyle().Foreground(colorSuccess)
	styleError   = lipgloss.NewStyle().Foreground(colorError)
	styleWarn    = lipgloss.NewStyle().Foreground(colorWarn)

	styleToolPend = lipgloss.NewStyle().Foreground(colorToolPend)
	styleToolRun  = lipgloss.NewStyle().Foreground(colorToolRun)
	styleToolOK   = lipgloss.NewStyle().Foreground(colorToolOK)
	styleToolErr  = lipgloss.NewStyle().Foreground(colorToolErr)
)

func modeStyle(mode string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(modeColor(mode)).Bold(true)
}

func modeBorderStyle(mode string) lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(modeColor(mode)).
		PaddingLeft(1).PaddingRight(1)
}

func dimBorderStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		PaddingLeft(1).PaddingRight(1)
}
