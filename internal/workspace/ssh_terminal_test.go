package workspace

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceTerminalAgainstRealOpenSSH(t *testing.T) {
	port, err := strconv.Atoi(os.Getenv("AHA2_REAL_SSHD_PORT"))
	user := os.Getenv("AHA2_REAL_SSHD_USER")
	key := os.Getenv("AHA2_REAL_SSHD_KEY")
	hosts := os.Getenv("AHA2_REAL_SSHD_KNOWN_HOSTS")
	if err != nil || port == 0 || user == "" || key == "" || hosts == "" {
		t.Skip("requires a local OpenSSH test server configured via AHA2_REAL_SSHD_*")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "workspace with 'quote")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &SSHRunner{Host: "127.0.0.1", Port: port, User: user, Auth: "key",
		KnownHostsPaths: []string{hosts}, PrivateKeyPaths: []string{key}}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	terminal, err := runner.StartTerminal(ctx, root, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if err := terminal.Resize(111, 41); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("printf 'SSH''-PTY:%s:%s\\n' \"$(pwd -P)\" \"$TERM\"; stty size; exit\n")); err != nil {
		t.Fatal(err)
	}
	output := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(terminal)
		output <- string(data)
	}()
	select {
	case data := <-output:
		for _, want := range []string{"SSH-PTY:" + root + ":xterm-256color", "41 111"} {
			if !strings.Contains(data, want) {
				t.Fatalf("OpenSSH PTY output missing %q: %q", want, data)
			}
		}
	case <-ctx.Done():
		t.Fatal("OpenSSH PTY did not output and exit")
	}
}

func TestWorkspaceTerminalSSHRequiresAbsoluteRoot(t *testing.T) {
	for _, test := range []struct{ platform, root string }{
		{"linux", "checkout"}, {"linux", "C:\\path"}, {"windows/amd64", "C:checkout"},
		{"windows", "/checkout"}, {"linux", "/root\x00checkout"},
	} {
		if _, err := sshTerminalCommand(test.platform, test.root); err == nil {
			t.Errorf("accepted invalid %s workspace directory %q", test.platform, test.root)
		}
	}
}
