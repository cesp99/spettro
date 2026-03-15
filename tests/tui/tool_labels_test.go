package tui_test

import (
	"strings"
	"testing"

	"spettro/internal/tui"
)

func TestFormatToolLabel_GlobIncludesPattern(t *testing.T) {
	got := tui.FormatToolLabelForTesting("glob", `{"pattern":"internal/**/*.go"}`)
	if !strings.Contains(got, `internal/**/*.go`) {
		t.Fatalf("expected glob pattern in label, got: %q", got)
	}
}

func TestFormatToolLabel_GrepIncludesPattern(t *testing.T) {
	got := tui.FormatToolLabelForTesting("grep", `{"pattern":"TODO|FIXME"}`)
	if !strings.Contains(got, `TODO|FIXME`) {
		t.Fatalf("expected grep pattern in label, got: %q", got)
	}
}

func TestFormatRunningLabel_GlobAndGrepIncludePattern(t *testing.T) {
	globLabel := tui.FormatRunningLabelForTesting("glob", `{"pattern":"**/*.md"}`)
	grepLabel := tui.FormatRunningLabelForTesting("grep", `{"pattern":"approval"}`)

	if !strings.Contains(globLabel, `**/*.md`) {
		t.Fatalf("expected running glob label to include pattern, got: %q", globLabel)
	}
	if !strings.Contains(grepLabel, `approval`) {
		t.Fatalf("expected running grep label to include pattern, got: %q", grepLabel)
	}
}
