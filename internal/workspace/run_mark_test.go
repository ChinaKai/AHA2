package workspace

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func processAlive(pid int) bool {
	// Signal 0 checks existence; a zombie still "exists", so read its state too.
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	fields := strings.Fields(string(stat[strings.LastIndex(string(stat), ")")+1:]))
	return len(fields) > 0 && fields[0] != "Z"
}

// The kill script must end every process of a marked run, including a child
// that moved into its own session (which a process-group kill would miss), and
// must leave a process without the mark alone.
func TestKillMarkedProcessesEndsTheWholeMarkedTree(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the mark is found through /proc")
	}
	mark := newRunMark()
	if mark == "" {
		t.Fatal("no run mark")
	}
	start := func(env string, script string) *exec.Cmd {
		t.Helper()
		process := exec.Command("sh", "-c", script)
		process.Env = append(os.Environ(), env)
		process.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := process.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = process.Process.Kill(); _, _ = process.Process.Wait() })
		return process
	}
	// A parent that starts a child and waits, like an Agent running a tool.
	marked := start(runMarkEnv+"="+mark, "sleep 300 & echo $! > "+t.TempDir()+"/child; wait")
	// An unrelated process with a different mark.
	other := start(runMarkEnv+"="+newRunMark(), "sleep 300")
	time.Sleep(300 * time.Millisecond)

	waited := make(chan struct{})
	go func() { _, _ = marked.Process.Wait(); close(waited) }()
	KillMarkedProcesses(LocalRunner{}, mark)
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("the marked process survived the kill")
	}
	if !processAlive(other.Process.Pid) {
		t.Fatal("a process with another mark was killed")
	}
	// Nothing carrying the mark is left, child included.
	result, err := LocalRunner{}.Run(context.Background(), Command{
		Executable: "sh", Timeout: 10 * time.Second,
		Args: []string{"-c", `for d in /proc/[0-9]*; do tr '\0' '\n' < "$d/environ" 2>/dev/null | grep -qxF "` + runMarkEnv + `=$1" && echo "${d#/proc/}"; done; exit 0`, "scan", mark},
	}, nil)
	if err != nil || strings.TrimSpace(result.Stdout) != "" {
		t.Fatalf("processes still carry the mark: %q err=%v", result.Stdout, err)
	}
	// An empty mark must never match anything.
	KillMarkedProcesses(LocalRunner{}, "")
	if !processAlive(other.Process.Pid) {
		t.Fatal("an empty mark killed a process")
	}
}

func TestMarkCommandOnlyTagsKillTreeCommandsAndCopiesEnv(t *testing.T) {
	env := map[string]string{"A": "1"}
	plain, mark := markCommand(Command{Executable: "x", Env: env})
	if mark != "" || len(plain.Env) != 1 {
		t.Fatalf("a command that did not ask for KillTree was marked: %q %#v", mark, plain.Env)
	}
	tagged, mark := markCommand(Command{Executable: "x", Env: env, KillTree: true})
	if mark == "" || tagged.Env[runMarkEnv] != mark || tagged.Env["A"] != "1" {
		t.Fatalf("mark=%q env=%#v", mark, tagged.Env)
	}
	if _, leaked := env[runMarkEnv]; leaked {
		t.Fatal("markCommand modified the caller's environment map")
	}
}

// Interrupting a Turn on an SSH workspace has to end the remote Agent. Closing
// the connection alone leaves it running, so the runner must first ask the
// remote host, over a second session, to kill the processes carrying the mark
// it exported into the run.
func TestSSHRunnerKillsTheMarkedRemoteRunOnCancel(t *testing.T) {
	hostSigner, clientKey := newSSHKeyPair(t)
	config := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, nil
	}}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	scripts := make(chan string, 1)
	kills := make(chan string, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		server, channels, requests, err := ssh.NewServerConn(connection, config)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for channelRequest := range channels {
			channel, channelRequests, err := channelRequest.Accept()
			if err != nil {
				return
			}
			go func() {
				defer channel.Close()
				var payload struct{ Command string }
				for request := range channelRequests {
					if request.Type != "exec" {
						_ = request.Reply(false, nil)
						continue
					}
					_ = ssh.Unmarshal(request.Payload, &payload)
					_ = request.Reply(true, nil)
					break
				}
				if strings.Contains(payload.Command, "aha-kill") {
					kills <- payload.Command
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					return
				}
				// The run itself: report the script it was given, then keep
				// "running" until the client goes away.
				data, _ := io.ReadAll(io.LimitReader(channel, 1<<20))
				scripts <- string(data)
				_ = server.Wait()
			}()
		}
	}()
	runner := &SSHRunner{
		Host: "127.0.0.1", User: "dev", Platform: "linux",
		Port:            listener.Addr().(*net.TCPAddr).Port,
		KnownHostsPaths: []string{writeKnownHosts(t, listener.Addr().String(), hostSigner.PublicKey())},
		PrivateKeyPaths: []string{writePrivateKey(t, clientKey)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, Command{Executable: "codex", Args: []string{"exec"}, KillTree: true, Timeout: 30 * time.Second}, nil)
		done <- err
	}()
	var script string
	select {
	case script = <-scripts:
	case <-time.After(10 * time.Second):
		t.Fatal("the remote run never received its script")
	}
	exported := regexp.MustCompile(`export ` + runMarkEnv + `='?([0-9a-f]{24})'?`).FindStringSubmatch(script)
	if exported == nil {
		t.Fatalf("the remote script does not export a run mark:\n%s", script)
	}
	cancel()
	select {
	case command := <-kills:
		if !strings.Contains(command, exported[1]) {
			t.Fatalf("the kill names a different mark than the run exported %q: %s", exported[1], command)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the run did not ask the remote host to kill it")
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}
