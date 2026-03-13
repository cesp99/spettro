package agent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SearchAgent searches the repository for files or content.
type SearchAgent interface {
	Search(ctx context.Context, cwd, query string) (string, error)
}

// RepoSearcher walks the repo tree and optionally greps file contents.
type RepoSearcher struct{}

func (RepoSearcher) Search(_ context.Context, cwd, query string) (string, error) {
	var files []string
	err := filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		rel, _ := filepath.Rel(cwd, path)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".spettro", "vendor", "node_modules", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk: %w", err)
	}

	if query == "" {
		return fmt.Sprintf("%d files:\n%s", len(files), strings.Join(files, "\n")), nil
	}

	q := strings.ToLower(query)
	var results []string
	for _, rel := range files {
		abs := filepath.Join(cwd, rel)
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(strings.ToLower(line), q) {
				results = append(results, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
	}

	if len(results) == 0 {
		return fmt.Sprintf("no matches for %q in %d files", query, len(files)), nil
	}
	return fmt.Sprintf("%d matches:\n%s", len(results), strings.Join(results, "\n")), nil
}
