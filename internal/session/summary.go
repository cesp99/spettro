package session

import (
	"os"
	"sort"
)

func List(globalDir, cwd string) ([]Summary, error) {
	var out []Summary
	projectHash := ProjectHash(cwd)
	entries, err := os.ReadDir(SessionsDir(globalDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		state, err := Load(globalDir, entry.Name())
		if err != nil {
			continue
		}
		if state.Metadata.ProjectHash != projectHash && state.Metadata.ProjectPath != cwd {
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

	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
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
