//go:build !windows

package main

import (
	"strings"
	"testing"
)

func TestServiceRunClearlyRejectsNonWindows(t *testing.T) {
	t.Parallel()
	err := runPlatformService([]string{"--listen", "127.0.0.1:8766"})
	if err == nil || !strings.Contains(err.Error(), "only supported on Windows") {
		t.Fatalf("non-Windows service error=%v", err)
	}
}
