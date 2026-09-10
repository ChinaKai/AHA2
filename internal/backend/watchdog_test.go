package backend

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBackendErrorsAndUnknownOutputDoNotResetIdleTimeout(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	activity := make(chan string, 1)
	done := make(chan struct{})
	var mu sync.Mutex
	events := []string{}
	go monitorBackendActivity(ctx, 10*time.Millisecond, 45*time.Millisecond, 10*time.Millisecond, activity, func(event Event) {
		mu.Lock()
		events = append(events, event.Type)
		mu.Unlock()
	}, cancel, done)

	ticker := time.NewTicker(3 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(500 * time.Millisecond)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			for _, eventType := range []string{"agent_error", ""} {
				select {
				case activity <- eventType:
				default:
				}
			}
		case <-done:
			if !errors.Is(context.Cause(ctx), ErrBackendIdleTimeout) {
				t.Fatalf("watchdog cause = %v", context.Cause(ctx))
			}
			mu.Lock()
			defer mu.Unlock()
			stalled, timedOut, resumed := false, false, false
			for _, eventType := range events {
				switch eventType {
				case "agent_stalled":
					stalled = true
				case "agent_idle_timeout":
					timedOut = true
				case "agent_resumed":
					resumed = true
				}
			}
			if !stalled || !timedOut || resumed {
				t.Fatalf("watchdog events = %v", events)
			}
			return
		case <-timeout.C:
			t.Fatal("repeated backend errors kept the watchdog alive")
		}
	}
}

func TestMeaningfulBackendActivityResetsIdleTimeout(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	activity := make(chan string, 1)
	done := make(chan struct{})
	events := make(chan Event, 8)
	go monitorBackendActivity(ctx, 10*time.Millisecond, 80*time.Millisecond, 10*time.Millisecond, activity, func(event Event) {
		events <- event
	}, cancel, done)

	deadline := time.NewTimer(500 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.Type != "agent_stalled" {
				continue
			}
			activity <- "agent_message"
			select {
			case resumed := <-events:
				if resumed.Type != "agent_resumed" {
					t.Fatalf("event after activity = %s", resumed.Type)
				}
				cancel(context.Canceled)
				<-done
				return
			case <-deadline.C:
				t.Fatal("meaningful activity did not resume the watchdog")
			}
		case <-deadline.C:
			t.Fatal("watchdog did not report a stall")
		}
	}
}
