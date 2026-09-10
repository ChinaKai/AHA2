package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/gateway"
	"github.com/ChinaKai/AHA2/internal/store"
)

func newModelDetectionHTTPTest(t *testing.T, jobs *modelDetectionJobs) (*httptest.Server, *http.Client, string, domain.Provider) {
	t.Helper()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := domain.Provider{
		ID: "provider-job-test", Name: "Provider Job Test", BaseURL: "https://provider.invalid/v1", AuthStyle: "bearer",
		CredentialRef: "provider/provider-job-test/credential", CredentialConfigured: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := database.UpsertProvider(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	secretStore := &fakeSecretStore{values: map[string]string{provider.CredentialRef: "model-job-secret"}}
	apiServer := New(Config{Store: database, Auth: auth.NewService(database, "setup-test", time.Hour), Secrets: secretStore})
	apiServer.modelDetectionJobs = jobs
	server := httptest.NewServer(apiServer.Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	return server, client, csrf, provider
}

func postModelDetectionJob(t *testing.T, client *http.Client, url, csrf string) modelDetectionJobView {
	t.Helper()
	response := requestJSON(t, client, http.MethodPost, url, nil, csrf)
	var payload struct {
		Job modelDetectionJobView `json:"job"`
	}
	decodeResponse(t, response, &payload)
	if response.StatusCode != http.StatusAccepted || payload.Job.ID == "" {
		t.Fatalf("create job status=%d payload=%#v", response.StatusCode, payload)
	}
	return payload.Job
}

func readModelDetectionEvents(t *testing.T, client *http.Client, url string, after string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if after != "" {
		request.Header.Set("Last-Event-ID", after)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events status=%d content-type=%q body=%s", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	return string(body)
}

func TestModelDetectionHTTPJobSSEAndCancelLifecycle(t *testing.T) {
	jobs := newModelDetectionJobs()
	models := make([]gateway.DetectedModel, 9)
	for index := range models {
		models[index] = gateway.DetectedModel{ID: string(rune('a' + index))}
	}
	jobs.detect = func(_ context.Context, _ domain.Provider, apiKey string) (gateway.Result, error) {
		if apiKey != "model-job-secret" {
			t.Errorf("provider credential was not supplied to transient detector")
		}
		return gateway.Result{AuthStyle: "bearer", Models: models}, nil
	}
	var active atomic.Int32
	var maximum atomic.Int32
	jobs.probe = func(context.Context, domain.Provider, string, string, string) (map[string]string, string) {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(8 * time.Millisecond)
		active.Add(-1)
		return map[string]string{"responses": "supported"}, ""
	}
	server, client, csrf, provider := newModelDetectionHTTPTest(t, jobs)
	baseURL := server.URL + "/api/v1/providers/" + provider.ID + "/model-detection-jobs"
	created := postModelDetectionJob(t, client, baseURL, csrf)
	eventsURL := baseURL + "/" + created.ID + "/events"
	stream := readModelDetectionEvents(t, client, eventsURL, "")
	catalog := strings.Index(stream, "event: catalog")
	result := strings.Index(stream, "event: result")
	done := strings.Index(stream, "event: done")
	if catalog < 0 || result <= catalog || done <= result {
		t.Fatalf("incremental event order is invalid: %s", stream)
	}
	if got := strings.Count(stream, "event: result"); got != len(models) {
		t.Fatalf("result events=%d want=%d", got, len(models))
	}
	if got := strings.Count(stream, "event: progress"); got < len(models)+1 {
		t.Fatalf("progress events=%d want at least %d", got, len(models)+1)
	}
	if maximum.Load() > modelDetectionWorkers || maximum.Load() < 2 {
		t.Fatalf("worker concurrency=%d bound=%d", maximum.Load(), modelDetectionWorkers)
	}
	if strings.Contains(stream, "model-job-secret") {
		t.Fatal("credential leaked into model detection events")
	}
	replayed := readModelDetectionEvents(t, client, eventsURL, "2")
	if strings.Contains(replayed, "event: catalog") || !strings.Contains(replayed, "event: done") {
		t.Fatalf("Last-Event-ID replay is invalid: %s", replayed)
	}

	jobs.detect = func(context.Context, domain.Provider, string) (gateway.Result, error) {
		return gateway.Result{AuthStyle: "bearer", Models: []gateway.DetectedModel{{ID: "fast"}, {ID: "blocked-a"}, {ID: "blocked-b"}, {ID: "blocked-c"}}}, nil
	}
	blockedStarted := make(chan struct{})
	cancelObserved := make(chan struct{})
	var startOnce sync.Once
	var cancelOnce sync.Once
	jobs.probe = func(ctx context.Context, _ domain.Provider, _, _, modelID string) (map[string]string, string) {
		if modelID == "fast" {
			return map[string]string{"responses": "supported"}, ""
		}
		startOnce.Do(func() { close(blockedStarted) })
		<-ctx.Done()
		cancelOnce.Do(func() { close(cancelObserved) })
		return nil, ""
	}
	cancelJob := postModelDetectionJob(t, client, baseURL, csrf)
	select {
	case <-blockedStarted:
	case <-time.After(time.Second):
		t.Fatal("blocking capability probes did not start")
	}
	job, err := jobs.get("", provider.ID, cancelJob.ID)
	if !errors.Is(err, errModelDetectionJobNotFound) {
		t.Fatalf("job accepted an empty owner binding: %v", err)
	}
	jobs.mu.RLock()
	job = jobs.jobs[cancelJob.ID]
	jobs.mu.RUnlock()
	deadline := time.Now().Add(time.Second)
	for job.view().Completed != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	response := requestJSON(t, client, http.MethodPost, baseURL+"/"+cancelJob.ID+"/cancel", nil, csrf)
	var cancelPayload struct {
		Job modelDetectionJobView `json:"job"`
	}
	decodeResponse(t, response, &cancelPayload)
	if response.StatusCode != http.StatusOK || (cancelPayload.Job.Status != "cancelling" && cancelPayload.Job.Status != "cancelled") {
		t.Fatalf("cancel status=%d payload=%#v", response.StatusCode, cancelPayload)
	}
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("cancel endpoint did not reach capability request context")
	}
	cancelStream := readModelDetectionEvents(t, client, baseURL+"/"+cancelJob.ID+"/events", "")
	if strings.Count(cancelStream, "event: result") != 1 || !strings.Contains(cancelStream, `"status":"cancelled"`) {
		t.Fatalf("cancelled stream did not retain exactly the completed result: %s", cancelStream)
	}
	encoded, _ := json.Marshal(cancelPayload.Job)
	if strings.Contains(string(encoded), "model-job-secret") {
		t.Fatalf("credential leaked into cancel response: %s", encoded)
	}
}

func TestModelDetectionJobErrorAndTTLExpiry(t *testing.T) {
	jobs := newModelDetectionJobs()
	jobs.ttl = 50 * time.Millisecond
	jobs.detect = func(context.Context, domain.Provider, string) (gateway.Result, error) {
		return gateway.Result{}, errors.New("catalog unavailable")
	}
	created := jobs.start("owner", domain.Provider{ID: "provider"}, "transient-secret")
	job, err := jobs.get("owner", "provider", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitModelDetectionStatus(t, job, "failed")
	events, terminal, _ := job.eventsAfter(0)
	if !terminal || len(events) != 2 || events[0].Type != "error" || events[1].Type != "done" {
		t.Fatalf("failure events=%#v terminal=%t", events, terminal)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := jobs.get("owner", "provider", created.ID); errors.Is(err, errModelDetectionJobNotFound) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("terminal model detection job was not removed after TTL")
}
