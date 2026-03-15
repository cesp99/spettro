package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"spettro/internal/conversation"
)

const (
	metadataFilename = "session.json"
	messagesFilename = "messages.json"
	todosFilename    = "todos.json"
	agentsFilename   = "agents.jsonl"
)

type Message struct {
	Role     string    `json:"role"`
	Content  string    `json:"content"`
	Thinking string    `json:"thinking,omitempty"`
	Meta     string    `json:"meta,omitempty"`
	At       time.Time `json:"at"`
}

type Todo struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	Owner     string    `json:"owner,omitempty"`
	Source    string    `json:"source,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentEvent struct {
	At            time.Time `json:"at"`
	AgentID       string    `json:"agent_id"`
	AgentType     string    `json:"agent_type,omitempty"`
	ParentAgentID string    `json:"parent_agent_id,omitempty"`
	Task          string    `json:"task,omitempty"`
	Status        string    `json:"status"`
	Summary       string    `json:"summary,omitempty"`
}

type Metadata struct {
	ID          string    `json:"id"`
	ProjectPath string    `json:"project_path"`
	ProjectHash string    `json:"project_hash"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type State struct {
	Metadata Metadata
	Messages []Message
	Todos    []Todo
	Events   []AgentEvent
}

type Summary struct {
	ID        string
	StartedAt time.Time
	UpdatedAt time.Time
	Path      string
	Preview   string
	Legacy    bool
}

func NewID(cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		now := time.Now().UnixNano()
		copy(suffix[:], fmt.Sprintf("%08x", now))
	}
	return fmt.Sprintf("session-%x-%s", sum[:4], hex.EncodeToString(suffix[:]))
}

func ProjectHash(cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	return fmt.Sprintf("%x", sum[:8])
}

func SessionsDir(globalDir string) string {
	return filepath.Join(globalDir, "sessions")
}

func SessionDir(globalDir, id string) string {
	return filepath.Join(SessionsDir(globalDir), id)
}

func Save(globalDir string, state State) error {
	if state.Metadata.ID == "" {
		return fmt.Errorf("session id is required")
	}
	dir := SessionDir(globalDir, state.Metadata.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if state.Metadata.StartedAt.IsZero() {
		state.Metadata.StartedAt = time.Now()
	}
	state.Metadata.UpdatedAt = time.Now()
	if err := writeJSON(filepath.Join(dir, metadataFilename), state.Metadata); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, messagesFilename), state.Messages); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(dir, todosFilename), state.Todos); err != nil {
		return err
	}
	return rewriteEvents(filepath.Join(dir, agentsFilename), state.Events)
}

func AppendEvent(globalDir, sessionID string, event AgentEvent) error {
	if sessionID == "" {
		return nil
	}
	dir := SessionDir(globalDir, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	f, err := os.OpenFile(filepath.Join(dir, agentsFilename), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return nil
}

func Load(globalDir, sessionID string) (State, error) {
	dir := SessionDir(globalDir, sessionID)
	metaPath := filepath.Join(dir, metadataFilename)
	msgPath := filepath.Join(dir, messagesFilename)
	todoPath := filepath.Join(dir, todosFilename)
	agentPath := filepath.Join(dir, agentsFilename)

	var meta Metadata
	if err := readJSON(metaPath, &meta); err != nil {
		return State{}, err
	}
	var messages []Message
	if err := readJSON(msgPath, &messages); err != nil {
		return State{}, err
	}
	var todos []Todo
	if err := readJSONIfExists(todoPath, &todos); err != nil {
		return State{}, err
	}
	events, err := readEvents(agentPath)
	if err != nil {
		return State{}, err
	}
	return State{Metadata: meta, Messages: messages, Todos: todos, Events: events}, nil
}

func LoadTodos(globalDir, sessionID string) ([]Todo, error) {
	if sessionID == "" {
		return nil, nil
	}
	var todos []Todo
	err := readJSONIfExists(filepath.Join(SessionDir(globalDir, sessionID), todosFilename), &todos)
	return todos, err
}

func List(globalDir, cwd string) ([]Summary, error) {
	var out []Summary
	projectHash := ProjectHash(cwd)
	entries, err := os.ReadDir(SessionsDir(globalDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-"+projectHash) {
			continue
		}
		state, err := Load(globalDir, entry.Name())
		if err != nil {
			continue
		}
		out = append(out, Summary{
			ID:        state.Metadata.ID,
			StartedAt: state.Metadata.StartedAt,
			UpdatedAt: state.Metadata.UpdatedAt,
			Path:      SessionDir(globalDir, state.Metadata.ID),
			Preview:   firstUserPreview(state.Messages),
		})
	}

	legacyDir := conversation.ProjectDir(globalDir, cwd)
	legacy, err := conversation.List(legacyDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, item := range legacy {
		out = append(out, Summary{
			ID:        item.ID,
			StartedAt: item.StartedAt,
			UpdatedAt: item.StartedAt,
			Path:      item.Path,
			Preview:   item.Preview,
			Legacy:    true,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func StateFromLegacy(conv conversation.Conversation, cwd string) State {
	msgs := make([]Message, 0, len(conv.Messages))
	for _, msg := range conv.Messages {
		msgs = append(msgs, Message{
			Role:     msg.Role,
			Content:  msg.Content,
			Thinking: msg.Thinking,
			Meta:     msg.Meta,
			At:       msg.At,
		})
	}
	return State{
		Metadata: Metadata{
			ID:          conv.ID,
			ProjectPath: cwd,
			ProjectHash: ProjectHash(cwd),
			StartedAt:   conv.StartedAt,
			UpdatedAt:   conv.StartedAt,
		},
		Messages: msgs,
	}
}

func firstUserPreview(messages []Message) string {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		preview := msg.Content
		if len(preview) > 60 {
			return preview[:60] + "…"
		}
		return preview
	}
	return ""
}

func readEvents(path string) ([]AgentEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	out := make([]AgentEvent, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev AgentEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

func rewriteEvents(path string, events []AgentEvent) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(raw, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func readJSONIfExists(path string, target any) error {
	if err := readJSON(path, target); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}
