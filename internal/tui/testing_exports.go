package tui

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func RenderMarkdownForTesting(md string, width int) string {
	return renderMarkdown(md, width)
}

func PrefixBlockWithBulletForTesting(bullet, body string) string {
	return prefixBlockWithBullet(bullet, body)
}

func FormatToolLabelForTesting(name, argsJSON string) string {
	return formatToolLabel(name, argsJSON)
}

func FormatRunningLabelForTesting(name, argsJSON string) string {
	return formatRunningLabel(name, argsJSON)
}

func ShellApprovalOptionsForTesting() []string {
	return append([]string(nil), shellApprovalOptions...)
}

func NewModelForTesting() Model {
	ta := textarea.New()
	ta.Focus()
	return Model{ta: ta}
}

func (m *Model) SetTextareaValueForTesting(v string) {
	m.ta.SetValue(v)
}

func (m *Model) SetCommandItemsForTesting(items []string) {
	m.cmdItems = make([]commandDef, 0, len(items))
	for _, item := range items {
		m.cmdItems = append(m.cmdItems, commandDef{name: item})
	}
}

func (m *Model) SetInputHistoryForTesting(history []string) {
	m.inputHistory = append([]string(nil), history...)
	m.historyIndex = -1
}

func (m *Model) SetPendingShellApprovalForTesting(cursor int) {
	m.pendingAuth = &shellApprovalRequestMsg{response: make(chan shellApprovalResponse, 1)}
	m.approvalCursor = cursor
}

func (m Model) TextareaValueForTesting() string {
	return m.ta.Value()
}

func (m Model) MessagesForTesting() []ChatMessage {
	return append([]ChatMessage(nil), m.messages...)
}

func (m Model) HistoryBrowsingForTesting() bool {
	return m.historyBrowsing
}

func (m Model) ApprovalCursorForTesting() int {
	return m.approvalCursor
}

func (m Model) HasPendingShellApprovalForTesting() bool {
	return m.pendingAuth != nil
}

func (m Model) UpdateMainForTesting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.updateMain(msg)
}

func (m Model) UpdateShellApprovalForTesting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.updateShellApproval(msg)
}
