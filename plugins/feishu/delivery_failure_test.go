package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func deliveryResponse(status int, requestID, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{requestID},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func TestReplyTargetWithdrawnFallsBackToCreate(t *testing.T) {
	t.Parallel()
	replyCalls, createCalls := 0, 0
	client := lark.NewClient("reply-fallback", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/auth/"):
			return mediaTestResponse(200, `{"code":0,"tenant_access_token":"token","expire":3600}`), nil
		case strings.HasSuffix(request.URL.Path, "/reply"):
			replyCalls++
			return deliveryResponse(400, "req-reply", `{"code":230011,"msg":"message withdrawn"}`), nil
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/messages"):
			createCalls++
			return deliveryResponse(200, "req-create", `{"code":0,"data":{"message_id":"om_fallback"}}`), nil
		default:
			t.Errorf("unexpected provider request %s %s", request.Method, request.URL.Path)
			return mediaTestResponse(404, `{"code":404}`), nil
		}
	}}))
	messageID, requestID, err := sendRenderedDelivery(context.Background(), client, delivery{
		IdempotencyKey: "fallback-key",
		Target:         map[string]string{"chat_id": "oc_chat", "reply_message_id": "om_withdrawn"},
	}, "text", `{"text":"hello"}`)
	if err != nil || messageID != "om_fallback" || requestID != "req-create" {
		t.Fatalf("message=%q request=%q err=%v", messageID, requestID, err)
	}
	if replyCalls != 1 || createCalls != 1 {
		t.Fatalf("reply=%d create=%d", replyCalls, createCalls)
	}
}

func TestReplyRejectionStaysPermanentWithoutFallback(t *testing.T) {
	t.Parallel()
	createCalls := 0
	client := lark.NewClient("reply-rejected", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/auth/"):
			return mediaTestResponse(200, `{"code":0,"tenant_access_token":"token","expire":3600}`), nil
		case strings.HasSuffix(request.URL.Path, "/reply"):
			return deliveryResponse(400, "req-reply", `{"code":230002,"msg":"bot not in chat"}`), nil
		case strings.HasSuffix(request.URL.Path, "/messages"):
			createCalls++
			return deliveryResponse(200, "req-create", `{"code":0,"data":{"message_id":"om_unexpected"}}`), nil
		default:
			return mediaTestResponse(404, `{"code":404}`), nil
		}
	}}))
	_, requestID, err := sendRenderedDelivery(context.Background(), client, delivery{
		IdempotencyKey: "rejected-key",
		Target:         map[string]string{"chat_id": "oc_chat", "reply_message_id": "om_parent"},
	}, "text", `{"text":"hello"}`)
	if err == nil || requestID != "req-reply" {
		t.Fatalf("request=%q err=%v", requestID, err)
	}
	var failure deliverySendFailure
	if !errors.As(err, &failure) || failure.code != "feishu_target_unavailable" || failure.retryable || failure.replyTargetInvalid {
		t.Fatalf("failure=%#v err=%v", failure, err)
	}
	if createCalls != 0 {
		t.Fatalf("create called %d times after permanent reply rejection", createCalls)
	}
}

func TestDeliveryFailureClassification(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name, stage, code string
		status, provider  int
		retryable         bool
		replyTarget       bool
	}{
		{"withdrawn reply", "reply", "feishu_reply_target_unavailable", 400, 230011, false, true},
		{"withdrawn patch", "patch", "feishu_message_unavailable", 400, 230011, false, false},
		{"bot not in chat", "create", "feishu_target_unavailable", 400, 230002, false, false},
		{"bot disabled", "create", "feishu_bot_disabled", 400, 230006, false, false},
		{"permission denied", "create", "feishu_permission_denied", 403, 230008, false, false},
		{"group rate limit", "create", "feishu_rate_limited", 200, 230020, true, false},
		{"http rate limit", "create", "feishu_rate_limited", 429, 0, true, false},
		{"provider outage", "create", "feishu_provider_unavailable", 503, 0, true, false},
		{"unknown business code", "create", "feishu_create_rejected_230099", 200, 230099, true, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			failure := providerDeliveryFailure(scenario.stage, scenario.status, scenario.provider)
			if failure.code != scenario.code || failure.retryable != scenario.retryable || failure.replyTargetInvalid != scenario.replyTarget {
				t.Fatalf("failure=%#v", failure)
			}
		})
	}
	if failure := classifyDeliveryError(errMediaSize); failure.code != "resource_size_invalid" || failure.retryable {
		t.Fatalf("size failure=%#v", failure)
	}
	if failure := classifyDeliveryError(errors.New("connection reset by peer")); failure.code != "feishu_send_ambiguous" || !failure.ambiguous || !failure.retryable {
		t.Fatalf("ambiguous failure=%#v", failure)
	}
}

