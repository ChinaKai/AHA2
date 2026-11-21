//go:build !windows

package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealADBOutputLimitReportsPossibleExecutionWithoutRetry(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, false)
	directory := t.TempDir()
	executable := filepath.Join(directory, "adb-fixture")
	countFile := filepath.Join(directory, "executions")
	script := `#!/bin/sh
if [ "$1" = "mdns" ]; then exit 0; fi
if [ "$1" = "connect" ]; then printf 'connected\n'; exit 0; fi
if [ "$1" = "-s" ] && [ "$3" = "get-state" ]; then printf 'device\n'; exit 0; fi
if [ "$1" = "-s" ] && [ "$3" = "shell" ]; then
  printf 'executed\n' >> "$AHA_TEST_ADB_EXECUTIONS"
  head -c 16777217 /dev/zero
  exit 0
fi
exit 2
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AHA2_ADB_PATH", executable)
	t.Setenv("AHA_TEST_ADB_EXECUTIONS", countFile)
	// Deliberately use the real ExecuteADB implementation, including its own
	// output bound. Only the executable is a local fixture, never a real device.
	server := httptest.NewServer(New(Config{Store: f.h.database, App: f.h.service, AgentCapabilities: f.h.caps}).Handler())
	defer server.Close()
	result := coreMCPCallTool(t, server.URL+"/api/v1/agent/hardware/mcp", f.token, "hardware_adb_exec", map[string]any{"hardware_id": "device", "args": []string{"shell", "fixture"}, "timeout_seconds": 10})
	if !result.IsError || !strings.Contains(result.Text, "adb_output_limit_may_have_executed") {
		t.Fatal("actual executor limit did not distinguish possibly executed command")
	}
	data, err := os.ReadFile(countFile)
	if err != nil || string(data) != "executed\n" {
		t.Fatal("output limit caused missing/duplicate command execution")
	}
}
