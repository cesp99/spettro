package budget

import (
	"strings"
	"testing"
)

func TestValidateUnderLimit(t *testing.T) {
	if err := Validate("short prompt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOverLimit(t *testing.T) {
	// ~11k estimated tokens
	huge := strings.Repeat("a", 44_000)
	if err := Validate(huge); err == nil {
		t.Fatal("expected token budget error")
	}
}
