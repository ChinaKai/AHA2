package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentHardwareFourLifecyclesUseFakeTransportForPetAndOrdinaryTask(t *testing.T) {
	for _, pet := range []bool{false, true} {
		t.Run(fmt.Sprintf("pet=%v", pet), func(t *testing.T) {
			f := newHardwareAuthorizationFixture(t, pet)
			result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"})
			if result.Status != 200 || result.IsError || !f.manager.Status(f.task.ID, "board", "network", false).Connected {
				t.Fatalf("connect=%#v", result)
			}

			conn := f.connection(t)
			reader := bufio.NewReader(conn)
			result = f.mcp(t, "hardware_send", map[string]any{"hardware_id": "board", "data": "FIXTURE-PING\n"})
			if result.Status != 200 || result.IsError {
				t.Fatalf("send=%#v", result)
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			line, err := reader.ReadString('\n')
			if err != nil || line != "FIXTURE-PING\n" {
				t.Fatal("send did not reach fake transport")
			}
			result = f.mcp(t, "hardware_login", map[string]any{"hardware_id": "board", "wakeup": false, "line_ending": "lf", "timeout_seconds": 10})
			if result.Status != 200 || result.IsError || strings.Contains(result.Text, f.secret) {
				t.Fatalf("login request failed: status=%d error=%v", result.Status, result.IsError)
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _ = conn.Write([]byte("login:"))
			line, err = reader.ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "fixture-user" {
				t.Fatal("login username did not reach fake transport")
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _ = conn.Write([]byte("password:"))
			line, err = reader.ReadString('\n')
			if err != nil || strings.TrimSpace(line) != f.secret {
				t.Fatal("stored login credential did not reach fake transport")
			}
			_, _ = conn.Write([]byte("$ "))
			deadline := time.Now().Add(3 * time.Second)
			for f.manager.Status(f.task.ID, "board", "network", false).LoginStatus != "authenticated" {
				if time.Now().After(deadline) {
					t.Fatal("fake login did not authenticate")
				}
				time.Sleep(10 * time.Millisecond)
			}
			page, err := f.h.database.HardwareIOPage(f.h.ctx, f.task.ID, "board", "network", 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range page.Items {
				if strings.Contains(event.Data, f.secret) {
					t.Fatal("credential was recorded in terminal history")
				}
			}
			result = f.mcp(t, "hardware_disconnect", map[string]any{"hardware_id": "board"})
			if result.Status != 200 || result.IsError || f.manager.Status(f.task.ID, "board", "network", false).Connected {
				t.Fatalf("disconnect=%#v", result)
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			if _, err = reader.ReadByte(); err == nil {
				t.Fatal("disconnect left fake transport open")
			}
		})
	}
}

func TestADBMCPLargeOutputIsBoundedAfterExactlyOneExecution(t *testing.T) {
	f := newHardwareAuthorizationFixture(t, false)
	server := httptest.NewServer(New(Config{Store: f.h.database, App: f.h.service, AgentCapabilities: f.h.caps, ExecuteADB: func(context.Context, string, []string, []byte, time.Duration) (hardware.ADBExecResult, error) {
		f.executions.Add(1)
		return hardware.ADBExecResult{Stdout: []byte(strings.Repeat("x", 3<<20)), Stderr: []byte("tail"), ExitCode: 7}, nil
	}}).Handler())
	defer server.Close()
	result := coreMCPCallTool(t, server.URL+"/api/v1/agent/hardware/mcp", f.token, "hardware_adb_exec", map[string]any{"hardware_id": "device", "args": []string{"shell", "fixture"}})
	if result.Status != 200 || result.IsError || len(result.Text) > 64<<10 || f.executions.Load() != 1 {
		t.Fatalf("large result: status=%d isError=%v size=%d executions=%d", result.Status, result.IsError, len(result.Text), f.executions.Load())
	}
	var payload map[string]any
	if json.Unmarshal([]byte(result.Text), &payload) != nil || payload["stdout_truncated"] != true || payload["stderr_truncated"] != true || payload["exit_code"] != float64(7) {
		t.Fatal("truncation or command status was lost")
	}
}
