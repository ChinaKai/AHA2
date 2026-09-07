package tray

import (
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
