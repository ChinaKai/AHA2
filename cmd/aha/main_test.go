package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseCommandRoutesServeAndWindowsService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args        []string
		command     string
		commandArgs []string
		errorText   string
	}{
		{command: "serve"},
		{args: []string{"--listen", "127.0.0.1:9000"}, command: "serve", commandArgs: []string{"--listen", "127.0.0.1:9000"}},
		{args: []string{"serve", "--data-dir", "data"}, command: "serve", commandArgs: []string{"--data-dir", "data"}},
		{args: []string{"service", "run", "--listen", "127.0.0.1:8766"}, command: "service-run", commandArgs: []string{"--listen", "127.0.0.1:8766"}},
		{args: []string{"version"}, command: "version"},
		{args: []string{"service"}, errorText: "aha2 service run"},
		{args: []string{"service", "stop"}, errorText: "aha2 service run"},
		{args: []string{"unknown"}, errorText: "unknown command"},
	}
	for _, test := range tests {
		command, args, err := parseCommand(test.args)
		if test.errorText != "" {
			if err == nil || !strings.Contains(err.Error(), test.errorText) {
				t.Errorf("parseCommand(%q) error=%v", test.args, err)
			}
			continue
		}
		if err != nil || command != test.command || strings.Join(args, "\x00") != strings.Join(test.commandArgs, "\x00") {
			t.Errorf("parseCommand(%q) = %q, %q, %v", test.args, command, args, err)
		}
	}
}

func TestParseServiceRunOptions(t *testing.T) {
	t.Setenv("AHA2_LISTEN", "")
	t.Setenv("AHA2_DATA_DIR", "")
	options, err := parseServeOptions("aha2 service run", []string{
		"--listen", "127.0.0.1:8766", "--data-dir", `C:\AHA2\data`, "--secure-cookie",
		"--agent-api-url", "https://aha.example.test", "--codex-bin", "codex.exe", "--claude-bin", "claude.exe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.listen != "127.0.0.1:8766" || options.dataDir != `C:\AHA2\data` || !options.secureCookie || options.agentAPIURL != "https://aha.example.test" || options.codexBinary != "codex.exe" || options.claudeBinary != "claude.exe" {
		t.Fatalf("service options=%#v", options)
	}
	if _, err := parseServeOptions("aha2 service run", []string{"--listen", "127.0.0.1:8766", "extra"}); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("unexpected positional argument error=%v", err)
	}
}

func nextServiceStatus(t *testing.T, statuses <-chan serviceStatus) serviceStatus {
	t.Helper()
	select {
	case status := <-statuses:
		return status
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for service status")
		return serviceStatus{}
	}
}

func TestServiceLifecycleReportsStatesAndStopsGracefully(t *testing.T) {
	for _, control := range []serviceControl{serviceStop, serviceShutdown} {
		control := control
		t.Run(map[serviceControl]string{serviceStop: "stop", serviceShutdown: "shutdown"}[control], func(t *testing.T) {
			controls := make(chan serviceControl, 1)
			statuses := make(chan serviceStatus, 8)
			cancelled := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				finished <- runServiceLifecycle(context.Background(), func(ctx context.Context, ready func()) error {
					ready()
					<-ctx.Done()
					close(cancelled)
					return ctx.Err()
				}, controls, statuses)
			}()
			if status := nextServiceStatus(t, statuses); status.state != serviceStartPending || status.waitHint == 0 {
				t.Fatalf("start status=%#v", status)
			}
			if status := nextServiceStatus(t, statuses); status.state != serviceRunning || !status.acceptStop || !status.acceptDown {
				t.Fatalf("running status=%#v", status)
			}
			controls <- serviceInterrogate
			if status := nextServiceStatus(t, statuses); status.state != serviceRunning {
				t.Fatalf("interrogate status=%#v", status)
			}
			controls <- control
			if status := nextServiceStatus(t, statuses); status.state != serviceStopPending || status.waitHint == 0 {
				t.Fatalf("stop pending status=%#v", status)
			}
			if status := nextServiceStatus(t, statuses); status.state != serviceStopped {
				t.Fatalf("stopped status=%#v", status)
			}
			select {
			case <-cancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("service run context was not cancelled")
			}
			if err := <-finished; err != nil {
				t.Fatalf("graceful service stop error=%v", err)
			}
		})
	}
}

func TestServiceLifecycleReportsStartupFailure(t *testing.T) {
	want := errors.New("listen failed")
	statuses := make(chan serviceStatus, 4)
	finished := make(chan error, 1)
	go func() {
		finished <- runServiceLifecycle(context.Background(), func(context.Context, func()) error {
			return want
		}, make(chan serviceControl), statuses)
	}()
	if status := nextServiceStatus(t, statuses); status.state != serviceStartPending {
		t.Fatalf("start status=%#v", status)
	}
	if status := nextServiceStatus(t, statuses); status.state != serviceStopped {
		t.Fatalf("stopped status=%#v", status)
	}
	if err := <-finished; !errors.Is(err, want) {
		t.Fatalf("startup error=%v", err)
	}
}

func TestServiceLifecycleCanStopWhileStarting(t *testing.T) {
	controls := make(chan serviceControl, 1)
	statuses := make(chan serviceStatus, 4)
	finished := make(chan error, 1)
	go func() {
		finished <- runServiceLifecycle(context.Background(), func(ctx context.Context, _ func()) error {
			<-ctx.Done()
			return ctx.Err()
		}, controls, statuses)
	}()
	if status := nextServiceStatus(t, statuses); status.state != serviceStartPending {
		t.Fatalf("start status=%#v", status)
	}
	controls <- serviceStop
	if status := nextServiceStatus(t, statuses); status.state != serviceStopPending {
		t.Fatalf("stop pending status=%#v", status)
	}
	if status := nextServiceStatus(t, statuses); status.state != serviceStopped {
		t.Fatalf("stopped status=%#v", status)
	}
	if err := <-finished; err != nil {
		t.Fatalf("startup stop error=%v", err)
	}
}

func TestResolveAgentAPIURL(t *testing.T) {
	tests := []struct {
		listen, configured, expected string
	}{
		{"0.0.0.0:8766", "", "http://127.0.0.1:8766"},
		{"[::]:9000", "", "http://127.0.0.1:9000"},
		{"127.0.0.1:7000", "", "http://127.0.0.1:7000"},
		{"0.0.0.0:8766", "https://aha.example.test/", "https://aha.example.test"},
	}
	for _, test := range tests {
		if actual := resolveAgentAPIURL(test.listen, test.configured); actual != test.expected {
			t.Errorf("resolveAgentAPIURL(%q, %q) = %q, want %q", test.listen, test.configured, actual, test.expected)
		}
	}
}

func TestValidateAgentAPIURLRequiresHTTPSOffLoopback(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:8766", "http://localhost:8766", "https://192.0.2.10"} {
		if err := validateAgentAPIURL(value, false); err != nil {
			t.Errorf("%s: %v", value, err)
		}
	}
	if err := validateAgentAPIURL("http://192.0.2.10:8766", false); err == nil || !strings.Contains(err.Error(), "requires https") {
		t.Fatalf("insecure remote URL error = %v", err)
	}
	if err := validateAgentAPIURL("http://192.0.2.10:8766", true); err != nil {
		t.Fatal(err)
	}
	if err := validateAgentAPIURL("https://user:secret@example.test", false); err == nil {
		t.Fatal("URL credentials were accepted")
	}
}
