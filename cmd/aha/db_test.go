package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/instancelock"
	"github.com/ChinaKai/AHA2/internal/store"
)

func TestParseCommandRoutesDBMaintenance(t *testing.T) {
	command, args, err := parseCommand([]string{"db", "compact", "--data-dir", "data", "--json"})
	if err != nil || command != "db" || strings.Join(args, "\x00") != "compact\x00--data-dir\x00data\x00--json" {
		t.Fatalf("parseCommand = %q %q %v", command, args, err)
	}
}

func TestDBCompactJSONReportsSuccess(t *testing.T) {
	dataDir := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := dbCommand([]string{"compact", "--data-dir", dataDir, "--json"}, &stdout, &stderr)
	if code != dbExitSuccess {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var result domain.OfflineCompactionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not the machine-readable result: %v\n%s", err, stdout.String())
	}
	if result.Status != domain.OfflineCompactionStatusOK || result.BackupPath == "" || !result.IntegrityOK || !result.RowCountsOK {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(result.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	// Progress belongs on stderr when stdout is the JSON channel.
	if !strings.Contains(stderr.String(), "VACUUM") {
		t.Fatalf("progress missing from stderr: %s", stderr.String())
	}
}

func TestDBCompactRefusesWhileServiceLockIsHeld(t *testing.T) {
	dataDir := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(dataDir, "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	lock, err := instancelock.Acquire(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	var stdout, stderr bytes.Buffer
	code := dbCommand([]string{"compact", "--data-dir", dataDir, "--json"}, &stdout, &stderr)
	if code != dbExitRefused {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	var result domain.OfflineCompactionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.OfflineCompactionStatusRefused || result.Reason != domain.OfflineCompactionReasonServiceRunning {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "backups")); !os.IsNotExist(err) {
		t.Fatalf("refusal created a backup directory: %v", err)
	}
}

func TestDBCompactUsageAndMissingDatabaseExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := dbCommand(nil, &stdout, &stderr); code != dbExitUsage {
		t.Fatalf("no subcommand exit=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := dbCommand([]string{"compact", "--unknown"}, &stdout, &stderr); code != dbExitUsage {
		t.Fatalf("bad flag exit=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := dbCommand([]string{"compact", "--data-dir", t.TempDir(), "--json"}, &stdout, &stderr); code != dbExitFailure {
		t.Fatalf("missing database exit=%d stderr=%s", code, stderr.String())
	}
	var result domain.OfflineCompactionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.OfflineCompactionStatusFailed || result.Reason != domain.OfflineCompactionReasonDatabaseMissing {
		t.Fatalf("result=%#v", result)
	}
}
