package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/hardware"
	"net"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type hardwareAuthorizationFixture struct {
	h          *coreMCPHarness
	task       domain.Task
	turn       domain.Turn
	token      string
	server     *httptest.Server
	manager    *hardware.Manager
	listener   net.Listener
	accepted   chan net.Conn
	accepts    atomic.Int32
	executions atomic.Int32
	board      domain.HardwareResource
	adb        domain.HardwareResource
	secret     string
}

func newHardwareAuthorizationFixture(t *testing.T, pet bool) *hardwareAuthorizationFixture {
	t.Helper()
	h := newCoreMCPHarness(t)
	base := createHardwareAPITask(t, h.database)
	task := base
	if pet {
		task = createPetDomainHTTPTask(t, h, base)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	f := &hardwareAuthorizationFixture{h: h, task: task, listener: listener, accepted: make(chan net.Conn, 8), secret: ""}
	var secretBytes [24]byte
	if _, err := rand.Read(secretBytes[:]); err != nil {
		t.Fatal("fixture secret generation failed")
	}
	f.secret = hex.EncodeToString(secretBytes[:])
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.accepts.Add(1)
			f.accepted <- conn
		}
	}()
	now := time.Now().UTC()
	host, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	f.board = domain.HardwareResource{ID: domain.NewID("resource"), Description: "Loopback fixture", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: host, Port: port, Protocol: domain.HardwareProtocolRaw}, Username: "fixture-user", CredentialRef: "fixture/login", PasswordConfigured: true, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}
	f.adb = domain.HardwareResource{ID: domain.NewID("resource"), Description: "Fake ADB", Mode: domain.HardwareModeNetwork, Network: domain.HardwareNetworkConfig{Host: "192.0.2.24", Port: 5555, Protocol: domain.HardwareProtocolADB}, Access: domain.HardwareAccessReadWrite, CreatedAt: now, UpdatedAt: now}
	for _, item := range []domain.HardwareResource{f.board, f.adb} {
		if err := h.database.UpsertHardwareResource(h.ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.database.ReplaceTaskHardwareBindings(h.ctx, task.ID, []domain.TaskHardwareBinding{{TaskID: task.ID, ID: "board", ResourceID: f.board.ID}, {TaskID: task.ID, ID: "device", ResourceID: f.adb.ID}}); err != nil {
		t.Fatal(err)
	}
	f.manager = hardware.NewManager(h.database)
	t.Cleanup(func() { _ = f.manager.Close() })
	secrets := &fakeSecretStore{values: map[string]string{"fixture/login": f.secret}}
	f.server = httptest.NewServer(New(Config{Store: h.database, App: h.service, Auth: auth.NewService(h.database, "setup-test", time.Hour), AgentCapabilities: h.caps, Hardware: f.manager, Secrets: secrets, ExecuteADB: func(ctx context.Context, _ string, _ []string, _ []byte, _ time.Duration) (hardware.ADBExecResult, error) {
		if ctx.Err() != nil {
			return hardware.ADBExecResult{}, ctx.Err()
		}
		f.executions.Add(1)
		return hardware.ADBExecResult{Stdout: []byte("fake-adb-result")}, nil
	}}).Handler())
	t.Cleanup(f.server.Close)
	if task.Status == domain.TaskDraft {
		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, task.Status, domain.TaskPreparing, now.Format(time.RFC3339Nano), ""); err != nil {
			t.Fatal(err)
		}
		if err := h.database.UpdateTaskStatus(h.ctx, task.ID, domain.TaskPreparing, domain.TaskActive, now.Format(time.RFC3339Nano), ""); err != nil {
			t.Fatal(err)
		}
	}
	f.task.Status = domain.TaskActive
	if !pet {
		assistant, err := h.database.EnsureOwnerAssistant(h.ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.database.UpdateOwnerPetPermissions(h.ctx, assistant.ID, false, false, false, false, false, false, false, false, now); err != nil {
			t.Fatal(err)
		}
	}

	f.turn = createCoreTurn(t, h.database, task, "main", domain.NewID("inbox_batch"))
	f.token = h.issue(task.ID, "main", f.turn.ID)
	return f
}

func (f *hardwareAuthorizationFixture) mcp(t *testing.T, name string, args map[string]any) coreMCPToolCall {
	t.Helper()
	return coreMCPCallTool(t, f.server.URL+"/api/v1/agent/hardware/mcp", f.token, name, args)
}

func (f *hardwareAuthorizationFixture) connection(t *testing.T) net.Conn {
	t.Helper()
	select {
	case conn := <-f.accepted:
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	case <-time.After(3 * time.Second):
		t.Fatal("fixture did not accept a connection")
		return nil
	}
}

func (f *hardwareAuthorizationFixture) raw(t *testing.T, method, path string, body any) int {
	t.Helper()
	response := agentRequest(t, f.server.URL+"/api/v1/agent/hardware"+path, method, f.token, body)
	defer response.Body.Close()
	return response.StatusCode
}
