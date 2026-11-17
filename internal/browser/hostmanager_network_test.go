package browser

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// TestHostManagerRetriesAnOccupiedPort occupies a real loopback port and makes
// the first reservation hand it to the host. Binding fails exactly like a port
// conflict in production; the manager must move to the next freshly reserved
// port instead of failing the whole launch.
func TestHostManagerRetriesAnOccupiedPort(t *testing.T) {
	occupiedListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupiedListener.Close()
	occupied := occupiedListener.Addr().(*net.TCPAddr).Port

	var mu sync.Mutex
	reservations := 0
	recorder := &launchRecorder{}
	manager, _ := managerFixture(t, recorder, func(options *HostManagerOptions) {
		options.ReservePort = func() (int, error) {
			mu.Lock()
			defer mu.Unlock()
			reservations++
			if reservations == 1 {
				return occupied, nil
			}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				return 0, err
			}
			port := listener.Addr().(*net.TCPAddr).Port
			listener.Close()
			return port, nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("start with an occupied first port: %v", err)
	}
	specs, _ := recorder.snapshot()
	if len(specs) != 2 || specs[0].Port != occupied || specs[1].Port == occupied {
		t.Fatalf("launches=%#v, want the occupied port retried once", specs)
	}
	if err := manager.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

// TestHostManagerFailsClosedWhenTheHostRejectsTheToken keeps the token check
// real at the HTTP layer: the child only answers health for a different token.
// The manager must give up after its launch attempts and expose the typed
// failure instead of pretending the host is healthy.
func TestHostManagerFailsClosedWhenTheHostRejectsTheToken(t *testing.T) {
	recorder := &launchRecorder{rejectToken: true}
	manager, _ := managerFixture(t, recorder, func(options *HostManagerOptions) {
		options.ReadyTimeout = 150 * time.Millisecond
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	err := manager.Start(ctx)
	if Code(err) != "browser_host_start_failed" {
		t.Fatalf("start error=%v, want browser_host_start_failed", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("token rejection burned the readiness timeout: %v", elapsed)
	}
	if specs, _ := recorder.snapshot(); len(specs) != 3 {
		t.Fatalf("launch attempts=%d, want PortAttempts", len(specs))
	}
	if _, err := manager.Health(ctx); Code(err) != "browser_host_start_failed" {
		t.Fatalf("health after failed start=%v", err)
	}
}
