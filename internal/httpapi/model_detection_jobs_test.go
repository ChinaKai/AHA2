package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/gateway"
)

func waitModelDetectionStatus(t *testing.T, job *modelDetectionJob, terminal ...string) modelDetectionJobView {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		view := job.view()
		for _, status := range terminal {
			if view.Status == status {
				return view
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("model detection did not reach %v; current=%#v", terminal, job.view())
	return modelDetectionJobView{}
}

func TestModelDetectionJobStreamsCatalogAndPerModelResults(t *testing.T) {
	t.Parallel()
	jobs := newModelDetectionJobs()
	jobs.ttl = time.Hour
	jobs.workers = 2
	jobs.detect = func(context.Context, domain.Provider, string) (gateway.Result, error) {
		return gateway.Result{AuthStyle: "bearer", Models: []gateway.DetectedModel{{ID: "one"}, {ID: "two"}, {ID: "three"}}}, nil
	}
	jobs.probe = func(_ context.Context, _ domain.Provider, _, _, modelID string) (map[string]string, string) {
		time.Sleep(5 * time.Millisecond)
		return map[string]string{"responses": "supported", "model": modelID}, ""
	}
	provider := domain.Provider{ID: "provider-stream", BaseURL: "https://example.invalid"}
	created := jobs.start("owner-a", provider, "secret")
	job, err := jobs.get("owner-a", provider.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := waitModelDetectionStatus(t, job, "completed")
	if view.Total != 3 || view.Completed != 3 || len(view.Results) != 3 {
		t.Fatalf("completed job = %#v", view)
	}
	events, terminal, _ := job.eventsAfter(0)
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Type]++
	}
	if !terminal || counts["catalog"] != 1 || counts["result"] != 3 || counts["done"] != 1 {
		t.Fatalf("stream events = %#v terminal=%t", counts, terminal)
	}
	if _, err := jobs.get("owner-b", provider.ID, created.ID); !errors.Is(err, errModelDetectionJobNotFound) {
		t.Fatalf("cross-owner lookup err = %v", err)
	}
	if _, err := jobs.get("owner-a", "other-provider", created.ID); !errors.Is(err, errModelDetectionJobNotFound) {
		t.Fatalf("cross-provider lookup err = %v", err)
	}
}

func TestModelDetectionJobCancellationStopsWorkersAndKeepsResults(t *testing.T) {
	t.Parallel()
	jobs := newModelDetectionJobs()
	jobs.ttl = time.Hour
	jobs.workers = 1
	jobs.detect = func(context.Context, domain.Provider, string) (gateway.Result, error) {
		return gateway.Result{AuthStyle: "none", Models: []gateway.DetectedModel{{ID: "fast"}, {ID: "blocked"}}}, nil
	}
	firstDone := make(chan struct{})
	jobs.probe = func(ctx context.Context, _ domain.Provider, _, _, modelID string) (map[string]string, string) {
		if modelID == "fast" {
			close(firstDone)
			return map[string]string{"responses": "supported"}, ""
		}
		<-ctx.Done()
		return nil, ""
	}
	provider := domain.Provider{ID: "provider-cancel", BaseURL: "https://example.invalid"}
	created := jobs.start("owner-a", provider, "secret")
	job, err := jobs.get("owner-a", provider.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first model was not probed")
	}
	deadline := time.Now().Add(time.Second)
	for job.view().Completed == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := jobs.cancel("owner-a", provider.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	view := waitModelDetectionStatus(t, job, "cancelled")
	if view.Completed != 1 || len(view.Results) != 1 || view.Results[0].ID != "fast" {
		t.Fatalf("cancelled job lost completed results: %#v", view)
	}
}
