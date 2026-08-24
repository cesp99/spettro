package workflow

import (
	"fmt"
	"strings"

	"github.com/dop251/goja"
)

// Meta is the header every workflow script must declare:
//
//	export const meta = { name, description, phases: [{title, detail}] }
//
// It is read before a single agent runs, so hosts can show what a script is
// about to do (and ask for approval) without executing it. That is only sound
// if the header cannot itself run code, hence the pure-literal rule enforced
// by ParseMeta.
type Meta struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	WhenToUse   string      `json:"whenToUse,omitempty"`
	Phases      []PhaseMeta `json:"phases,omitempty"`
	Model       string      `json:"model,omitempty"`
}

// PhaseMeta describes one declared phase of a workflow.
type PhaseMeta struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Model  string `json:"model,omitempty"`
}

const metaDecl = "export const meta"

// stripMetaExport rewrites the `export const meta = …` header into a plain
// declaration. Scripts are run as a function body, not as an ES module, so the
// export keyword would be a syntax error; nothing else about the declaration
// changes.
func stripMetaExport(script string) string {
	idx := indexMetaDecl(script)
	if idx < 0 {
		return script
	}
	return script[:idx] + "const meta" + script[idx+len(metaDecl):]
}

// indexMetaDecl finds the meta declaration at the start of a line (ignoring
// indentation), so the token cannot be matched inside a string or comment that
// happens to quote it.
func indexMetaDecl(script string) int {
	for offset := 0; ; {
		i := strings.Index(script[offset:], metaDecl)
		if i < 0 {
			return -1
		}
		abs := offset + i
		if lineStartIsBlank(script, abs) {
			return abs
		}
		offset = abs + len(metaDecl)
	}
}

func lineStartIsBlank(s string, idx int) bool {
	for i := idx - 1; i >= 0; i-- {
		switch s[i] {
		case '\n':
			return true
		case ' ', '\t', '\r':
			continue
		default:
			return false
		}
	}
	return true
}

// ParseMeta extracts and evaluates the meta header without running the script.
//
// The object literal is evaluated in a throwaway runtime stripped of every
// global a script could reach through, so a header that calls a function or
// reads a variable fails here rather than silently doing work before the user
// has seen what the workflow is.
func ParseMeta(script string) (Meta, error) {
	decl := indexMetaDecl(script)
	if decl < 0 {
		return Meta{}, fmt.Errorf("script must begin with `export const meta = {...}` declaring name, description and phases")
	}
	eq := strings.IndexByte(script[decl:], '=')
	if eq < 0 {
		return Meta{}, fmt.Errorf("malformed meta declaration: no `=` after `export const meta`")
	}
	body := script[decl+eq+1:]
	literal, err := objectLiteral(body)
	if err != nil {
		return Meta{}, fmt.Errorf("malformed meta declaration: %w", err)
	}

	vm := goja.New()
	// A pure literal needs no globals at all. Emptying the global object is
	// what turns "must be a literal" from documentation into a rule: any
	// identifier or call in the header now throws a ReferenceError.
	for _, key := range vm.GlobalObject().Keys() {
		_ = vm.GlobalObject().Delete(key)
	}
	val, err := vm.RunString("(" + literal + ")")
	if err != nil {
		return Meta{}, fmt.Errorf("meta must be a pure object literal (no variables, calls, or spreads): %w", err)
	}
	obj, ok := val.Export().(map[string]any)
	if !ok {
		return Meta{}, fmt.Errorf("meta must be an object literal")
	}
	m := Meta{
		Name:        stringField(obj, "name"),
		Description: stringField(obj, "description"),
		WhenToUse:   stringField(obj, "whenToUse"),
		Model:       stringField(obj, "model"),
	}
	if raw, ok := obj["phases"].([]any); ok {
		for _, entry := range raw {
			p, ok := entry.(map[string]any)
			if !ok {
				return Meta{}, fmt.Errorf("meta.phases entries must be objects with a title")
			}
			m.Phases = append(m.Phases, PhaseMeta{
				Title:  stringField(p, "title"),
				Detail: stringField(p, "detail"),
				Model:  stringField(p, "model"),
			})
		}
	}
	if m.Name == "" {
		return Meta{}, fmt.Errorf("meta.name is required")
	}
	if m.Description == "" {
		return Meta{}, fmt.Errorf("meta.description is required")
	}
	for i, p := range m.Phases {
		if p.Title == "" {
			return Meta{}, fmt.Errorf("meta.phases[%d].title is required", i)
		}
	}
	return m, nil
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// objectLiteral returns the `{...}` starting the given text, brace-matched
// while skipping strings, template literals and comments so a `}` inside a
// description does not truncate the header.
func objectLiteral(s string) (string, error) {
	start := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
			start = i
		}
		break
	}
	if start < 0 {
		return "", fmt.Errorf("expected an object literal after `=`")
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch c := s[i]; c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		case '\'', '"', '`':
			end, err := skipString(s, i, c)
			if err != nil {
				return "", err
			}
			i = end
		case '/':
			if i+1 < len(s) && s[i+1] == '/' {
				if nl := strings.IndexByte(s[i:], '\n'); nl < 0 {
					return "", fmt.Errorf("unterminated meta object")
				} else {
					i += nl
				}
			} else if i+1 < len(s) && s[i+1] == '*' {
				end := strings.Index(s[i+2:], "*/")
				if end < 0 {
					return "", fmt.Errorf("unterminated comment in meta object")
				}
				i += 2 + end + 1
			}
		}
	}
	return "", fmt.Errorf("unterminated meta object")
}

// skipString returns the index of the closing quote for the string starting at
// open (which holds the quote character).
func skipString(s string, open int, quote byte) (int, error) {
	for i := open + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case quote:
			return i, nil
		}
	}
	return 0, fmt.Errorf("unterminated string in meta object")
}
