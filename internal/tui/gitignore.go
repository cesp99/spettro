package tui

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// gitignorePattern holds a compiled .gitignore rule.
type gitignorePattern struct {
	raw     string
	negate  bool
	dirOnly bool   // trailing slash in pattern → only matches directories
	rooted  bool   // pattern has a slash before the last segment → root-anchored
	pattern string // cleaned pattern used for matching
}

// gitignoreMatcher loads and applies .gitignore rules from a repository root.
type gitignoreMatcher struct {
	patterns []gitignorePattern
}

// newGitignoreMatcher loads .gitignore files from the given root directory.
// It reads the root .gitignore and any nested ones encountered during the walk.
func newGitignoreMatcher(root string) *gitignoreMatcher {
	m := &gitignoreMatcher{}
	m.loadFile(filepath.Join(root, ".gitignore"))
	return m
}

func (m *gitignoreMatcher) loadFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if p, ok := parsePattern(line); ok {
			m.patterns = append(m.patterns, p)
		}
	}
}

// parsePattern parses a single .gitignore line into a pattern struct.
func parsePattern(line string) (gitignorePattern, bool) {
	// Strip inline comments and trailing spaces
	line = strings.TrimRight(line, " \t\r")
	if line == "" || strings.HasPrefix(line, "#") {
		return gitignorePattern{}, false
	}

	p := gitignorePattern{raw: line}

	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\#`) {
		line = line[1:] // escaped hash
	}

	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}

	// A pattern is rooted if it contains a slash anywhere except at the end
	// (we already stripped the trailing slash above).
	if strings.Contains(line, "/") {
		p.rooted = true
		line = strings.TrimPrefix(line, "/")
	}

	p.pattern = line
	return p, true
}

// Ignored reports whether the given relative path (using forward slashes) should
// be ignored. isDir should be true when the path refers to a directory.
func (m *gitignoreMatcher) Ignored(relPath string, isDir bool) bool {
	// Always allow .gitignore itself to be listed
	ignored := false
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if m.matchPattern(p, relPath, isDir) {
			ignored = !p.negate
		}
	}
	return ignored
}

func (m *gitignoreMatcher) matchPattern(p gitignorePattern, relPath string, isDir bool) bool {
	// Normalise to forward slashes
	relPath = filepath.ToSlash(relPath)

	if p.rooted {
		// Match against the full path from root
		return matchGlob(p.pattern, relPath)
	}

	// Non-rooted: match against any path component suffix
	// e.g. "*.log" should match "foo/bar.log"
	if matchGlob(p.pattern, relPath) {
		return true
	}
	// Also try matching just the base name
	base := filepath.Base(relPath)
	if matchGlob(p.pattern, base) {
		return true
	}
	// Also check every path prefix when pattern contains ** or /
	if strings.Contains(p.pattern, "/") || strings.Contains(p.pattern, "**") {
		parts := strings.Split(relPath, "/")
		for i := range parts {
			sub := strings.Join(parts[i:], "/")
			if matchGlob(p.pattern, sub) {
				return true
			}
		}
	}
	return false
}

// matchGlob matches a gitignore-style glob pattern against a path.
// Supports *, **, ?, and character classes.
// ** matches any number of path segments including none.
func matchGlob(pattern, path string) bool {
	// Fast path: no special chars
	if !strings.ContainsAny(pattern, "*?[\\") {
		return pattern == path || strings.HasSuffix(path, "/"+pattern)
	}

	// Expand ** into a recursive match by trying all possible splits
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, path)
	}

	// Single-level glob via filepath.Match
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		return false
	}
	return matched
}

// matchDoublestar handles ** in patterns by recursively trying all splits.
func matchDoublestar(pattern, path string) bool {
	// Split on **
	parts := strings.SplitN(pattern, "**", 2)
	prefix, suffix := parts[0], parts[1]
	suffix = strings.TrimPrefix(suffix, "/")

	// The prefix must match the beginning of the path
	if prefix != "" {
		if !strings.HasPrefix(path, prefix) {
			return false
		}
		path = path[len(prefix):]
		path = strings.TrimPrefix(path, "/")
	}

	if suffix == "" {
		// ** at end matches everything
		return true
	}

	// Try matching the suffix against every suffix of path
	segments := strings.Split(path, "/")
	for i := range segments {
		candidate := strings.Join(segments[i:], "/")
		if ok, _ := filepath.Match(suffix, candidate); ok {
			return true
		}
		if matchDoublestar(suffix, candidate) {
			return true
		}
	}
	return false
}
