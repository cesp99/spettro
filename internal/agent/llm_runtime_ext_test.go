package agent

import (
	"strings"
	"testing"
)

func TestExtractDuckDuckGoResults(t *testing.T) {
	html := `
<div class="results">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpost">Example Post</a>
  <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2Fdoc">Go Docs</a>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fpost">Duplicate Example</a>
</div>`
	rows := extractDuckDuckGoResults(html, 10)
	if len(rows) != 2 {
		t.Fatalf("expected 2 unique rows, got %d: %#v", len(rows), rows)
	}
	if !strings.Contains(rows[0], "Example Post") || !strings.Contains(rows[0], "https://example.com/post") {
		t.Fatalf("unexpected first row: %q", rows[0])
	}
	if !strings.Contains(rows[1], "Go Docs") || !strings.Contains(rows[1], "https://golang.org/doc") {
		t.Fatalf("unexpected second row: %q", rows[1])
	}
}

func TestResolveDuckDuckGoResultURL(t *testing.T) {
	got := resolveDuckDuckGoResultURL("//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Fpath")
	if got != "https://example.org/path" {
		t.Fatalf("unexpected resolved url: %q", got)
	}
	if resolveDuckDuckGoResultURL("https://duckduckgo.com/l/?x=1") != "" {
		t.Fatalf("expected empty for missing uddg")
	}
	if resolveDuckDuckGoResultURL("javascript:alert(1)") != "" {
		t.Fatalf("expected empty for invalid non-http link")
	}
}
