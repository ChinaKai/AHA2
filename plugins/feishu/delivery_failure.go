package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	deliveryCertaintyFailure = "confirmed_failure"
	deliveryCertaintyUnknown = "unknown"
	deliveryMaxAttempts      = 12
	// deliveryAmbiguousWindow bounds automatic retries after a request whose
	// outcome is unknown (for example a dropped connection). After this window
	// the provider's idempotency window has likely elapsed, so replaying blindly
	// could duplicate the message.
	deliveryAmbiguousWindow = 55 * time.Minute
)

// deliverySendFailure carries a stable, provider-neutral error code and the
// retry policy for one send attempt. Raw provider messages are never persisted
// by the core; only the stable code and request ID are recorded.
type deliverySendFailure struct {
	code               string
	providerCode       int
	status             int
	retryable          bool
	ambiguous          bool
	replyTargetInvalid bool
}

func (failure deliverySendFailure) Error() string { return failure.code }

// providerDeliveryFailure maps a Feishu business/HTTP result to a stable code.
// Feishu commonly answers HTTP 200 with a non-zero business code, so both the
// HTTP status and the response code must be inspected.
func providerDeliveryFailure(stage string, status, code int) deliverySendFailure {
	failure := deliverySendFailure{providerCode: code, status: status}
	switch code {
	case 230011:
		// The reply target was withdrawn or no longer exists. Replying again
		// can never succeed; the caller should fall back to a plain message.
		if stage == "reply" {
			failure.code = "feishu_reply_target_unavailable"
			failure.replyTargetInvalid = true
		} else {
			failure.code = "feishu_message_unavailable"
		}
	case 230012:
		failure.code = "feishu_message_owner_mismatch"
	case 230013, 230014:
		failure.code = "feishu_target_unavailable"
	case 230002:
		// The bot is not in the chat or the app is not available to the user.
		failure.code = "feishu_target_unavailable"
	case 230006:
		failure.code = "feishu_bot_disabled"
	case 230008:
		failure.code = "feishu_permission_denied"
	case 230020, 99991400:
		failure.code, failure.retryable = "feishu_rate_limited", true
	default:
		switch {
		case status == http.StatusTooManyRequests, status == http.StatusRequestTimeout:
			failure.code, failure.retryable = "feishu_rate_limited", true
		case status >= http.StatusInternalServerError:
			failure.code, failure.retryable = "feishu_provider_unavailable", true
		case code > 0:
			// Unknown business codes stay retryable for the bounded attempt
			// window; known permanent codes above opt out explicitly.
			failure.code, failure.retryable = fmt.Sprintf("feishu_%s_rejected_%d", stage, code), true
		case status >= http.StatusBadRequest:
			failure.code, failure.retryable = fmt.Sprintf("feishu_%s_http_%d", stage, status), true
		default:
			failure.code = "feishu_send_failed"
		}
	}
	return failure
}

func invalidDeliveryResponse(stage string) deliverySendFailure {
	return deliverySendFailure{code: "feishu_" + stage + "_invalid_response", retryable: true}
}

// deliveryResponseError keeps the legacy media stage code shape for native
// attachment sends while still classifying the failure for retry decisions.
func deliveryResponseError(item delivery, stage string, status, code int) error {
	failure := providerDeliveryFailure(stage, status, code)
	if stringValue(item.SemanticPayload, "kind") == "provider_media" && failure.providerCode > 0 && !failure.replyTargetInvalid {
		return mediaFailure{code: fmt.Sprintf("media_send_provider_%d", failure.providerCode)}
	}
	return failure
}

// deliveryRetryDecision returns the NACK certainty and whether the delivery
// must terminate as a dead letter. Permanent provider errors stop immediately;
// ambiguous transport errors keep retrying only inside the deduplication
// window; every delivery stops after deliveryMaxAttempts attempts.
func deliveryRetryDecision(item delivery, failure deliverySendFailure, now time.Time) (string, bool) {
	certainty := deliveryCertaintyFailure
	permanent := item.Attempts >= deliveryMaxAttempts || (!failure.retryable && !failure.ambiguous)
	if failure.ambiguous {
		certainty = deliveryCertaintyUnknown
		permanent = !item.FirstAttemptAt.IsZero() && now.Sub(item.FirstAttemptAt) >= deliveryAmbiguousWindow
	}
	return certainty, permanent
}

// classifyDeliveryError converts any send-path error into retry semantics.
// Unknown transport failures are retryable but ambiguous: Feishu may have
// accepted the request before the connection broke.
func classifyDeliveryError(err error) deliverySendFailure {
	var sendFailure deliverySendFailure
	if errors.As(err, &sendFailure) {
		return sendFailure
	}
	if errors.Is(err, errMediaSize) {
		return deliverySendFailure{code: errMediaSize.Error()}
	}
	if errors.Is(err, errMediaType) {
		return deliverySendFailure{code: errMediaType.Error()}
	}
	var mediaErr mediaFailure
	if errors.As(err, &mediaErr) {
		// Media transfer/upload failures were historically retried until the
		// attempt limit; keep that behavior instead of dead-lettering on the
		// first transient failure.
		return deliverySendFailure{code: mediaErr.code, retryable: true}
	}
	if isAmbiguous(err) {
		return deliverySendFailure{code: "feishu_send_ambiguous", retryable: true, ambiguous: true}
	}
	return deliverySendFailure{code: "feishu_send_failed", retryable: true}
}
