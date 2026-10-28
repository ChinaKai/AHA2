package channel

import (
	"testing"
	"time"
)

func TestChannelHeartbeatStale(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name     string
		started  time.Time
		lastSeen time.Time
		want     bool
	}{
		{name: "no process", started: time.Time{}, lastSeen: time.Time{}, want: false},
		{name: "startup grace", started: now.Add(-10 * time.Second), lastSeen: time.Time{}, want: false},
		{name: "never reported", started: now.Add(-4 * time.Minute), lastSeen: time.Time{}, want: true},
		{name: "reported before start", started: now.Add(-4 * time.Minute), lastSeen: now.Add(-5 * time.Minute), want: true},
		{name: "recent heartbeat", started: now.Add(-4 * time.Minute), lastSeen: now.Add(-10 * time.Second), want: false},
		{name: "stale heartbeat", started: now.Add(-5 * time.Minute), lastSeen: now.Add(-2 * time.Minute), want: true},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := channelHeartbeatStale(testCase.started, testCase.lastSeen, now); got != testCase.want {
				t.Fatalf("channelHeartbeatStale(%v, %v, %v)=%t want %t", testCase.started, testCase.lastSeen, now, got, testCase.want)
			}
		})
	}
}

func TestChannelHeartbeatSupported(t *testing.T) {
	t.Parallel()
	if channelHeartbeatSupported(nil) {
		t.Fatal("nil config reported heartbeat support")
	}
	if channelHeartbeatSupported(map[string]any{"runtime_heartbeat_supported": "0"}) {
		t.Fatal("disabled heartbeat reported support")
	}
	if !channelHeartbeatSupported(map[string]any{"runtime_heartbeat_supported": "1"}) {
		t.Fatal("enabled heartbeat was not detected")
	}
}
