package domain

const (
	// DefaultModelMaxOutputTokens is the output budget used when a model is
	// added without a sane value. Catalogs sometimes report the context window
	// as the output budget; that is not a usable completion limit.
	DefaultModelMaxOutputTokens = int64(128_000)
	defaultAutoCompactPercent   = 80
	autoCompactTailReserve      = int64(16_000)
)

// DefaultMaxOutputTokens resolves the output budget for a model being added or
// edited. A plausible smaller value is kept; missing, equal-to-context, or
// larger-than-context values fall back to 128K (capped by the context window).
func DefaultMaxOutputTokens(contextWindow, provided int64) int64 {
	if provided > 0 && (contextWindow <= 0 || provided < contextWindow) {
		return provided
	}
	if contextWindow > 0 && contextWindow < DefaultModelMaxOutputTokens {
		return contextWindow
	}
	return DefaultModelMaxOutputTokens
}

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
