package backend

import (
	"errors"
	"strings"
)

// ErrContextWindowExceeded marks a provider rejection caused by the resumed
// session's context exceeding the model window. Unlike a generic backend
// failure, retrying the same provider session cannot recover: the accumulated
// transcript is already over the limit. Callers use this sentinel to rotate the
// session and retry once with a fresh context.
var ErrContextWindowExceeded = errors.New("context window exceeded")

// clipError keeps the head of a provider error. Provider messages put the
// actionable text first and often append routing/fallback noise; keeping the
// head preserves the original cause while bounding an exceptionally long line.
func clipError(message string, limit int) string {
	message = strings.TrimSpace(message)
	if limit <= 0 {
		return message
	}
	runes := []rune(message)
	if len(runes) <= limit {
		return message
	}
	return string(runes[:limit]) + "…"
}
