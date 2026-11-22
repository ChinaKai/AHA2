package workspace

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSSHScanFailureCallsCancelBeforeClosingConnection(t *testing.T) {
	signer, key := newSSHKeyPair(t)
	listener, _, serverErrors := startStreamingSSHServer(t, signer, func(_ string, stdin io.Reader, stdout io.Writer) error {
		if _, err := io.WriteString(stdout, strings.Repeat("x", 2048)); err != nil {
			return err
		}
		// Keep the session alive so only scan failure, not natural exit, triggers
		// cancellation. The client disconnect releases this read without a sleep.
		_, err := io.Copy(io.Discard, stdin)
		return err
	})
	runner := &SSHRunner{Host: "127.0.0.1", User: "dev", Platform: "linux", Port: listener.Addr().(*net.TCPAddr).Port, KnownHostsPaths: []string{writeKnownHosts(t, listener.Addr().String(), signer.PublicKey())}, PrivateKeyPaths: []string{writePrivateKey(t, key)}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := runner.dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	calls := 0
	var hookError error
	_, err = runner.execOnClientWithCancel(ctx, client, "synthetic", "", make(chan string), 512, nil, func() {
		calls++
		// ssh.DiscardRequests replies false to this probe. A nil transport error
		// proves the hook ran while the same authenticated connection was alive.
		_, _, hookError = client.SendRequest("aha-scan-order-probe", true, nil)
	}, 1024)
	if err == nil || !strings.Contains(err.Error(), "line exceeds 1024 byte scan limit") {
		t.Fatalf("want scan failure, got %v", err)
	}
	if calls != 1 || hookError != nil {
		t.Fatalf("onCancel calls=%d, connection during hook=%v", calls, hookError)
	}
	if _, _, err := client.SendRequest("after-close", true, nil); err == nil {
		t.Fatal("connection remained open after cancellation")
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestWSLScanFailureCleansMarkedRun(t *testing.T) {
	original := wslProcessRunner
	t.Cleanup(func() { wslProcessRunner = original })
	scanError := fmt.Errorf("wrapped scan failure: %w", errProcessOutputScan)
	ordinaryError := errors.New("synthetic ordinary failure")
	for _, tc := range []struct {
		name                          string
		killTree, stream, wantCleanup bool
		failure                       error
	}{
		{name: "scan", killTree: true, wantCleanup: true, failure: scanError},
		{name: "streaming-scan", killTree: true, stream: true, wantCleanup: true, failure: scanError},
		{name: "unmarked-scan", failure: scanError},
		{name: "ordinary-error", killTree: true, failure: ordinaryError},
		{name: "success", killTree: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hosts []Command
			wslProcessRunner = func(_ context.Context, host Command, _ []string, _ LineHandler) (Result, error) {
				hosts = append(hosts, host)
				if len(hosts) == 1 {
					return Result{ExitCode: 7, Stdout: "synthetic-tail"}, tc.failure
				}
				return Result{}, nil // Only capture the cleanup invocation; never run WSL.
			}
			environment := map[string]string{"SYNTHETIC": "value"}
			command := Command{Executable: "synthetic", KillTree: tc.killTree, Env: environment, Timeout: 2 * time.Second, StdoutLineLimit: 12345}
			if tc.stream {
				command.Input = make(chan string)
			}
			result, err := (WSLRunner{Distro: "synthetic-distro"}).Run(context.Background(), command, nil)
			if err != tc.failure || result.ExitCode != 7 || result.Stdout != "synthetic-tail" {
				t.Fatalf("original result/error changed: result=%+v err=%v", result, err)
			}
			wantCalls := 1
			if tc.wantCleanup {
				wantCalls = 2
			}
			if len(hosts) != wantCalls {
				t.Fatalf("WSL process calls=%d, want %d (marked cleanup)", len(hosts), wantCalls)
			}
			if _, ok := environment[runMarkEnv]; ok {
				t.Fatal("caller environment was modified")
			}
			if !tc.wantCleanup {
				return
			}
			script := hosts[0].Stdin
			if tc.stream {
				line, _, _ := strings.Cut(script, "\n")
				decoded, e := base64.StdEncoding.DecodeString(line)
				if e != nil {
					t.Fatal(e)
				}
				script = string(decoded)
			}
			mark := regexp.MustCompile(`export ` + runMarkEnv + `='?([0-9a-f]{24})'?`).FindStringSubmatch(script)
			if len(mark) != 2 {
				t.Fatal("original run was not marked")
			}
			cleanup := hosts[1]
			if cleanup.KillTree || cleanup.Timeout != remoteKillTimeout || !strings.Contains(cleanup.Stdin, "aha-kill") || !strings.Contains(cleanup.Stdin, mark[1]) {
				t.Fatal("cleanup did not target the same mark through a bounded unmarked command")
			}
		})
	}
}
