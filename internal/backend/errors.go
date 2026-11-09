package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrContextWindowExceeded marks a provider rejection caused by the resumed
// session's context exceeding the model window. Unlike a generic backend
// failure, retrying the same provider session cannot recover: the accumulated
// transcript is already over the limit. Callers use this sentinel to rotate the
// session and retry once with a fresh context.
var ErrContextWindowExceeded = errors.New("context window exceeded")

// providerDetailLimit bounds the provider error text carried by an
// agent_error event and the Turn error. Provider payloads can embed whole
// response bodies; the actionable text is at the head, and an unbounded copy
// would flood the stream and the conversation row.
const providerDetailLimit = 4000

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

// providerErrorText is the single entry point for provider error text that
// reaches events or Turn errors: strip credentials first, then bound the
// length. Keeping this in one place makes it hard to accidentally add a new
// provider error path that leaks a token or dumps a whole payload.
func providerErrorText(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	for _, redaction := range credentialRedactions {
		message = redaction.pattern.ReplaceAllString(message, redaction.replacement)
	}
	return clipError(message, providerDetailLimit)
}

var credentialRedactions = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// URL userinfo: https://user:password@host/...
	{regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`), `${1}[redacted]@`},
	// Authorization / Proxy-Authorization with a standard scheme. Only the
	// credential is replaced so diagnostics on the same line (request_id,
	// status) survive.
	{regexp.MustCompile(`(?i)\b(proxy-authorization|authorization)\b(\s*:\s*)(bearer|basic|token|digest)\s+[^\s;,]+`), `${1}${2}${3} [redacted]`},
	// AWS SigV4 authorization carries several comma-separated credential
	// components; none of them are diagnostic, so redact the whole value.
	{regexp.MustCompile(`(?i)\b(proxy-authorization|authorization)\b(\s*:\s*)AWS4-HMAC-SHA256\b[^\r\n]*`), `${1}${2}[redacted]`},
	// Other Authorization values are bounded at a separator or quote so a
	// same-line request id/status is preserved.
	{regexp.MustCompile(`(?i)\b(proxy-authorization|authorization)\b(\s*:\s*)("[^"]*"|'[^']*'|[^;,\s"']+)`), `${1}${2}[redacted]`},
	// Cookie and Set-Cookie headers.
	{regexp.MustCompile(`(?i)\b(set-cookie|cookie)\b(\s*:\s*)[^\r\n]+`), `${1}${2}[redacted]`},
	// Bare bearer tokens that are not part of a header line.
	{regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{8,}`), `${1} [redacted]`},
	// Well-known token prefixes.
	{regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9._-]{8,}|sk-[A-Za-z0-9._-]{8,}|ghp_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,}|xox[abprs]-[A-Za-z0-9-]{8,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35})`), `[redacted]`},
	// key: value / key=value pairs. "tokens" (a count) does not match \btoken\b.
	{regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|auth[_-]?token|token|client[_-]?secret|secret[_-]?key|password|passwd)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,;&"}]{4,})`), `${1}${2}[redacted]`},
	// JSON string values for generic secret keys.
	{regexp.MustCompile(`(?i)"(authorization|cookie|token|access_token|refresh_token|api_key|apikey|password|secret)"\s*:\s*"[^"]*"`), `"${1}":"[redacted]"`},
}

// errorValueText renders an arbitrary JSON-decoded error value (string,
// object, array, number) as readable text. Objects and arrays keep their JSON
// form, which is what preserves provider codes and nested details that a
// plain string assertion would drop.
func errorValueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.RawMessage:
		return jsonValueText(typed)
	case error:
		return strings.TrimSpace(typed.Error())
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := errorValueText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return strings.TrimSpace(fmt.Sprint(typed))
		}
		return string(encoded)
	}
}

// jsonValueText renders a raw JSON value, treating null as absent.
func jsonValueText(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return trimmed
	}
	return errorValueText(value)
}

// compactJSONValue serializes a decoded JSON value back to compact JSON for a
// raw-payload fallback. It returns "" for absent or null values.
func compactJSONValue(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return trimmed
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return trimmed
	}
	return string(encoded)
}
