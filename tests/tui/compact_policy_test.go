package tui_test

import (
	"testing"

	"spettro/internal/compact"
)

func TestCompactPolicyThresholds(t *testing.T) {
	eval := compact.Evaluate(128000, compact.Config{AutoEnabled: true, AutoThresholdPct: 85, MaxFailures: 3}, compact.State{TokensUsed: 95000})
	if eval.EffectiveWindow <= 0 {
		t.Fatal("expected positive effective window")
	}
	if eval.WarningThreshold <= 0 || eval.ErrorThreshold <= 0 || eval.AutoCompactThreshold <= 0 || eval.BlockingLimit <= 0 {
		t.Fatalf("expected positive thresholds: %+v", eval)
	}
	if !eval.ShouldAutoCompact {
		t.Fatalf("expected auto compact to trigger: %+v", eval)
	}
}

func TestCompactPolicyCircuitBreaker(t *testing.T) {
	eval := compact.Evaluate(128000, compact.Config{AutoEnabled: true, AutoThresholdPct: 85, MaxFailures: 3}, compact.State{TokensUsed: 120000, ConsecutiveFailures: 3})
	if eval.ShouldAutoCompact {
		t.Fatalf("expected circuit breaker to disable auto compact: %+v", eval)
	}
	if eval.AutoDisabledReason == "" {
		t.Fatalf("expected disabled reason for circuit breaker: %+v", eval)
	}
}
