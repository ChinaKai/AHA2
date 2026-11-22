package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSSHRunnerRPCOutputLimits(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		name := "large"
		if overflow {
			name = "overflow"
		}
		t.Run(name, func(t *testing.T) {
			signer, key := newSSHKeyPair(t)
			listener, _, serverErrors := startStreamingSSHServer(t, signer, func(_ string, stdin io.Reader, stdout io.Writer) error {
				size := 5 * 1024 * 1024
				if overflow {
					size = 17 * 1024 * 1024
				}
				if err := json.NewEncoder(stdout).Encode(map[string]any{"id": 1, "result": strings.Repeat("x", size)}); err != nil {
					return err
				}
				if _, err := io.WriteString(stdout, `{"id":2,"result":true}`+"\n"); err != nil {
					return err
				}
				// Wait for the receiver to acknowledge the final frame by closing
				// stdin, before the fixture closes its TCP connection.
				_, err := io.Copy(io.Discard, stdin)
				return err
			})
			runner := &SSHRunner{Host: "127.0.0.1", User: "dev", Platform: "linux", Port: listener.Addr().(*net.TCPAddr).Port, KnownHostsPaths: []string{writeKnownHosts(t, listener.Addr().String(), signer.PublicKey())}, PrivateKeyPaths: []string{writePrivateKey(t, key)}}
			var ids []int
			var parseErr error
			input := make(chan string)
			start := time.Now()
			result, err := runner.Run(context.Background(), Command{Executable: "synthetic", Input: input, Timeout: 8 * time.Second, StdoutLineLimit: rpcTestLineLimit, OutputLimit: 512}, func(line string) {
				var frame struct {
					ID int `json:"id"`
				}
				if e := json.Unmarshal([]byte(line), &frame); e != nil {
					parseErr = e
				}
				ids = append(ids, frame.ID)
				if frame.ID == 2 {
					close(input)
				}
			})
			if overflow {
				if err == nil || !strings.Contains(err.Error(), "line exceeds 16777216 byte scan limit") || len(ids) != 0 {
					t.Fatalf("limit error=%v IDs=%v", err, ids)
				}
				if time.Since(start) > 6*time.Second {
					t.Fatal("SSH scan failure did not terminate promptly")
				}
			} else if err != nil || result.ExitCode != 0 || parseErr != nil || fmt.Sprint(ids) != "[1 2]" {
				t.Fatalf("exit=%d err=%v parse=%v IDs=%v", result.ExitCode, err, parseErr, ids)
			}
			if err := <-serverErrors; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWSLHostCommandPreservesRPCOutputLimits(t *testing.T) {
	for _, stream := range []bool{false, true} {
		command := Command{Executable: "synthetic", OutputLimit: 512, StdoutLineLimit: rpcTestLineLimit}
		if stream {
			command.Input = make(chan string)
		}
		host, err := wslHostCommand("synthetic-distro", command)
		if err != nil {
			t.Fatal(err)
		}
		if host.StdoutLineLimit != command.StdoutLineLimit || host.OutputLimit != command.OutputLimit {
			t.Fatalf("WSL lost output bounds: line=%d tail=%d", host.StdoutLineLimit, host.OutputLimit)
		}
	}
}
