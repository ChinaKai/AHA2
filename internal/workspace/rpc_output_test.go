package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

const rpcOutputHelperEnv = "AHA2_RPC_OUTPUT_TEST_HELPER"
const rpcTestLineLimit = 16 * 1024 * 1024

// Only synthetic JSON is written by this subprocess; no CLI or model is used.
func TestRunProcessRPCOutputHelper(t *testing.T) {
	mode := os.Getenv(rpcOutputHelperEnv)
	if mode == "" {
		t.Skip("subprocess helper")
	}
	if mode == "large" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"id": 1, "result": map[string]any{"padding": strings.Repeat("x", 5*1024*1024)}})
		_, _ = io.WriteString(os.Stdout, `{"id":2,"result":{"ok":true}}`+"\n")
		os.Exit(0)
	}
	stream := os.Stdout
	size := rpcTestLineLimit + 1024
	if mode == "stderr" {
		stream = os.Stderr
		size = 5 * 1024 * 1024
	}
	_, _ = io.WriteString(stream, "synthetic-private-history-api_key=not-a-real-key"+strings.Repeat("x", size))
	time.Sleep(30 * time.Second) // Explicit scan failure must stop a live producer.
	os.Exit(0)
}

func runRPCOutputHelper(t *testing.T, mode string, handler LineHandler) (Result, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return (LocalRunner{}).Run(context.Background(), Command{
		Executable: exe, Args: []string{"-test.run=^TestRunProcessRPCOutputHelper$"},
		Env:     map[string]string{rpcOutputHelperEnv: mode},
		Timeout: 8 * time.Second, KillTree: true, OutputLimit: 512, StdoutLineLimit: rpcTestLineLimit,
	}, handler)
}

func TestRunProcessRPCFrameAbove4MiBAndFollowingResponse(t *testing.T) {
	var ids []int
	var parseErr error
	result, err := runRPCOutputHelper(t, "large", func(line string) {
		var frame struct {
			ID     int `json:"id"`
			Result struct {
				Padding string `json:"padding"`
				OK      bool   `json:"ok"`
			} `json:"result"`
		}
		if e := json.Unmarshal([]byte(line), &frame); e != nil {
			parseErr = e
			return
		}
		if frame.ID == 1 && len(frame.Result.Padding) != 5*1024*1024 {
			parseErr = fmt.Errorf("wrong padding length")
		}
		if frame.ID == 2 && !frame.Result.OK {
			parseErr = fmt.Errorf("missing following result")
		}
		ids = append(ids, frame.ID)
	})
	if err != nil || result.ExitCode != 0 || parseErr != nil {
		t.Fatalf("exit=%d err=%v parse=%v", result.ExitCode, err, parseErr)
	}
	if fmt.Sprint(ids) != "[1 2]" {
		t.Fatalf("delivered IDs=%v, want both large and following response", ids)
	}
	if len(result.Stdout) > 512 || !strings.Contains(result.Stdout, `"id":2`) {
		t.Fatal("retained output tail changed")
	}
}

func TestRunProcessRPCFrameLimitFailsPromptly(t *testing.T) {
	for _, mode := range []string{"stdout", "stderr"} {
		t.Run(mode, func(t *testing.T) {
			start := time.Now()
			calls := 0
			result, err := runRPCOutputHelper(t, mode, func(string) { calls++ })
			limit := rpcTestLineLimit
			if mode == "stderr" {
				limit = 4 * 1024 * 1024
			}
			if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("line exceeds %d byte scan limit", limit)) {
				t.Fatalf("want explicit scan limit error, got %v", err)
			}
			if time.Since(start) > 6*time.Second {
				t.Fatalf("scan error did not stop live producer promptly: %s", time.Since(start))
			}
			if calls != 0 || result.Stdout != "" || result.Stderr != "" {
				t.Fatal("partial rejected frame was delivered or retained")
			}
			if strings.Contains(err.Error(), "synthetic-private-history") || strings.Contains(err.Error(), "not-a-real-key") {
				t.Fatal("scan error disclosed rejected frame")
			}
		})
	}
}
