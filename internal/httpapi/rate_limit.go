package httpapi

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type authAttempt struct {
	failures int
	resetAt  time.Time
}

type authLimiter struct {
	mu       sync.Mutex
	attempts map[string]authAttempt
	now      func() time.Time
	limit    int
	window   time.Duration
}

func newAuthLimiter() *authLimiter {
	return &authLimiter{
		attempts: map[string]authAttempt{},
		now:      time.Now,
		limit:    5,
		window:   5 * time.Minute,
	}
}

func (limiter *authLimiter) allow(key string) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	attempt, exists := limiter.attempts[key]
	if !exists || !attempt.resetAt.After(now) {
		delete(limiter.attempts, key)
		return true, 0
	}
	if attempt.failures < limiter.limit {
		return true, 0
	}
	return false, attempt.resetAt.Sub(now)
}

func (limiter *authLimiter) failure(key string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := limiter.now()
	attempt := limiter.attempts[key]
	if !attempt.resetAt.After(now) {
		attempt = authAttempt{resetAt: now.Add(limiter.window)}
	}
	attempt.failures++
	limiter.attempts[key] = attempt
}

func (limiter *authLimiter) success(key string) {
	limiter.mu.Lock()
	delete(limiter.attempts, key)
	limiter.mu.Unlock()
}

func authClientKey(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return request.RemoteAddr
}

func enforceAuthRateLimit(writer http.ResponseWriter, request *http.Request, limiter *authLimiter) (string, bool) {
	key := authClientKey(request)
	allowed, retryAfter := limiter.allow(key)
	if allowed {
		return key, true
	}
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(writer, http.StatusTooManyRequests, "auth_rate_limited")
	return key, false
}
