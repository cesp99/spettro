package conversation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Message struct {
	Role     string    `json:"role"`
	Content  string    `json:"content"`
	Thinking string    `json:"thinking,omitempty"`
	Meta     string    `json:"meta,omitempty"`
	At       time.Time `json:"at"`
}

type Conversation struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	Messages  []Message `json:"messages"`
}

type Summary struct {
	ID        string
	StartedAt time.Time
	Path      string
	Preview   string // first user message text, truncated
}

func NewID() string {
	return time.Now().Format("20060102-150405")
}

func Save(dir string, conv Conversation) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, conv.ID+".json")
	raw, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o644)
}

func Load(path string) (Conversation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Conversation{}, err
	}
	var conv Conversation
	return conv, json.Unmarshal(data, &conv)
}

// List returns summaries of all saved conversations in dir, sorted newest first.
func List(dir string) ([]Summary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Summary
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		conv, err := Load(p)
		if err != nil {
			continue
		}
		preview := ""
		for _, m := range conv.Messages {
			if m.Role == "user" {
				preview = m.Content
				if len(preview) > 60 {
					preview = preview[:60] + "…"
				}
				break
			}
		}
		out = append(out, Summary{
			ID:        conv.ID,
			StartedAt: conv.StartedAt,
			Path:      p,
			Preview:   preview,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

// ProjectDir returns the path under ~/.spettro/conversations/ that is dedicated
// to a specific working directory. It uses "<basename>-<8-char hash>" so that
// two projects with the same folder name don't collide, and the directory is
// still human-readable.
func ProjectDir(globalDir, cwd string) string {
	sum := sha256.Sum256([]byte(cwd))
	slug := fmt.Sprintf("%s-%x", filepath.Base(cwd), sum[:4])
	return filepath.Join(globalDir, "conversations", slug)
}
