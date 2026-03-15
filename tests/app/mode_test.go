package app_test

import (
	"testing"

	"spettro/internal/app"
)

func TestModeNextCycle(t *testing.T) {
	got := app.ModePlanning.Next()
	if got != app.ModeCoding {
		t.Fatalf("expected coding, got %s", got)
	}

	got = got.Next()
	if got != app.ModeChat {
		t.Fatalf("expected chat, got %s", got)
	}

	got = got.Next()
	if got != app.ModePlanning {
		t.Fatalf("expected plan, got %s", got)
	}
}

func TestShiftTabInputs(t *testing.T) {
	for _, in := range []string{"/next", "shift+tab", ":next", "\x1b[Z"} {
		if !app.IsModeSwitchInput(in) {
			t.Fatalf("expected shift-tab switch match for %q", in)
		}
	}
}
