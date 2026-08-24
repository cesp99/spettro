package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"spettro/internal/homedir"
)

// SavedWorkflowsDir is the folder, relative to a project root or to
// ~/.spettro, that holds reusable workflow scripts.
const SavedWorkflowsDir = "workflows"

// Saved is one discovered workflow script.
type Saved struct {
	Name  string
	Path  string
	Meta  Meta
	Scope string // "project" or "global"
	// Err is set when the file exists but its header does not parse, so the
	// listing can show the problem instead of hiding the file.
	Err error
}

// SearchPaths returns the directories scanned for saved workflows, project
// first. A project script shadows a global one with the same name.
func SearchPaths(cwd string) []string {
	var paths []string
	if strings.TrimSpace(cwd) != "" {
		paths = append(paths, filepath.Join(cwd, ".spettro", SavedWorkflowsDir))
	}
	if home, err := homedir.Dir(); err == nil {
		paths = append(paths, filepath.Join(home, ".spettro", SavedWorkflowsDir))
	}
	return paths
}

// Discover lists every saved workflow visible from cwd, sorted by name.
func Discover(cwd string) []Saved {
	seen := map[string]bool{}
	var out []Saved
	for i, dir := range SearchPaths(cwd) {
		scope := "project"
		if i > 0 {
			scope = "global"
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".js")
			if seen[name] {
				continue
			}
			seen[name] = true
			path := filepath.Join(dir, e.Name())
			s := Saved{Name: name, Path: path, Scope: scope}
			// Validate, not just ParseMeta: a listing that shows a script as
			// runnable when its body does not compile sends the user into a
			// failure they could have been told about here.
			if data, err := os.ReadFile(path); err != nil {
				s.Err = err
			} else if meta, err := Validate(string(data)); err != nil {
				s.Meta, s.Err = meta, err
			} else {
				s.Meta = meta
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Save writes a script to the saved-workflow folder so it can be re-run by
// name. scope is "global" for ~/.spettro/workflows, anything else for the
// project's own folder.
//
// The script is validated first: a saved workflow that does not compile is a
// trap for whoever runs it next, and the failure would surface far from
// whoever wrote it.
func Save(cwd, name, scope, script string) (string, error) {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return "", err
	}
	if _, err := Validate(script); err != nil {
		return "", fmt.Errorf("refusing to save %q: %w", name, err)
	}
	paths := SearchPaths(cwd)
	if len(paths) == 0 {
		return "", fmt.Errorf("no workflow directory available")
	}
	dir := paths[0]
	if scope == "global" {
		home, err := homedir.Dir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		dir = filepath.Join(home, ".spettro", SavedWorkflowsDir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, name+".js")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func validName(name string) error {
	if name == "" {
		return fmt.Errorf("workflow name is required")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("invalid workflow name %q", name)
	}
	return nil
}

// Load reads a saved workflow's script by name.
func Load(cwd, name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return "", "", err
	}
	for _, dir := range SearchPaths(cwd) {
		path := filepath.Join(dir, name+".js")
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), path, nil
		}
		if !os.IsNotExist(err) {
			return "", "", fmt.Errorf("read workflow %q: %w", name, err)
		}
	}
	return "", "", fmt.Errorf("no saved workflow named %q (looked in %s)", name, strings.Join(SearchPaths(cwd), ", "))
}
