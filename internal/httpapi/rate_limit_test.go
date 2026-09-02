package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthLimiterBlocksAfterFailures(t *testing.T) {
	t.Parallel()
	now := time.Now()
	limiter := newAuthLimiter()
	limiter.now = func() time.Time { return now }
	key := "127.0.0.1"
	for index := 0; index < limiter.limit; index++ {
		if allowed, _ := limiter.allow(key); !allowed {
			t.Fatalf("attempt %d blocked too early", index)
		}
		limiter.failure(key)
	}
	if allowed, _ := limiter.allow(key); allowed {
		t.Fatal("rate limiter did not block")
	}
	now = now.Add(limiter.window + time.Second)
	if allowed, _ := limiter.allow(key); !allowed {
		t.Fatal("rate limiter did not reset")
	}
}

func TestAuthClientKeyIgnoresForwardedHeader(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodPost, "http://example.test/api/v1/auth/login", nil)
	request.RemoteAddr = "192.0.2.5:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	if key := authClientKey(request); key != "192.0.2.5" {
		t.Fatalf("unexpected client key %q", key)
	}
}
