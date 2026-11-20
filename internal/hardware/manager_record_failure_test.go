package hardware

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

type failingRecorder struct {
	mu   sync.Mutex
	fail bool
}

func (recorder *failingRecorder) AppendHardwareIO(context.Context, domain.HardwareIOEvent) (int64, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.fail {
		return 0, errors.New("disk full")
	}
	return 42, nil
}

func TestRecordFailureIsCountedLoggedAndShownOnStatus(t *testing.T) {
	t.Parallel()
	device, bridge := net.Pipe()
	defer device.Close()
	defer bridge.Close()
	recorder := &failingRecorder{fail: true}
	manager := NewManager(recorder)
	clock := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return clock }
	var logs bytes.Buffer
	manager.logger = slog.New(slog.NewTextHandler(&logs, nil))
	target := attachment{taskID: "task-1", hardwareID: "board"}
	key := attachmentKey("task-1", "board", "serial")
	live := make(chan LiveEvent, 8)
	session := &terminalSession{
		manager: manager, conn: device, transport: "serial", status: "running",
		attachments: map[string]attachment{key: target},
		subscribers: map[string]liveSubscriber{"s1": {attachment: target, events: live}},
	}

	session.record("rx", "boot\r\n", "runtime", []byte("boot\r\n"), nil)
	session.record("rx", "login: ", "runtime", []byte("login: "), nil)
	if manager.RecordFailures() != 2 {
		t.Fatalf("record failures = %d, want 2", manager.RecordFailures())
	}
	status := session.statusFor("task-1", "board", false)
	if !strings.Contains(status.RecordError, "disk full") {
		t.Fatalf("status record_error = %q", status.RecordError)
	}
	if count := strings.Count(logs.String(), "could not be stored"); count != 1 {
		t.Fatalf("log lines = %d, want 1 (rate limited):\n%s", count, logs.String())
	}
	// The live terminal keeps working even though nothing was stored.
	for _, want := range []string{"boot\r\n", "login: "} {
		select {
		case event := <-live:
			if string(event.Data) != want || event.Sequence != 0 {
				t.Fatalf("live event = %+v, want %q with sequence 0", event, want)
			}
		case <-time.After(time.Second):
			t.Fatal("live output was not delivered")
		}
	}

	clock = clock.Add(2 * time.Minute)
	session.record("rx", "again", "runtime", []byte("again"), nil)
	if count := strings.Count(logs.String(), "could not be stored"); count != 2 || !strings.Contains(logs.String(), "suppressed_since_last_log=1") {
		t.Fatalf("second log line missing or without suppressed count:\n%s", logs.String())
	}

	recorder.mu.Lock()
	recorder.fail = false
	recorder.mu.Unlock()
	session.record("rx", "ok", "runtime", []byte("ok"), nil)
	if status := session.statusFor("task-1", "board", false); status.RecordError != "" {
		t.Fatalf("record_error not cleared after a stored event: %q", status.RecordError)
	}
}
