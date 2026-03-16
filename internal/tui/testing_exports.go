package tui

import (
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"spettro/internal/storage"
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

func SanitizeToolOutputForTesting(output string, maxLines int) string {
	return sanitizeToolOutput(output, maxLines)
}

func ShellApprovalOptionsForTesting() []string {
	return append([]string(nil), shellApprovalOptions...)
}

func NewModelForTesting() Model {
	ta := textarea.New()
	ta.Focus()
	tmp := filepath.Join(os.TempDir(), "spettro-tui-tests")
	return Model{
		ta:    ta,
		cwd:   tmp,
		store: &storage.Store{ProjectDir: filepath.Join(tmp, ".spettro"), GlobalDir: tmp},
	}
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

func (m *Model) SetThinkingForTesting(v bool) {
	m.thinking = v
}

func (m *Model) SetActiveAgentForTesting(id string) {
	m.activeAgentID = id
}

func (m *Model) SetLiveToolsForTesting(tools []ToolItem, current *ToolItem) {
	m.liveTools = append([]ToolItem(nil), tools...)
	if current == nil {
		m.currentTool = nil
		return
	}
	cp := *current
	m.currentTool = &cp
}

func (m Model) PendingPromptCountForTesting() int {
	return len(m.pendingPrompts)
}

func (m Model) AwaitingInsteadForTesting() bool {
	return m.awaitingInstead
}

func (m Model) BannerForTesting() string {
	return m.banner
}

func (m Model) ProgressNoteForTesting() string {
	return m.progressNote
}

func (m Model) UpdateMainForTesting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.updateMain(msg)
}

func (m Model) UpdateShellApprovalForTesting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.updateShellApproval(msg)
}

func (m *Model) SetDimensionsForTesting(width, height int) {
	m.width = width
	m.height = height
}

func (m *Model) SetSidePanelVisibleForTesting(v bool) {
	m.showSidePanel = v
}

func (m *Model) SetShowToolsForTesting(v bool) {
	m.showTools = v
}

func (m *Model) AddActivityForTesting(kind, id, agentID, title, detail, body, status string) {
	m.activityFeed = append(m.activityFeed, activityItem{
		Key:     title + time.Now().Format(time.RFC3339Nano),
		Kind:    kind,
		ID:      id,
		AgentID: agentID,
		Title:   title,
		Detail:  detail,
		Body:    body,
		Status:  status,
		At:      time.Now(),
	})
}

func (m Model) SidePanelWidthForTesting() int {
	return m.sidePanelWidth()
}

func (m Model) ViewSidePanelForTesting(width int) string {
	return m.viewSidePanel(width)
}
