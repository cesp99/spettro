package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// schemaInstruction is appended to an agent prompt when the script asked for
// structured output. The sub-agent has no special "return JSON" channel, so
// the contract is carried in the prompt and enforced on the way back by
// parseStructured — a mismatch is fed to the agent as a correction rather than
// crashing the script, which is what makes schema calls worth using.
func schemaInstruction(schema json.RawMessage) string {
	return "\n\nSTRUCTURED OUTPUT REQUIRED.\nYour final message must be exactly one JSON value matching this JSON Schema, and nothing else — no prose before or after, no markdown fence:\n" +
		string(compactJSON(schema)) +
		"\nIf you have nothing to report, still return a valid value (e.g. an object with empty arrays)."
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return json.RawMessage(buf.Bytes())
}

// parseStructured pulls the JSON value out of an agent's final message and
// checks it against the parts of the schema that catch real mistakes: the
// top-level type and its required properties. Full JSON Schema validation is
// deliberately not attempted — the goal is to catch "the model wrote prose" and
// "the model omitted a field", both of which a retry fixes.
func parseStructured(output string, schema json.RawMessage) (any, error) {
	raw := extractJSON(output)
	if raw == "" {
		return nil, fmt.Errorf("no JSON value found in the response")
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("response is not valid JSON: %v", err)
	}
	if err := checkSchema(value, schema); err != nil {
		return nil, err
	}
	return value, nil
}

func checkSchema(value any, schema json.RawMessage) error {
	var s struct {
		Type     string   `json:"type"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return nil
	}
	switch s.Type {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("expected a JSON object at the top level, got %s", jsonKind(value))
		}
		var missing []string
		for _, key := range s.Required {
			if _, ok := obj[key]; !ok {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("missing required propert%s: %s", plural(len(missing), "y", "ies"), strings.Join(missing, ", "))
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("expected a JSON array at the top level, got %s", jsonKind(value))
		}
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func jsonKind(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case string:
		return "a string"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case nil:
		return "null"
	}
	return "an unknown value"
}

// extractJSON finds the JSON value in an agent response: the whole message
// when it is already clean, the contents of a fenced block when the model
// wrapped it, or the outermost brace/bracket span when it added prose anyway.
func extractJSON(output string) string {
	s := strings.TrimSpace(output)
	if s == "" {
		return ""
	}
	if json.Valid([]byte(s)) {
		return s
	}
	if fenced := fencedBlock(s); fenced != "" && json.Valid([]byte(fenced)) {
		return fenced
	}
	if span := outermostSpan(s); span != "" && json.Valid([]byte(span)) {
		return span
	}
	return ""
}

func fencedBlock(s string) string {
	start := strings.Index(s, "```")
	if start < 0 {
		return ""
	}
	rest := s[start+3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	}
	end := strings.Index(rest, "```")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// outermostSpan returns the text between the first opening brace/bracket and
// the last matching closing one, which recovers the payload when a model wraps
// valid JSON in an apology.
func outermostSpan(s string) string {
	openIdx, closeCh := -1, byte(0)
	for i := 0; i < len(s); i++ {
		if s[i] == '{' {
			openIdx, closeCh = i, '}'
			break
		}
		if s[i] == '[' {
			openIdx, closeCh = i, ']'
			break
		}
	}
	if openIdx < 0 {
		return ""
	}
	closeIdx := strings.LastIndexByte(s, closeCh)
	if closeIdx <= openIdx {
		return ""
	}
	return s[openIdx : closeIdx+1]
}
