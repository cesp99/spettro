package budget

import "fmt"

const MaxRequestTokens = 10_000

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

func Validate(parts ...string) error {
	estimated := EstimateTokens(parts...)
	if estimated >= MaxRequestTokens {
		return fmt.Errorf("token budget exceeded: estimated=%d max=%d", estimated, MaxRequestTokens-1)
	}
	return nil
}
