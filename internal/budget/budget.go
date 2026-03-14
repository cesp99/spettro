package budget

import "fmt"

const DefaultMax = 1_000_000

func EstimateTokens(parts ...string) int {
	totalChars := 0
	for _, p := range parts {
		totalChars += len([]rune(p))
	}

	if totalChars == 0 {
		return 0
	}

	return (totalChars / 4) + 1
}

// Validate returns an error if the combined token estimate of parts exceeds
// maxTokens. Pass 0 to use DefaultMax.
func Validate(maxTokens int, parts ...string) error {
	if maxTokens <= 0 {
		maxTokens = DefaultMax
	}
	estimated := EstimateTokens(parts...)
	if estimated >= maxTokens {
		return fmt.Errorf("token budget exceeded: estimated=%d max=%d", estimated, maxTokens-1)
	}
	return nil
}
