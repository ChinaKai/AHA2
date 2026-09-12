package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestClientInjectsCredentialAndUsesProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Errorf("authorization not injected")
		}
		if r.Header.Get("X-Device-ID") != "device-a" {
			t.Errorf("device identity not injected")
		}
		if r.URL.Path != "/v1/sync/push" {
			t.Errorf("path=%s", r.URL.Path)
		}
		var request PushRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(PushResponse{AckedKeys: []string{request.Objects[0].IdempotencyKey}})
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, DeviceID: "device-a", Credential: func(context.Context) (string, error) { return "runtime-token", nil }}
	response, err := client.Push(context.Background(), PushRequest{Scope: "default", Objects: []domain.SyncObject{{Type: "note", ID: "1", IdempotencyKey: "key"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.AckedKeys) != 1 {
		t.Fatalf("response=%#v", response)
	}
}

func TestClientRetriesRetryableResponses(t *testing.T) {
	fixedNow := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		statuses   []int
		retryAfter []string
		wantDelays []time.Duration
	}{
		{name: "429 Retry-After seconds", statuses: []int{429, 200}, retryAfter: []string{"3"}, wantDelays: []time.Duration{3 * time.Second}},
		{name: "503 Retry-After date", statuses: []int{503, 200}, retryAfter: []string{fixedNow.Add(4 * time.Second).Format(http.TimeFormat)}, wantDelays: []time.Duration{4 * time.Second}},
		{name: "exponential fallback", statuses: []int{503, 429, 503, 200}, retryAfter: []string{"", "invalid", ""}, wantDelays: []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts int
			payload := PushRequest{Scope: "default", Objects: []domain.SyncObject{{Type: "note", ID: "1", IdempotencyKey: "key"}}}
			var firstBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := new(bytes.Buffer)
				_, _ = body.ReadFrom(r.Body)
				if attempts == 0 {
					firstBody = append([]byte(nil), body.Bytes()...)
				} else if !bytes.Equal(body.Bytes(), firstBody) {
					t.Errorf("retry body = %q, want %q", body.Bytes(), firstBody)
				}
				status := tt.statuses[attempts]
				if attempts < len(tt.retryAfter) && tt.retryAfter[attempts] != "" {
					w.Header().Set("Retry-After", tt.retryAfter[attempts])
				}
				attempts++
				w.WriteHeader(status)
				if status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(PushResponse{AckedKeys: []string{"key"}})
				}
			}))
			defer server.Close()
			var delays []time.Duration
			client := Client{BaseURL: server.URL, retryNow: func() time.Time { return fixedNow }, retryJitter: func(delay time.Duration) time.Duration { return delay }, retryWait: func(_ context.Context, delay time.Duration) error {
				delays = append(delays, delay)
				return nil
			}}
			response, err := client.Push(context.Background(), payload)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(delays, tt.wantDelays) {
				t.Fatalf("delays = %v, want %v", delays, tt.wantDelays)
			}
			if !reflect.DeepEqual(response.AckedKeys, []string{"key"}) {
				t.Fatalf("response = %#v", response)
			}
		})
	}
}

func TestClientRetryLimitAndNonRetryableStatus(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		wantAttempts int32
		wantWaits    int32
	}{
		{name: "retry limit", status: http.StatusServiceUnavailable, wantAttempts: 4, wantWaits: 3},
		{name: "non-retryable", status: http.StatusBadGateway, wantAttempts: 1, wantWaits: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				http.Error(w, "temporary failure", tt.status)
			}))
			defer server.Close()
			var waits atomic.Int32
			client := Client{BaseURL: server.URL, retryWait: func(context.Context, time.Duration) error { waits.Add(1); return nil }}
			_, err := client.Pull(context.Background(), "default", "0", "device", 100)
			if err == nil {
				t.Fatal("expected error")
			}
			if attempts.Load() != tt.wantAttempts || waits.Load() != tt.wantWaits {
				t.Fatalf("attempts=%d waits=%d, want attempts=%d waits=%d", attempts.Load(), waits.Load(), tt.wantAttempts, tt.wantWaits)
			}
		})
	}
}

func TestClientRetryWaitObeysContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&Client{}).waitForRetry(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestClientRegistrationUsesDeviceName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["device_name"] != "My Laptop" || request["device_id"] != "" {
			t.Fatalf("request=%#v", request)
		}
		_ = json.NewEncoder(w).Encode(RegisterResponse{DeviceID: "dev_center", DeviceName: "My Laptop", Token: "returned-once"})
	}))
	defer server.Close()
	response, err := (&Client{BaseURL: server.URL}).Register(context.Background(), "My Laptop", "registration-code")
	if err != nil {
		t.Fatal(err)
	}
	if response.DeviceID != "dev_center" || response.DeviceName != "My Laptop" || response.Token == "" {
		t.Fatalf("response=%#v", response)
	}
}
