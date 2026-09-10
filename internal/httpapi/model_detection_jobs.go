package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/gateway"
)

const (
	modelDetectionWorkers   = 4
	modelDetectionJobTTL    = 10 * time.Minute
	modelDetectionHeartbeat = 15 * time.Second
)

var errModelDetectionJobNotFound = errors.New("model detection job not found")

type modelCatalogDetector func(context.Context, domain.Provider, string) (gateway.Result, error)
type modelCapabilityProber func(context.Context, domain.Provider, string, string, string) (map[string]string, string)

type modelDetectionEvent struct {
	ID   int64
	Type string
	Data json.RawMessage
}

type modelDetectionJobView struct {
	ID               string                  `json:"id"`
	ProviderID       string                  `json:"provider_id"`
	Status           string                  `json:"status"`
	AuthStyle        string                  `json:"auth_style,omitempty"`
	Total            int                     `json:"total"`
	Completed        int                     `json:"completed"`
	Models           []gateway.DetectedModel `json:"models,omitempty"`
	Results          []gateway.DetectedModel `json:"results,omitempty"`
	AnthropicBaseURL string                  `json:"anthropic_base_url,omitempty"`
	Error            string                  `json:"error,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
	FinishedAt       time.Time               `json:"finished_at,omitempty"`
	ExpiresAt        time.Time               `json:"expires_at,omitempty"`
}

type modelDetectionJob struct {
	mu               sync.Mutex
	id               string
	ownerID          string
	providerID       string
	status           string
	authStyle        string
	total            int
	completed        int
	models           []gateway.DetectedModel
	results          []gateway.DetectedModel
	anthropicBaseURL string
	errorMessage     string
	createdAt        time.Time
	updatedAt        time.Time
	finishedAt       time.Time
	expiresAt        time.Time
	ttl              time.Duration
	events           []modelDetectionEvent
	nextEventID      int64
	notify           chan struct{}
	ctx              context.Context
	cancel           context.CancelFunc
}

type modelDetectionJobs struct {
	mu      sync.RWMutex
	jobs    map[string]*modelDetectionJob
	ttl     time.Duration
	workers int
	detect  modelCatalogDetector
	probe   modelCapabilityProber
}

func newModelDetectionJobs() *modelDetectionJobs {
	return &modelDetectionJobs{
		jobs:    make(map[string]*modelDetectionJob),
		ttl:     modelDetectionJobTTL,
		workers: modelDetectionWorkers,
		detect: func(ctx context.Context, provider domain.Provider, apiKey string) (gateway.Result, error) {
			return gateway.DetectModelsContext(ctx, provider.BaseURL, apiKey, provider.AuthStyle, 15*time.Second)
		},
		probe: func(ctx context.Context, provider domain.Provider, apiKey, authStyle, modelID string) (map[string]string, string) {
			return gateway.ProbeModelCapabilitiesContext(ctx, provider.BaseURL, apiKey, authStyle, modelID, 8*time.Second)
		},
	}
}

func (m *modelDetectionJobs) start(ownerID string, provider domain.Provider, apiKey string) modelDetectionJobView {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	job := &modelDetectionJob{
		id: domain.NewID("model_detection_job"), ownerID: ownerID, providerID: provider.ID, status: "queued",
		createdAt: now, updatedAt: now, ttl: m.ttl, notify: make(chan struct{}), ctx: ctx, cancel: cancel,
	}
	m.mu.Lock()
	m.jobs[job.id] = job
	m.mu.Unlock()
	go m.run(job, provider, apiKey)
	return job.view()
}

func (m *modelDetectionJobs) run(job *modelDetectionJob, provider domain.Provider, apiKey string) {
	defer m.scheduleExpiry(job)
	result, err := m.detect(job.ctx, provider, apiKey)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(job.ctx.Err(), context.Canceled) {
			job.finish("cancelled", "")
			return
		}
		job.finish("failed", err.Error())
		return
	}
	if job.ctx.Err() != nil {
		job.finish("cancelled", "")
		return
	}
	if len(result.Models) == 0 {
		job.finish("failed", "no_models_found")
		return
	}
	job.setCatalog(result)

	workers := m.workers
	if workers <= 0 {
		workers = modelDetectionWorkers
	}
	if workers > len(result.Models) {
		workers = len(result.Models)
	}
	type probeResult struct {
		model         gateway.DetectedModel
		anthropicBase string
	}
	indices := make(chan int, len(result.Models))
	for index := range result.Models {
		indices <- index
	}
	close(indices)
	results := make(chan probeResult, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indices {
				if job.ctx.Err() != nil {
					return
				}
				model := result.Models[index]
				capabilities, anthropicBase := m.probe(job.ctx, provider, apiKey, result.AuthStyle, model.ID)
				if job.ctx.Err() != nil {
					return
				}
				model.Capabilities = capabilities
				select {
				case results <- probeResult{model: model, anthropicBase: anthropicBase}:
				case <-job.ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	for result := range results {
		job.recordResult(result.model, result.anthropicBase)
	}
	if job.ctx.Err() != nil {
		job.finish("cancelled", "")
		return
	}
	job.finish("completed", "")
}

func (m *modelDetectionJobs) get(ownerID, providerID, jobID string) (*modelDetectionJob, error) {
	m.mu.RLock()
	job := m.jobs[jobID]
	m.mu.RUnlock()
	if job == nil || job.ownerID != ownerID || job.providerID != providerID {
		return nil, errModelDetectionJobNotFound
	}
	return job, nil
}

func (m *modelDetectionJobs) cancel(ownerID, providerID, jobID string) (modelDetectionJobView, error) {
	job, err := m.get(ownerID, providerID, jobID)
	if err != nil {
		return modelDetectionJobView{}, err
	}
	job.requestCancel()
	return job.view(), nil
}

func (m *modelDetectionJobs) scheduleExpiry(job *modelDetectionJob) {
	job.mu.Lock()
	if job.finishedAt.IsZero() {
		job.mu.Unlock()
		return
	}
	expiresAt := job.expiresAt
	job.mu.Unlock()
	time.AfterFunc(time.Until(expiresAt), func() {
		m.mu.Lock()
		if current := m.jobs[job.id]; current == job {
			current.mu.Lock()
			expired := !current.finishedAt.IsZero() && !time.Now().Before(current.expiresAt)
			current.mu.Unlock()
			if expired {
				delete(m.jobs, job.id)
			}
		}
		m.mu.Unlock()
	})
}

func (j *modelDetectionJob) appendEventLocked(eventType string, payload any) {
	data, _ := json.Marshal(payload)
	j.nextEventID++
	j.events = append(j.events, modelDetectionEvent{ID: j.nextEventID, Type: eventType, Data: data})
	close(j.notify)
	j.notify = make(chan struct{})
}

func (j *modelDetectionJob) setCatalog(result gateway.Result) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = "running"
	j.authStyle = result.AuthStyle
	j.models = append([]gateway.DetectedModel(nil), result.Models...)
	j.total = len(result.Models)
	j.updatedAt = time.Now().UTC()
	j.appendEventLocked("catalog", map[string]any{"provider_id": j.providerID, "auth_style": j.authStyle, "models": j.models, "total": j.total})
	j.appendEventLocked("progress", map[string]any{"status": j.status, "completed": j.completed, "total": j.total})
}

func (j *modelDetectionJob) recordResult(model gateway.DetectedModel, anthropicBase string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.terminalLocked() {
		return
	}
	j.results = append(j.results, model)
	j.completed++
	if j.anthropicBaseURL == "" && anthropicBase != "" {
		j.anthropicBaseURL = anthropicBase
	}
	j.updatedAt = time.Now().UTC()
	j.appendEventLocked("result", map[string]any{"model": model, "completed": j.completed, "total": j.total, "anthropic_base_url": anthropicBase})
	j.appendEventLocked("progress", map[string]any{"status": j.status, "completed": j.completed, "total": j.total})
}

func (j *modelDetectionJob) requestCancel() {
	j.mu.Lock()
	if j.terminalLocked() || j.status == "cancelling" {
		j.mu.Unlock()
		return
	}
	j.status = "cancelling"
	j.updatedAt = time.Now().UTC()
	j.appendEventLocked("progress", map[string]any{"status": j.status, "completed": j.completed, "total": j.total})
	cancel := j.cancel
	j.mu.Unlock()
	cancel()
}

func (j *modelDetectionJob) finish(status, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.terminalLocked() {
		return
	}
	now := time.Now().UTC()
	j.status = status
	j.errorMessage = message
	j.updatedAt = now
	j.finishedAt = now
	j.expiresAt = now.Add(j.ttl)
	if message != "" {
		code := "detect_models_failed"
		if message == "no_models_found" {
			code = message
		}
		j.appendEventLocked("error", map[string]any{"error": code, "message": message})
	}
	j.appendEventLocked("done", j.viewLocked())
}

func (j *modelDetectionJob) terminalLocked() bool {
	return j.status == "completed" || j.status == "cancelled" || j.status == "failed"
}

func (j *modelDetectionJob) view() modelDetectionJobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.viewLocked()
}

func (j *modelDetectionJob) viewLocked() modelDetectionJobView {
	return modelDetectionJobView{
		ID: j.id, ProviderID: j.providerID, Status: j.status, AuthStyle: j.authStyle,
		Total: j.total, Completed: j.completed,
		Models: append([]gateway.DetectedModel(nil), j.models...), Results: append([]gateway.DetectedModel(nil), j.results...),
		AnthropicBaseURL: j.anthropicBaseURL, Error: j.errorMessage,
		CreatedAt: j.createdAt, UpdatedAt: j.updatedAt, FinishedAt: j.finishedAt, ExpiresAt: j.expiresAt,
	}
}

func (j *modelDetectionJob) eventsAfter(after int64) ([]modelDetectionEvent, bool, <-chan struct{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	index := 0
	for index < len(j.events) && j.events[index].ID <= after {
		index++
	}
	events := append([]modelDetectionEvent(nil), j.events[index:]...)
	return events, j.terminalLocked(), j.notify
}

func (s *Server) createModelDetectionJob(writer http.ResponseWriter, request *http.Request) {
	providerID := strings.TrimSpace(request.PathValue("id"))
	provider, err := s.store.Provider(request.Context(), providerID)
	if err != nil {
		writeError(writer, http.StatusNotFound, "provider_not_found")
		return
	}
	session, _ := sessionFromContext(request.Context())
	apiKey := ""
	if provider.CredentialRef != "" && s.secrets != nil {
		apiKey, _ = s.secrets.Get(provider.CredentialRef)
	}
	view := s.modelDetectionJobs.start(session.OwnerID, provider, apiKey)
	s.audit(request, "provider.model_detection.create", "provider", provider.ID, map[string]any{"job_id": view.ID})
	writeJSON(writer, http.StatusAccepted, map[string]any{"ok": true, "job": view})
}

func (s *Server) cancelModelDetectionJob(writer http.ResponseWriter, request *http.Request) {
	session, _ := sessionFromContext(request.Context())
	view, err := s.modelDetectionJobs.cancel(session.OwnerID, request.PathValue("id"), request.PathValue("job"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "model_detection_job_not_found")
		return
	}
	s.audit(request, "provider.model_detection.cancel", "provider", view.ProviderID, map[string]any{"job_id": view.ID})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "job": view})
}

func (s *Server) modelDetectionJobEvents(writer http.ResponseWriter, request *http.Request) {
	session, _ := sessionFromContext(request.Context())
	job, err := s.modelDetectionJobs.get(session.OwnerID, request.PathValue("id"), request.PathValue("job"))
	if err != nil {
		writeError(writer, http.StatusNotFound, "model_detection_job_not_found")
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	after, _ := strconv.ParseInt(strings.TrimSpace(request.Header.Get("Last-Event-ID")), 10, 64)
	if queryAfter, err := strconv.ParseInt(strings.TrimSpace(request.URL.Query().Get("after")), 10, 64); err == nil && queryAfter > after {
		after = queryAfter
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()
	heartbeat := time.NewTicker(modelDetectionHeartbeat)
	defer heartbeat.Stop()
	for {
		events, terminal, notify := job.eventsAfter(after)
		for _, event := range events {
			if _, err := fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data); err != nil {
				return
			}
			after = event.ID
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		if terminal {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-notify:
		case <-heartbeat.C:
			if _, err := fmt.Fprint(writer, "event: heartbeat\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
