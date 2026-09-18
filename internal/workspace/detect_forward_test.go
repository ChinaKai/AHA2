package workspace

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// capturingProbeRunner records the command the probe builds and answers it the way
// a real workspace would if the tunnel worked.
type capturingProbeRunner struct {
	command Command
}

func (runner *capturingProbeRunner) Run(_ context.Context, command Command, _ LineHandler) (Result, error) {
	runner.command = command
	return Result{ExitCode: 0, Stdout: aha2HealthPayload}, nil
}

// TestProbeAgentAPIThroughForwardRequestsTheTunnel pins the change that fixes
// detection reporting a healthy remote workspace as unreachable.
//
// Detection used to probe the configured address directly. For a remote workspace
// that address is AHA's loopback, which inside the workspace is the workspace
// itself, so the probe could never succeed even though Turns over the tunnel did.
// The contract that must hold is therefore about what the probe *asks for*: it has
// to request a forward, and it has to read the address from the injected variable
// rather than dialing a fixed one. Asserting the request is exact; asserting a
// live round trip would depend on this host's network position and could pass for
// the wrong reason.
func TestProbeAgentAPIThroughForwardRequestsTheTunnel(t *testing.T) {
	item := domain.Workspace{
		ID: "ws", Name: "remote", Locality: "remote", Transport: "ssh",
		SSHHost: "101.201.66.192", SSHUser: "root", SSHPort: 22, RootPath: "/home/kk-test",
	}
	runner := &capturingProbeRunner{}
	const base = "http://127.0.0.1:8766"

	if err := probeAgentAPIThroughForwardWithRunner(context.Background(), item, base, runner); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if runner.command.ReverseForward == nil {
		t.Fatal("detection did not request a forward; a remote workspace would be reported unreachable")
	}
	if got := runner.command.ReverseForward.Target; got != "127.0.0.1:8766" {
		t.Fatalf("forward target = %q, want the AHA-side address", got)
	}
	if got := runner.command.ReverseForward.EnvName; got != "AHA2_AGENT_API_URL" {
		t.Fatalf("forward env = %q", got)
	}
	// The command must dial whatever the variable holds, so the runner's override
	// is what it uses. A hardcoded address here would bypass the tunnel.
	if got := runner.command.Env["AHA2_AGENT_API_URL"]; got != base {
		t.Fatalf("seeded env = %q, want %q", got, base)
	}
	script := runner.command.Args[len(runner.command.Args)-1]
	if !strings.Contains(script, `$AHA2_AGENT_API_URL/healthz`) {
		t.Fatalf("probe does not read the injected variable: %q", script)
	}
}

// TestProbeAgentAPIThroughForwardFallsBackForOtherTransports pins that only ssh
// gets the tunnel treatment.
//
// WSL and native run on this host, so a plain probe is the correct check for them.
// Extending the tunnel to them would be a different mechanism rather than a
// stronger check, and silently changing what non-ssh workspaces are tested against
// is how a working setup starts failing.
func TestProbeAgentAPIThroughForwardFallsBackForOtherTransports(t *testing.T) {
	ahaListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ahaListener.Close()
	aha := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, aha2HealthPayload)
	})}
	go func() { _ = aha.Serve(ahaListener) }()
	defer aha.Close()

	for _, transport := range []string{"native", "wsl"} {
		item := domain.Workspace{ID: "ws", Name: "local", Locality: "local", Transport: transport}
		runner := &capturingProbeRunner{}
		if err := probeAgentAPIThroughForwardWithRunner(context.Background(), item, "http://"+ahaListener.Addr().String(), runner); err != nil {
			t.Fatalf("%s: plain probe should succeed: %v", transport, err)
		}
		if runner.command.ReverseForward != nil {
			t.Fatalf("%s: a forward was requested for a transport that cannot carry one", transport)
		}
	}
}
