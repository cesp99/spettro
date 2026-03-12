package indexer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

type Snapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	Root        string    `json:"root"`
	Entries     []Entry   `json:"entries"`
}

func Build(root string) (Snapshot, error) {
	var entries []Entry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && shouldSkip(rel) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if shouldSkip(rel) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, Entry{
			Path:    filepath.ToSlash(rel),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
		})
		return nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("walk project: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return Snapshot{
		GeneratedAt: time.Now().UTC(),
		Root:        root,
		Entries:     entries,
	}, nil
}

func WriteJSON(snapshot Snapshot, outputPath string) error {
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	return os.WriteFile(outputPath, raw, 0o644)
}

func shouldSkip(rel string) bool {
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(rel, ".git/") || rel == ".git" {
		return true
	}
	if strings.HasPrefix(rel, ".spettro/") || rel == ".spettro" {
		return true
	}
	return false
}
