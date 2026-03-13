package budget

import (
	"strings"
	"testing"
)

func TestValidateUnderLimit(t *testing.T) {
	if err := Validate(0, "short prompt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOverLimit(t *testing.T) {
	// ~11k estimated tokens
	huge := strings.Repeat("a", 44_000)
	if err := Validate(0, huge); err == nil {
		t.Fatal("expected token budget error")
	}
}

func TestValidateCustomLimit(t *testing.T) {
	// 5 chars → 2 estimated tokens; limit of 1 should reject it
	if err := Validate(1, "hello"); err == nil {
		t.Fatal("expected budget error with tiny limit")
	}
	// limit of 100 should pass
	if err := Validate(100, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
