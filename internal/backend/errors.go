package backend

import "errors"

// ErrContextWindowExceeded marks a provider rejection caused by the resumed
// session's context exceeding the model window. Unlike a generic backend
// failure, retrying the same provider session cannot recover: the accumulated
// transcript is already over the limit. Callers use this sentinel to rotate the
// session and retry once with a fresh context.
var ErrContextWindowExceeded = errors.New("context window exceeded")
