package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestDesktopLookupFailureOnlyRevokesConfirmedInvalidTask(t *testing.T) {
	for _, scenario := range []string{"canceled", "deadline", "database", "missing", "terminal"} {
		t.Run(scenario, func(t *testing.T) {
			h := newStreamHarness(t)
			ctx := context.Background()
			taskID := h.taskID
			expectedStatus := http.StatusConflict
			switch scenario {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				expectedStatus = http.StatusGatewayTimeout
			case "database":
				h.store.Close()
				expectedStatus = http.StatusServiceUnavailable
			case "missing":
				taskID = "missing-task"
				h.manager.Stop(h.taskID, h.session.ID)
				status, err := h.manager.OpenWithMode(ctx, taskID, "new-desktop", "foreground", true)
				if err != nil {
					t.Fatal(err)
				}
				h.session = *status.Session
				expectedStatus = http.StatusNotFound
			case "terminal":
				task, err := h.store.Task(ctx, taskID)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC().Format(time.RFC3339Nano)
				if err := h.store.UpdateTaskStatus(ctx, taskID, task.Status, domain.TaskCompleted, now, now); err != nil {
					t.Fatal(err)
				}
				expectedStatus = http.StatusForbidden
			}
			request := httptest.NewRequest(http.MethodGet, "/desktop", nil).WithContext(ctx)
			request.SetPathValue("id", taskID)
			recorder := httptest.NewRecorder()
			if _, _, ok := h.api.desktopTarget(recorder, request); ok {
				t.Fatal("invalid lookup authorized request")
			}
			if recorder.Code != expectedStatus {
				t.Fatalf("status %d, want %d: %s", recorder.Code, expectedStatus, recorder.Body.String())
			}
			session := h.manager.Status(taskID).Session
			shouldRevoke := scenario == "missing" || scenario == "terminal"
			if shouldRevoke != (session == nil) {
				t.Fatalf("grant revocation=%v, want %v", session == nil, shouldRevoke)
			}
			if !shouldRevoke && (session.ID != h.session.ID || session.Revision != h.session.Revision) {
				t.Fatal("transient read failure mutated grant")
			}
		})
	}
}

func TestCanceledStreamAuthorizationCheckPreservesGrant(t *testing.T) {
	h := newStreamHarness(t)
	base, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var session domain.Session
	for _, cookie := range h.client.Jar.Cookies(base) {
		if cookie.Name == sessionCookieName {
			session, err = h.api.auth.Authenticate(context.Background(), cookie.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if session.ID == "" {
		t.Fatal("fixture owner session missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if h.api.desktopStreamAllowed(ctx, session, h.taskID, h.session.ID, h.session.Revision) {
		t.Fatal("canceled check allowed stream")
	}
	if !h.api.desktopStreamAllowed(context.Background(), session, h.taskID, h.session.ID, h.session.Revision) {
		t.Fatal("canceled stream check revoked valid sharing")
	}
}

func TestLiveWebSocketAndSingleAgentScreenshotRequest(t *testing.T) {
	h := newStreamHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	task, err := h.store.Task(ctx, h.taskID)
	if err != nil {
		t.Fatal(err)
	}
	message := domain.Message{ID: domain.NewID("message"), TaskID: task.ID, Role: "user",
		Sender: "owner", Content: "Fixture screenshot", CreatedAt: time.Now().UTC()}
	if err := h.store.AddMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	turn, err := h.store.CreateAgentTurn(ctx, domain.Turn{
		ID: domain.NewID("turn"), TaskID: task.ID, AgentID: "main", RoundID: domain.NewID("round"),
		Status: domain.TurnRunning, QueuedAt: time.Now().UTC(), InputMessageID: message.ID,
		RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := h.caps.Issue(task.ID, "main", turn.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status, err := h.manager.Control(task.ID, h.session.ID, h.session.Revision, "agent")
	if err != nil {
		t.Fatal(err)
	}
	h.session = *status.Session
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	h.fixture.beforePreview = func(ctx context.Context) error {
		if h.fixture.captures.Load() == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	connection := h.connect(t, h.server.URL)
	startStream(t, h, connection, "live")
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("preview never reached provider")
	}
	type result struct {
		status int
		frame  desktop.Observation
		err    error
	}
	read := make(chan result, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
			h.server.URL+"/api/v1/agent/desktop/observation?session_id="+h.session.ID, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := h.client.Do(req)
		if err != nil {
			read <- result{err: err}
			return
		}
		defer resp.Body.Close()
		var body struct {
			Observation desktop.Observation `json:"observation"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		read <- result{status: resp.StatusCode, frame: body.Observation, err: err}
	}()
	// Keep the native lane occupied long enough to expose an immediate busy reply.
	select {
	case result := <-read:
		t.Fatalf("Agent request returned before in-flight preview completed: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	unblock.Do(func() { close(release) })
	resultValue := <-read
	if resultValue.err != nil || resultValue.status != http.StatusOK || resultValue.frame.ID == "" ||
		resultValue.frame.Width != 800 || resultValue.frame.PreviewWidth != 0 {
		t.Fatalf("single Agent read did not return a full observation: %+v", resultValue)
	}
	for range 3 {
		frame := streamFrame(t, connection)
		sendStream(t, connection, desktopStreamMessage{Type: "ack", FrameID: frame.ID, IntervalMS: 33})
	}
	if h.manager.Status(task.ID).Session == nil {
		t.Fatal("concurrent watching and screenshot revoked sharing")
	}
}
