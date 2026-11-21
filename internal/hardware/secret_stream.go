package hardware

import (
	"errors"
	"strings"
)

const secretMask = "••••"

// secretStream holds only a possible credential prefix. Nothing in that
// pending prefix may be recorded or broadcast before the next read resolves
// it. KMP fallback makes adversarial overlapping input linear in stream size.
type secretStream struct {
	secret   []byte
	fallback []int
	pending  []byte
	mask     []byte
	// forced counts leading input bytes whose preceding context is unknown
	// (a retained-history page boundary). They may continue a credential that
	// started before the page, so they are released only as a mask.
	forced int
}

func newSecretStream(secret string) *secretStream {
	filter := &secretStream{secret: []byte(secret), fallback: make([]int, len(secret)), mask: []byte(secretMaskFor(secret))}
	for i, j := 1, 0; i < len(filter.secret); i++ {
		for j > 0 && filter.secret[i] != filter.secret[j] {
			j = filter.fallback[j-1]
		}
		if filter.secret[i] == filter.secret[j] {
			j++
		}
		filter.fallback[i] = j
	}
	return filter
}

func (filter *secretStream) Feed(input []byte) []byte {
	if len(filter.secret) == 0 {
		return append([]byte(nil), input...)
	}
	output := make([]byte, 0, len(input))
	forcedMasked := false
	// Every input byte leaves the filter exactly once, in input order: either
	// released here or consumed by a full match. Forced bytes are a leading
	// run of that order, so the counter stays aligned with the bytes.
	release := func(values []byte) {
		for _, value := range values {
			if filter.forced > 0 {
				filter.forced--
				if !forcedMasked {
					output = append(output, filter.mask...)
					forcedMasked = true
				}
				continue
			}
			output = append(output, value)
		}
	}
	for _, value := range input {
		for len(filter.pending) > 0 && filter.secret[len(filter.pending)] != value {
			keep := filter.fallback[len(filter.pending)-1]
			emit := len(filter.pending) - keep
			release(filter.pending[:emit])
			filter.pending = append(filter.pending[:0], filter.pending[emit:]...)
		}
		if filter.secret[len(filter.pending)] == value {
			filter.pending = append(filter.pending, value)
			if len(filter.pending) == len(filter.secret) {
				filter.forced -= min(filter.forced, len(filter.pending))
				output = append(output, filter.mask...)
				filter.pending = filter.pending[:0]
			}
		} else {
			release([]byte{value})
		}
	}
	return output
}

// possibleContinuation returns how many leading bytes of a stream fragment
// could finish a credential occurrence that began before the fragment: the
// longest head that equals secret[k:] for some k>0 and either completes the
// credential or runs to the end of the fragment (total bytes). Masking them
// can hide a few ordinary bytes at a page start; it never reveals a tail.
func possibleContinuation(secret []byte, head []byte, total int) int {
	best := 0
	for start := 1; start < len(secret); start++ {
		rest := secret[start:]
		count := 0
		for count < len(rest) && count < len(head) && rest[count] == head[count] {
			count++
		}
		if count > best && (count == len(rest) || count == total) {
			best = count
		}
	}
	return best
}

func (filter *secretStream) Finish() []byte {
	if len(filter.pending) == 0 {
		return nil
	}
	// Even a final partial echo is not released on EOF/disconnect.
	clear(filter.pending)
	filter.pending = nil
	return append([]byte(nil), filter.mask...)
}

func secretMaskFor(secret string) string {
	// The marker cannot itself contain a credential, even for short or unusual
	// configured secrets. A rune absent from the secret also separates output
	// fragments so replacing a match cannot join them into another match.
	marker := rune('•')
	for strings.ContainsRune(secret, marker) {
		marker++
		if marker > 0x10ffff {
			marker = 0xe000
		}
	}
	return strings.Repeat(string(marker), 4)
}

func redactSecretText(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, secretMaskFor(secret))
}

type redactedHardwareError struct {
	cause error
	text  string
}

func (err *redactedHardwareError) Error() string { return err.text }
func (err *redactedHardwareError) Unwrap() error { return err.cause }
func redactHardwareError(err error, secret string) error {
	if err == nil {
		return nil
	}
	if secret == "" || !strings.Contains(err.Error(), secret) {
		return err
	}
	return &redactedHardwareError{cause: err, text: redactSecretText(err.Error(), secret)}
}

var (
	ErrLoginCredentialsNotConfigured = errors.New("hardware login credentials are not configured")
	ErrLoginOptionsInvalid           = errors.New("hardware login options are invalid")
	ErrLoginRetryLimit               = errors.New("hardware login retry limit reached; reconnect or explicitly raise retries within the allowed limit")
	ErrHardwareEncodingInvalid       = errors.New("hardware input encoding is invalid")
)
