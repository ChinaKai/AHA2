package domain

const (
	defaultAutoCompactPercent = 80
	autoCompactTailReserve    = int64(16_000)
)

// DefaultAutoCompactWindow derives the CLI auto-compact threshold for a model.
// The owner-visible default is 80% of the context window; when the model
// declares a large output budget, the threshold is lowered so the completion
// reservation and a small tail margin still fit inside the window.
//
// A zero result means "leave it to the CLI": either the context window is
// unknown, or the safe limit would be non-positive.
func DefaultAutoCompactWindow(contextWindow, maxOutputTokens int64) int64 {
	if contextWindow <= 0 {
		return 0
	}
	window := contextWindow * defaultAutoCompactPercent / 100
	if maxOutputTokens > 0 {
		safe := contextWindow - maxOutputTokens - autoCompactTailReserve
		if safe > 0 && safe < window {
			window = safe
		}
	}
	return window
}
