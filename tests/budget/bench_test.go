package budget_test

import (
	"strings"
	"testing"

	"spettro/internal/budget"
)

func BenchmarkEstimateTokens(b *testing.B) {
	payload := strings.Repeat("token-data-", 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = budget.EstimateTokens(payload)
	}
}
