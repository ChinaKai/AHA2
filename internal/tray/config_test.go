package tray

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgsAndBuildServerCommand(t *testing.T) {
	server := filepath.Join(t.TempDir(), "AHA 2", "aha2.exe")
	dataDir := filepath.Join(t.TempDir(), "AHA data")
	config, err := ParseArgs([]string{
		"--server", server,
		"--listen", "0.0.0.0:8766",
		"--data-dir", dataDir,
		"--agent-api-url", "http://192.0.2.10:8766",
		"--allow-insecure-agent-api",
	}, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	command, err := BuildServerCommand(config)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{
		"serve", "--listen", "0.0.0.0:8766", "--data-dir", config.DataDir,
		"--agent-api-url", "http://192.0.2.10:8766", "--allow-insecure-agent-api",
	}
	if command.Path != config.Server || command.Dir != filepath.Dir(config.Server) || !reflect.DeepEqual(command.Args, wantArgs) {
		t.Fatalf("command=%#v config=%#v", command, config)
	}
	if config.Version != "v1.2.3" {
		t.Fatalf("version=%q", config.Version)
	}
}

func TestParseArgsRejectsIncompleteOrInvalidContract(t *testing.T) {
	tests := [][]string{
		{"--server", "aha2.exe", "--listen", "127.0.0.1:8766"},
		{"--server", "aha2.exe", "--listen", "localhost:8766", "--data-dir", "data"},
		{"--server", "aha2.exe", "--listen", "127.0.0.1:70000", "--data-dir", "data"},
		{"--server", "aha2.exe", "--listen", "127.0.0.1:8766", "--data-dir", "data", "--agent-api-url", "https://user@example.test"},
		{"--server", "aha2.exe", "--listen", "127.0.0.1:8766", "--data-dir", "data", "--agent-api-url", "http://192.0.2.10:8766"},
	}
	for _, args := range tests {
		if _, err := ParseArgs(args, "dev"); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
	if _, err := ParseArgs([]string{"--unknown"}, "dev"); err == nil {
		t.Fatal("accepted unknown flag")
	}
}

func TestManagementURLAndSingleInstanceContract(t *testing.T) {
	tests := map[string]string{
		"0.0.0.0:8766":   "http://127.0.0.1:8766",
		"127.0.0.1:9000": "http://127.0.0.1:9000",
		"[::]:8766":      "http://[::1]:8766",
		"[::1]:8766":     "http://[::1]:8766",
	}
	for listen, want := range tests {
		if got := ManagementURL(listen); got != want {
			t.Fatalf("ManagementURL(%q)=%q want %q", listen, got, want)
		}
	}
	if got := ManagementURL("invalid"); got != "" {
		t.Fatalf("invalid management URL=%q", got)
	}
	if !strings.HasPrefix(SingleInstanceName, `Local\`) || !strings.Contains(SingleInstanceName, "AHA2Tray") {
		t.Fatalf("single instance name=%q", SingleInstanceName)
	}
}

type stubRunner struct {
	output string
	err    error
	seen   []string
}

func (runner *stubRunner) Output(command Command) (string, error) {
	runner.seen = append(runner.seen, strings.Join(command.Args, " "))
	if runner.err != nil {
		return "", runner.err
	}
	return runner.output, nil
}

func TestEffectiveListenPrefersTheStoredAddress(t *testing.T) {
	t.Parallel()
	config := Config{Server: `C:\AHA2\aha2.exe`, Listen: "0.0.0.0:8766", DataDir: `C:\AHA2\data`}

	stored := &stubRunner{output: "127.0.0.1:9000\n"}
	if got := EffectiveListen(stored, config); got != "127.0.0.1:9000" {
		t.Fatalf("stored address = %q, want the stored value", got)
	}
	// The query must pass the task's address as the fallback, so a database
	// without an override reports exactly what the task was installed with.
	if len(stored.seen) != 1 || !strings.Contains(stored.seen[0], "--default 0.0.0.0:8766") {
		t.Fatalf("query args = %v, want --default to carry the configured address", stored.seen)
	}
}

func TestEffectiveListenFallsBackWhenResolutionFails(t *testing.T) {
	t.Parallel()
	config := Config{Server: `C:\AHA2\aha2.exe`, Listen: "0.0.0.0:8766", DataDir: `C:\AHA2\data`}

	// A resolver failure or a bad stored value must never leave the tray with an
	// unbindable address: that would take AHA2 down rather than degrade it.
	cases := []struct {
		name   string
		runner *stubRunner
	}{
		{"query failed", &stubRunner{err: errors.New("boom")}},
		{"empty output", &stubRunner{output: "   \n"}},
		{"not an address", &stubRunner{output: "not-an-address"}},
		{"hostname instead of IP", &stubRunner{output: "localhost:8766"}},
		{"port out of range", &stubRunner{output: "0.0.0.0:70000"}},
	}
	for _, test := range cases {
		if got := EffectiveListen(test.runner, config); got != config.Listen {
			t.Fatalf("%s: effective listen = %q, want the configured fallback %q", test.name, got, config.Listen)
		}
	}
}
