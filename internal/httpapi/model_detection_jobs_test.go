package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestModelDetectionJobStreamsCatalogWithoutProbingModels(t *testing.T) {
	t.Parallel()
	jobs := newModelDetectionJobs()
	jobs.ttl = time.Hour
	jobs.detect = func(context.Context, domain.Provider, string) (gateway.Result, error) {
		return gateway.Result{AuthStyle: "bearer", Models: []gateway.DetectedModel{{ID: "one"}, {ID: "two"}, {ID: "three"}}}, nil
	}
	jobs.probe = func(context.Context, domain.Provider, string, string, string) (map[string]string, string, map[string]string) {
		t.Fatal("model detection job must not probe capabilities")
		return nil, "", nil
	}
	provider := domain.Provider{ID: "provider-stream", BaseURL: "https://example.invalid"}
	created := jobs.start("owner-a", provider, "secret")
	job, err := jobs.get("owner-a", provider.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	view := waitModelDetectionStatus(t, job, "completed")
	if view.Total != 3 || view.Completed != 0 || len(view.Results) != 0 {
		t.Fatalf("completed job = %#v", view)
	}
	events, terminal, _ := job.eventsAfter(0)
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Type]++
	}
	if !terminal || counts["catalog"] != 1 || counts["result"] != 0 || counts["done"] != 1 {
		t.Fatalf("stream events = %#v terminal=%t", counts, terminal)
	}
	if _, err := jobs.get("owner-b", provider.ID, created.ID); !errors.Is(err, errModelDetectionJobNotFound) {
		t.Fatalf("cross-owner lookup err = %v", err)
	}
	if _, err := jobs.get("owner-a", "other-provider", created.ID); !errors.Is(err, errModelDetectionJobNotFound) {
		t.Fatalf("cross-provider lookup err = %v", err)
	}
}

func TestModelDetectionJobCancellationStopsCatalogFetch(t *testing.T) {
	t.Parallel()
	jobs := newModelDetectionJobs()
	jobs.ttl = time.Hour
	blocked := make(chan struct{})
	cancelled := make(chan struct{})
	jobs.detect = func(ctx context.Context, _ domain.Provider, _ string) (gateway.Result, error) {
		close(blocked)
		<-ctx.Done()
		close(cancelled)
		return gateway.Result{}, ctx.Err()
	}
	provider := domain.Provider{ID: "provider-cancel", BaseURL: "https://example.invalid"}
	created := jobs.start("owner-a", provider, "secret")
	job, err := jobs.get("owner-a", provider.ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("catalog fetch did not start")
	}
	if _, err := jobs.cancel("owner-a", provider.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("catalog fetch did not receive cancellation")
	}
	view := waitModelDetectionStatus(t, job, "cancelled")
	if view.Completed != 0 || len(view.Results) != 0 {
		t.Fatalf("cancelled catalog job retained results: %#v", view)
	}
}

func TestModelDetectionProbeUsesProviderAnthropicBaseURL(t *testing.T) {
	t.Parallel()
	var baseAnthropicHits int32
	base := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/responses" || request.URL.Path == "/chat/completions":
			writer.WriteHeader(http.StatusOK)
		case strings.HasSuffix(request.URL.Path, "/messages"):
			atomic.AddInt32(&baseAnthropicHits, 1)
			http.NotFound(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(base.Close)
	var anthropicPaths []string
	var mu sync.Mutex
	anthropic := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		anthropicPaths = append(anthropicPaths, request.URL.Path)
		mu.Unlock()
		if request.URL.Path == "/apps/anthropic/v1/messages" {
			writer.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(writer, request)
	}))
	t.Cleanup(anthropic.Close)

	jobs := newModelDetectionJobs()
	provider := domain.Provider{
		ID:               "provider-ali",
		BaseURL:          base.URL + "/compatible-mode/v1",
		AnthropicBaseURL: anthropic.URL + "/apps/anthropic",
		AuthStyle:        "auto",
	}
	capabilities, anthropicBase, protocolErrors := jobs.probe(context.Background(), provider, "secret", "", "claude-model")
	if capabilities["anthropic_messages"] != "supported" {
		t.Fatalf("capabilities=%v protocolErrors=%v", capabilities, protocolErrors)
	}
	if anthropicBase != "" {
		t.Fatalf("anthropicBase=%q, want empty for the configured base", anthropicBase)
	}
	mu.Lock()
	paths := append([]string(nil), anthropicPaths...)
	mu.Unlock()
	sawMessages := false
	for _, path := range paths {
		if path == "/apps/anthropic/v1/messages" {
			sawMessages = true
		}
	}
	if !sawMessages {
		t.Fatalf("configured anthropic_base_url was not probed: %v", paths)
	}
	if atomic.LoadInt32(&baseAnthropicHits) != 0 {
		t.Fatalf("base_url was probed for anthropic_messages despite provider.anthropic_base_url")
	}
}

func TestModelDetectionProbeReportsFailedCandidateEvidence(t *testing.T) {
	t.Parallel()
	notFound := func() *httptest.Server {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/responses" || request.URL.Path == "/chat/completions" ||
				request.URL.Path == "/v1/responses" || request.URL.Path == "/v1/chat/completions" {
				writer.WriteHeader(http.StatusOK)
				return
			}
			http.NotFound(writer, request)
		}))
		t.Cleanup(server.Close)
		return server
	}
	base := notFound()
	anthropic := notFound()
	jobs := newModelDetectionJobs()
	provider := domain.Provider{
		ID:               "provider-ali",
		BaseURL:          base.URL + "/v1",
		AnthropicBaseURL: anthropic.URL + "/apps/anthropic",
		AuthStyle:        "auto",
	}
	capabilities, _, protocolErrors := jobs.probe(context.Background(), provider, "secret", "", "claude-model")
	if capabilities["anthropic_messages"] != "unsupported" {
		t.Fatalf("capabilities=%v protocolErrors=%v", capabilities, protocolErrors)
	}
	evidence := protocolErrors["anthropic_messages"]
	for _, want := range []string{
		anthropic.URL + "/apps/anthropic: HTTP 404",
		base.URL + "/v1: HTTP 404",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("protocol_errors missing %q: %q", want, evidence)
		}
	}
	if strings.Contains(evidence, "secret") {
		t.Fatalf("protocol_errors leaked the API key: %q", evidence)
	}
}
