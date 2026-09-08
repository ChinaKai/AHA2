package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestProbeAgentAPIVerifiesAHA2Health(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			t.Fatalf("path=%s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true,"service":"aha2"}`))
	}))
	defer server.Close()
	if err := ProbeAgentAPI(context.Background(), domain.Workspace{Transport: "native"}, server.URL); err != nil {
		t.Fatal(err)
	}
}

func TestProbeAgentAPIRemoteUsesWorkspaceSideRequest(t *testing.T) {
	t.Parallel()
	calls := 0
	runner := contextRunnerFunc(func(command Command) (Result, error) {
		calls++
		if command.Executable != "sh" || command.Dir != "/repo" || command.Args[len(command.Args)-1] != "https://aha.example.test" || !strings.Contains(command.Args[1], "/healthz") {
			t.Fatalf("command=%#v", command)
		}
		return Result{Stdout: `{"ok":true,"service":"aha2"}`}, nil
	})
	item := domain.Workspace{Transport: "ssh", RootPath: "/repo"}
	if err := probeAgentAPIWithRunner(context.Background(), item, "https://aha.example.test", runner); err != nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestAgentAPIHostCandidatesUseWorkspaceNetworkView(t *testing.T) {
	t.Parallel()
	runner := contextRunnerFunc(func(command Command) (Result, error) {
		if command.Args[len(command.Args)-1] != "wsl" {
			t.Fatalf("command=%#v", command)
		}
		return Result{Stdout: "172.28.0.1\n172.28.0.1\ninvalid\n"}, nil
	})
	result := agentAPIHostCandidatesWithRunner(context.Background(), domain.Workspace{Transport: "wsl", RootPath: "/repo"}, runner)
	if len(result) != 1 || result[0] != "172.28.0.1" {
		t.Fatalf("hosts=%#v", result)
	}
}
