package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
