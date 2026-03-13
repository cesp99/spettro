package tui

import (
	"strings"
	"testing"
)

func TestFormatToolLabel_GlobIncludesPattern(t *testing.T) {
	got := formatToolLabel("glob", `{"pattern":"internal/**/*.go"}`)
	if !strings.Contains(got, `internal/**/*.go`) {
		t.Fatalf("expected glob pattern in label, got: %q", got)
	}
}

func TestFormatToolLabel_GrepIncludesPattern(t *testing.T) {
	got := formatToolLabel("grep", `{"pattern":"TODO|FIXME"}`)
	if !strings.Contains(got, `TODO|FIXME`) {
		t.Fatalf("expected grep pattern in label, got: %q", got)
	}
}

func TestFormatRunningLabel_GlobAndGrepIncludePattern(t *testing.T) {
	globLabel := formatRunningLabel("glob", `{"pattern":"**/*.md"}`)
	grepLabel := formatRunningLabel("grep", `{"pattern":"approval"}`)

	if !strings.Contains(globLabel, `**/*.md`) {
		t.Fatalf("expected running glob label to include pattern, got: %q", globLabel)
	}
	if !strings.Contains(grepLabel, `approval`) {
		t.Fatalf("expected running grep label to include pattern, got: %q", grepLabel)
	}
}
