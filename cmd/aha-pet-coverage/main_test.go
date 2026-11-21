package main

import (
	"bytes"
	"strings"
	"testing"
)

// The CLI runs against the real repository: the checked-in ledgers pass the
// drift/evidence check, -complete still fails while rows are open, and bad
// arguments are rejected. -update is exercised on synthetic trees in
// internal/petcoverage, never on the real checkout from a test.
func TestCommandChecksRealLedgersAndKeepsCompleteGateClosed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("check exit %d: %s%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "HTTP operations: ") || !strings.Contains(stdout.String(), "Handle/HandleFunc calls: ") {
		t.Fatalf("summary missing: %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-complete"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "coverage is incomplete") {
		t.Fatalf("complete gate exit %d: %s", code, stderr.String())
	}
	for _, args := range [][]string{{"-unknown"}, {"extra"}} {
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%v exit %d", args, code)
		}
	}
}
