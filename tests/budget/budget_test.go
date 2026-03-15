package budget_test

import (
	"strings"
	"testing"

	"spettro/internal/budget"
)

func TestValidateUnderLimit(t *testing.T) {
	if err := budget.Validate(0, "short prompt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateOverLimit(t *testing.T) {
	// ~11k estimated tokens
	huge := strings.Repeat("a", 44_000)
	if err := budget.Validate(8_000, huge); err == nil {
		t.Fatal("expected token budget error")
	}
}

func TestValidateZeroIsUnlimited(t *testing.T) {
	huge := strings.Repeat("a", 1_000_000)
	if err := budget.Validate(0, huge); err != nil {
		t.Fatalf("expected no error with unlimited budget, got: %v", err)
	}
}

func TestValidateCustomLimit(t *testing.T) {
	// 5 chars → 2 estimated tokens; limit of 1 should reject
	if err := budget.Validate(1, "hello"); err == nil {
		t.Fatal("expected budget error with tiny limit")
	}
	// limit of 100 should pass
	if err := budget.Validate(100, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
