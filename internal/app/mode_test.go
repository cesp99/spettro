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

func TestShiftTabInputs(t *testing.T) {
	for _, in := range []string{"/next", "shift+tab", ":next", "\x1b[Z"} {
		if !IsModeSwitchInput(in) {
			t.Fatalf("expected shift-tab switch match for %q", in)
		}
	}
}
