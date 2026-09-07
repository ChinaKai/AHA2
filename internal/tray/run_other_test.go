//go:build !windows

package tray

import (
	"errors"
	"testing"
)

func TestRunReturnsUnsupportedOffWindows(t *testing.T) {
	if err := Run(Config{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Run error=%v", err)
	}
}
