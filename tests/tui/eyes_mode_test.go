package tui_test

import (
	"testing"

	"spettro/internal/tui"
)

func TestIsPlanningEyeMode(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{mode: "plan", want: true},
		{mode: "ask", want: true},
		{mode: "coding", want: false},
		{mode: "code", want: false},
		{mode: "planning", want: false},
		{mode: "explore", want: false},
		{mode: "research", want: false},
	}

	for _, tt := range tests {
		got := tui.IsPlanningEyeModeForTesting(tt.mode)
		if got != tt.want {
			t.Fatalf("mode %q: got %v, want %v", tt.mode, got, tt.want)
		}
	}
}
