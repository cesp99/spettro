package app

import "testing"

func TestModeNextCycle(t *testing.T) {
	got := ModePlanning.Next()
	if got != ModeCoding {
		t.Fatalf("expected coding, got %s", got)
	}

	got = got.Next()
	if got != ModeChat {
		t.Fatalf("expected chat, got %s", got)
	}

	got = got.Next()
	if got != ModePlanning {
		t.Fatalf("expected planning, got %s", got)
	}
}

func TestCtrlTabInputs(t *testing.T) {
	for _, in := range []string{"/next", "ctrl+tab", ":next", "\x1b[27;5;9~"} {
		if !IsCtrlTabInput(in) {
			t.Fatalf("expected ctrl-tab match for %q", in)
		}
	}
}
