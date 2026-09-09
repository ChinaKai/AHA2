package app

import (
	"context"
	"testing"
	"time"
)

func TestShutdownCancelsAndWaitsForActiveTurnWorkers(t *testing.T) {
	t.Parallel()
	service := NewService(nil, nil, nil)
	workerContext, cancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	service.mu.Lock()
	service.cancels["turn-shutdown"] = cancel
	service.runWG.Add(1)
	service.mu.Unlock()
	go func() {
		defer service.runWG.Done()
		<-workerContext.Done()
		close(workerDone)
	}()
	shutdownContext, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := service.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-workerDone:
	default:
		t.Fatal("shutdown returned before the active worker exited")
	}
}
