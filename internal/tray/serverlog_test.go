package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerLogPathIsUnderDataDirLogs(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "AHA data")
	want := filepath.Join(dataDir, "logs", "aha2-server.log")
	if got := ServerLogPath(dataDir); got != want {
		t.Fatalf("ServerLogPath=%q want %q", got, want)
	}
}

func TestRotatingLogKeepsOneBackupAndOnlyTwoFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "aha2-server.log")
	log, err := openServerLog(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	if _, err := log.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Write([]byte("abcdefgh")); err != nil {
		t.Fatal(err)
	}
	if got := readLogFile(t, path); got != "abcdefgh" {
		t.Fatalf("live log=%q", got)
	}
	if got := readLogFile(t, path+".1"); got != "12345678" {
		t.Fatalf("backup log=%q", got)
	}

	if _, err := log.Write([]byte("ABCDEFGH")); err != nil {
		t.Fatal(err)
	}
	if got := readLogFile(t, path); got != "ABCDEFGH" {
		t.Fatalf("live log after second rotation=%q", got)
	}
	if got := readLogFile(t, path+".1"); got != "abcdefgh" {
		t.Fatalf("backup log after second rotation=%q", got)
	}
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third log file: %v", err)
	}
}

func TestRotatingLogAppendsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "aha2-server.log")
	first, err := openServerLog(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := openServerLog(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	second.writeEntry("second")
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}

	content := readLogFile(t, path)
	if !strings.HasPrefix(content, "first\n") {
		t.Fatalf("existing content was not preserved: %q", content)
	}
	if !strings.Contains(content, "second\n") {
		t.Fatalf("entry was not appended: %q", content)
	}
}

func TestFormatCommandKeepsDiagnosticsButNoEnvironment(t *testing.T) {
	command := Command{
		Path: `C:\Program Files\AHA2\aha2.exe`,
		Args: []string{"serve", "--listen", "127.0.0.1:8766", "--data-dir", `D:\ProgramData\AHA2`},
	}
	got := formatCommand(command)
	for _, want := range []string{`"C:\Program Files\AHA2\aha2.exe"`, "serve", "--listen", "127.0.0.1:8766", `--data-dir D:\ProgramData\AHA2`} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatCommand=%q missing %q", got, want)
		}
	}
}

func readLogFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
