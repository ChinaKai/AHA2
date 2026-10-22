//go:build !windows

package hardware

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenADBTerminalConnectsAndStartsDeviceShell(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "adb")
	script := `#!/bin/sh
if [ "$1" = "mdns" ] && [ "$2" = "services" ]; then
  echo "adb-test _adb-tls-connect._tcp 192.0.2.44:45678"
  exit 0
fi
if [ "$1" = "connect" ] && [ "$2" = "192.0.2.44:45678" ]; then
  echo "connected to $2"
  exit 0
fi
if [ "$1" = "-s" ] && [ "$2" = "192.0.2.44:45678" ] && [ "$3" = "shell" ] && [ "$4" = "-t" ] && [ "$5" = "-t" ]; then
  echo "shell ready"
  IFS= read -r line
  echo "command:$line"
  exit 0
fi
echo "unexpected arguments: $*" >&2
exit 2
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(adbExecutableEnvironment, executable)

	terminal, err := openADBTerminal(context.Background(), "192.0.2.44:5555")
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if adbTerminal, ok := terminal.(*adbTerminal); !ok || adbTerminal.ResolvedEndpoint() != "192.0.2.44:45678" {
		t.Fatalf("ADB terminal did not report the mDNS endpoint: %#v", terminal)
	}
	buffer := make([]byte, 64)
	count, err := terminal.Read(buffer)
	if err != nil || !strings.Contains(string(buffer[:count]), "shell ready") {
		t.Fatalf("initial ADB output=%q err=%v", buffer[:count], err)
	}
	if _, err := terminal.Write([]byte("getprop\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var output strings.Builder
	output.Write(buffer[:count])
	for time.Now().Before(deadline) && !strings.Contains(output.String(), "command:getprop") {
		count, err = terminal.Read(buffer)
		if count > 0 {
			output.Write(buffer[:count])
		}
		if err != nil {
			break
		}
	}
	if !strings.Contains(output.String(), "command:getprop") {
		t.Fatalf("ADB shell did not receive input: %q err=%v", output.String(), err)
	}
}

func TestPairADBUsesPairingEndpointAndRedactsCode(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "adb")
	script := `#!/bin/sh
if [ "$1" = "pair" ] && [ "$2" = "192.0.2.44:37123" ]; then
  IFS= read -r code
  echo "paired with $code"
  exit 0
fi
echo "unexpected arguments: $*" >&2
exit 2
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	output, err := pairADBWithExecutable(context.Background(), executable, "192.0.2.44:37123", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "<pairing-code>") || strings.Contains(output, "123456") {
		t.Fatalf("pairing output was not redacted: %q", output)
	}
	if _, err := PairADB(context.Background(), "192.0.2.44:37123", "12345x"); err == nil {
		t.Fatal("invalid pairing code was accepted")
	}
}

func TestExecuteADBReturnsBinaryResultAndExitCode(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	executable := filepath.Join(directory, "adb")
	script := `#!/bin/sh
if [ "$1" = "mdns" ] && [ "$2" = "services" ]; then
  echo "adb-test _adb-tls-connect._tcp 192.0.2.44:45678"
  exit 0
fi
if [ "$1" = "connect" ] && [ "$2" = "192.0.2.44:45678" ]; then
  echo "connected to $2"
  exit 0
fi
if [ "$1" = "-s" ] && [ "$2" = "192.0.2.44:45678" ] && [ "$3" = "exec-out" ] && [ "$4" = "screencap" ] && [ "$5" = "-p" ]; then
  printf '\211PNG\r\n'
  printf 'capture warning' >&2
  exit 7
fi
exit 2
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := executeADBWithExecutable(
		context.Background(), executable, "192.0.2.44:5555",
		[]string{"exec-out", "screencap", "-p"}, nil, time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Endpoint != "192.0.2.44:45678" || result.ExitCode != 7 || string(result.Stdout) != "\x89PNG\r\n" || string(result.Stderr) != "capture warning" {
		t.Fatalf("unexpected ADB result: %#v", result)
	}
}
