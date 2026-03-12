package budget

import (
	"strings"
	"testing"
)

func BenchmarkEstimateTokens(b *testing.B) {
	payload := strings.Repeat("token-data-", 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EstimateTokens(payload)
	}
}