func TestDeliveryRetryDecision(t *testing.T) {
	t.Parallel()
	now := time.Now()
	permanent := deliverySendFailure{code: "feishu_target_unavailable"}
	if certainty, dead := deliveryRetryDecision(delivery{Attempts: 1}, permanent, now); certainty != deliveryCertaintyFailure || !dead {
		t.Fatalf("permanent failure certainty=%s dead=%v", certainty, dead)
	}
	retryable := deliverySendFailure{code: "feishu_rate_limited", retryable: true}
	if certainty, dead := deliveryRetryDecision(delivery{Attempts: 1}, retryable, now); certainty != deliveryCertaintyFailure || dead {
		t.Fatalf("retryable failure certainty=%s dead=%v", certainty, dead)
	}
	if certainty, dead := deliveryRetryDecision(delivery{Attempts: deliveryMaxAttempts}, retryable, now); certainty != deliveryCertaintyFailure || !dead {
		t.Fatalf("attempt limit certainty=%s dead=%v", certainty, dead)
	}
	ambiguous := deliverySendFailure{code: "feishu_send_ambiguous", retryable: true, ambiguous: true}
	if certainty, dead := deliveryRetryDecision(delivery{Attempts: 1, FirstAttemptAt: now.Add(-time.Minute)}, ambiguous, now); certainty != deliveryCertaintyUnknown || dead {
		t.Fatalf("ambiguous retry certainty=%s dead=%v", certainty, dead)
	}
	if certainty, dead := deliveryRetryDecision(delivery{Attempts: 1, FirstAttemptAt: now.Add(-deliveryAmbiguousWindow - time.Minute)}, ambiguous, now); certainty != deliveryCertaintyUnknown || !dead {
		t.Fatalf("ambiguous window certainty=%s dead=%v", certainty, dead)
	}
}

func TestMediaSendDisconnectUsesAmbiguousOutcomeWindow(t *testing.T) {
	t.Parallel()
	nacks := []map[string]any{}
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/deliveries/delivery/media"):
			writer.Write([]byte(`{"ok":true}`))
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/deliveries/delivery/attachment"):
			writer.Header().Set("Content-Type", "image/png")
			writer.Write([]byte("\x89PNG\r\n\x1a\nfixture"))
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/nack"):
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			nacks = append(nacks, payload)
			writer.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected runtime request %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer runtimeServer.Close()
	client := lark.NewClient("media-disconnect", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/auth/"):
			return mediaTestResponse(200, `{"code":0,"tenant_access_token":"token","expire":3600}`), nil
		case strings.HasSuffix(request.URL.Path, "/images"):
			return mediaTestResponse(200, `{"code":0,"data":{"image_key":"img-key"}}`), nil
		case strings.HasSuffix(request.URL.Path, "/messages"):
			return nil, errors.New("connection reset by peer")
		default:
			t.Errorf("unexpected provider request %s", request.URL.Path)
			return mediaTestResponse(404, `{"code":404}`), nil
		}
	}}))
	runtime := &runtimeClient{baseURL: runtimeServer.URL, http: runtimeServer.Client()}
	item := delivery{
		ID: "delivery", LeaseID: "lease", Attempts: 1, IdempotencyKey: "media-disconnect-key",
		FirstAttemptAt:  time.Now().Add(-time.Minute),
		Target:          map[string]string{"chat_id": "oc_chat"},
		SemanticPayload: map[string]any{"kind": "attachment", "name": "a.png"},
	}
	runtime.deliver(context.Background(), client, item)
	if len(nacks) != 1 {
		t.Fatalf("nacks=%#v", nacks)
	}
	if nacks[0]["outcome_certainty"] != deliveryCertaintyUnknown || nacks[0]["permanent"] != false || nacks[0]["error_code"] != "media_send_failed" {
		t.Fatalf("fresh disconnect nack=%#v", nacks[0])
	}

	item.FirstAttemptAt = time.Now().Add(-deliveryAmbiguousWindow - time.Minute)
	runtime.deliver(context.Background(), client, item)
	if len(nacks) != 2 {
		t.Fatalf("nacks=%#v", nacks)
	}
	if nacks[1]["outcome_certainty"] != deliveryCertaintyUnknown || nacks[1]["permanent"] != true || nacks[1]["error_code"] != "media_send_failed" {
		t.Fatalf("expired uncertain window nack=%#v", nacks[1])
	}
}

func TestDeliveryNackRecordsStableCodeAndProviderRequestID(t *testing.T) {
	t.Parallel()
	nack := map[string]any{}
	runtimeServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasSuffix(request.URL.Path, "/nack") {
			t.Errorf("unexpected runtime request %s %s", request.Method, request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&nack); err != nil {
			t.Error(err)
		}
		writer.Write([]byte(`{"ok":true}`))
	}))
	defer runtimeServer.Close()
	client := lark.NewClient("nack-diagnostics", "test-secret", lark.WithHttpClient(mockHTTPClient{do: func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/auth/") {
			return mediaTestResponse(200, `{"code":0,"tenant_access_token":"token","expire":3600}`), nil
		}
		return deliveryResponse(400, "req-provider", `{"code":230002,"msg":"bot not in chat"}`), nil
	}}))
	runtime := &runtimeClient{baseURL: runtimeServer.URL, http: runtimeServer.Client()}
	runtime.deliver(context.Background(), client, delivery{
		ID: "delivery", LeaseID: "lease", Attempts: 1, IdempotencyKey: "nack-key",
		Target: map[string]string{"chat_id": "oc_chat"}, SemanticPayload: map[string]any{"kind": "text", "text": "hello"},
	})
	if nack["error_code"] != "feishu_target_unavailable" || nack["provider_request_id"] != "req-provider" ||
		nack["outcome_certainty"] != deliveryCertaintyFailure || nack["permanent"] != true {
		t.Fatalf("nack=%#v", nack)
	}
}
