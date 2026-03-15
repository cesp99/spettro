package session

import (
	"os"
	"sort"
	"strings"

	"spettro/internal/conversation"
)

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
