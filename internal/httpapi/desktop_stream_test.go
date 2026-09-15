package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/agentapi"
	"github.com/ChinaKai/AHA2/internal/app"
	"github.com/ChinaKai/AHA2/internal/auth"
	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
	"github.com/coder/websocket"
)

type desktopStreamFixture struct {
	foregroundHTTPFixture
	captures      atomic.Int32
	beforePreview func(context.Context) error
}

func (p *desktopStreamFixture) ObservePreview(ctx context.Context, w desktop.Window, o desktop.FrameOptions) (desktop.Observation, error) {
	p.captures.Add(1)
	if p.beforePreview != nil {
		if err := p.beforePreview(ctx); err != nil {
			return desktop.Observation{}, err
		}
	}
	frame, err := p.ObserveForeground(ctx, w)
	frame.Image = base64.StdEncoding.EncodeToString([]byte{255, 216, 255, 217})
	frame.Mime, frame.PreviewWidth, frame.PreviewHeight = "image/jpeg", 400, 300
	return frame, err
}

type streamHarness struct {
	server  *httptest.Server
	client  *http.Client
	csrf    string
	base    string
	taskID  string
	session desktop.Session
	manager *desktop.Manager
	fixture *desktopStreamFixture
	store   *store.Store
	api     *Server
	caps    *agentapi.Capabilities
}

func newStreamHarness(t *testing.T, ttl ...time.Duration) *streamHarness {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "aha.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	task := createHardwareAPITask(t, db)
	p := &desktopStreamFixture{}
	m := desktop.New(p)
	authTTL := time.Hour
	if len(ttl) != 0 {
		authTTL = ttl[0]
	}
	caps := agentapi.NewCapabilities()
	api := New(Config{Store: db, Auth: auth.NewService(db, "setup-test", authTTL),
		App: app.NewService(db, nil, app.StubExecutor{}), Desktop: m, AgentCapabilities: caps})
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	csrf := registerOwner(t, client, server.URL)
	status, err := m.OpenWithMode(context.Background(), task.ID, "new-desktop", "foreground", true)
	if err != nil {
		t.Fatal(err)
	}
	return &streamHarness{server: server, client: client, csrf: csrf,
		base: server.URL + "/api/v1/tasks/" + task.ID + "/desktop", taskID: task.ID,
		session: *status.Session, manager: m, fixture: p, store: db, api: api, caps: caps}
}

func TestDesktopStreamRetriesUnavailableForegroundWithoutRevocation(t *testing.T) {
	h := newStreamHarness(t)
	h.fixture.beforePreview = func(context.Context) error {
		if h.fixture.captures.Load() == 1 {
			return &desktop.Error{Code: "desktop_foreground_unavailable"}
		}
		return nil
	}
	connection := h.connect(t, h.server.URL)
	startStream(t, h, connection, "transient")
	frame := streamFrame(t, connection)
	if frame.ID == "" || h.fixture.captures.Load() < 2 || h.manager.Status(h.taskID).Session == nil {
		t.Fatal("foreground transition ended the stream or revoked sharing")
	}
}

func (h *streamHarness) connect(t *testing.T, origin string) *websocket.Conn {
	t.Helper()
	headers := http.Header{}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, response, err := websocket.Dial(ctx, h.base+"/stream?session_id="+h.session.ID,
		&websocket.DialOptions{HTTPClient: h.client, HTTPHeader: headers})
	if origin != h.server.URL {
		if err == nil {
			c.CloseNow()
			t.Fatal("cross-origin/missing origin stream accepted")
		}
		if response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatal("unexpected origin rejection", err)
		}
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func sendStream(t *testing.T, c *websocket.Conn, payload any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func readStream(t *testing.T, c *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	kind, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return kind, data
}

func startStream(t *testing.T, h *streamHarness, c *websocket.Conn, view string) {
	t.Helper()
	sendStream(t, c, desktopStreamMessage{Type: "start", CSRFToken: h.csrf, ViewID: view, IntervalMS: 33})
	kind, data := readStream(t, c)
	if kind != websocket.MessageText || !strings.Contains(string(data), `"ready"`) {
		t.Fatalf("stream not ready: %s", data)
	}
}

func streamFrame(t *testing.T, c *websocket.Conn) desktop.Observation {
	t.Helper()
	kind, data := readStream(t, c)
	if kind != websocket.MessageBinary || len(data) < 4 {
		t.Fatal("expected binary frame")
	}
	n := int(binary.BigEndian.Uint32(data[:4]))
	if n > 64*1024 || n+4 >= len(data) {
		t.Fatal("invalid frame envelope")
	}
	var meta struct {
		Observation desktop.Observation `json:"observation"`
	}
	if err := json.Unmarshal(data[4:4+n], &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Observation.Image != "" || meta.Observation.Width != 800 || meta.Observation.PreviewWidth != 400 ||
		meta.Observation.Mime != "image/jpeg" || len(data[4+n:]) != 4 {
		t.Fatal("logical geometry or binary payload changed")
	}
	return meta.Observation
}

func TestDesktopStreamOriginAndCSRFBeforeCapture(t *testing.T) {
	h := newStreamHarness(t)
	h.connect(t, "")
	h.connect(t, "http://attacker.invalid")
	c := h.connect(t, h.server.URL)
	sendStream(t, c, desktopStreamMessage{Type: "start", CSRFToken: "wrong", ViewID: "v"})
	_, data := readStream(t, c)
	if !strings.Contains(string(data), "unauthorized") || h.fixture.captures.Load() != 0 {
		t.Fatal("capture occurred before CSRF confirmation")
	}
}

func TestDesktopStreamCreditBoundAndIndependentHTTPInput(t *testing.T) {
	h := newStreamHarness(t)
	c := h.connect(t, h.server.URL)
	startStream(t, h, c, "v")
	first := streamFrame(t, c)
	streamFrame(t, c)
	time.Sleep(180 * time.Millisecond)
	if n := h.fixture.captures.Load(); n != 2 {
		t.Fatal("unacknowledged captures unbounded", n)
	}
	action := desktop.ActionRequest{SessionID: h.session.ID, Revision: h.session.Revision,
		ObservationID: first.ID, ViewID: "v", ActionID: "a1",
		Action: desktop.Action{ElementID: "$surface", Kind: "click", X: 799, Y: 599}}
	response := requestJSON(t, h.client, http.MethodPost, h.base+"/actions", action, h.csrf)
	if response.StatusCode != http.StatusOK || h.fixture.calls.Load() != 1 {
		t.Fatal("pending video blocked input or used JPEG coordinates", response.StatusCode)
	}
	response.Body.Close()
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/actions", action, h.csrf)
	if response.StatusCode != http.StatusConflict || h.fixture.calls.Load() != 1 {
		t.Fatal("action replay reached provider")
	}
	response.Body.Close()
	sendStream(t, c, desktopStreamMessage{Type: "ack", FrameID: first.ID, MaxWidth: 640, MaxHeight: 360, Quality: 40, IntervalMS: 100})
	streamFrame(t, c)
	if h.fixture.captures.Load() != 3 {
		t.Fatal("ACK did not restore one credit")
	}
}

func TestDesktopStreamRevokedOnLogoutStopAndControlChange(t *testing.T) {
	for _, operation := range []string{"logout", "stop", "transfer", "terminal"} {
		t.Run(operation, func(t *testing.T) {
			h := newStreamHarness(t)
			c := h.connect(t, h.server.URL)
			startStream(t, h, c, "v")
			streamFrame(t, c)
			streamFrame(t, c)
			switch operation {
			case "logout":
				response := requestJSON(t, h.client, http.MethodPost, h.server.URL+"/api/v1/auth/logout", nil, h.csrf)
				if response.StatusCode != http.StatusOK {
					t.Fatal("logout failed")
				}
				response.Body.Close()
			case "stop":
				h.manager.Stop(h.taskID, h.session.ID)
			case "transfer":
				h.manager.Control(h.taskID, h.session.ID, h.session.Revision, "agent")
			case "terminal":
				task, err := h.store.Task(context.Background(), h.taskID)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC().Format(time.RFC3339Nano)
				if err := h.store.UpdateTaskStatus(context.Background(), h.taskID, task.Status, domain.TaskCompleted, now, now); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _, err := c.Read(ctx)
			if err == nil || ctx.Err() != nil {
				t.Fatal("revoked socket remained open", err)
			}
			if h.fixture.captures.Load() != 2 {
				t.Fatal("revoked socket captured again")
			}
		})
	}
}

func TestDesktopStreamAuthExpiryClosesIdleConnection(t *testing.T) {
	h := newStreamHarness(t, 2*time.Second)
	c := h.connect(t, h.server.URL)
	startStream(t, h, c, "v")
	streamFrame(t, c)
	streamFrame(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatal("expired authentication kept socket open")
	}
}

func TestDesktopStreamMalformedAckAndDisconnectedView(t *testing.T) {
	h := newStreamHarness(t)
	c := h.connect(t, h.server.URL)
	startStream(t, h, c, "v")
	frame := streamFrame(t, c)
	streamFrame(t, c)
	sendStream(t, c, map[string]any{"type": "ack", "frame_id": frame.ID, "command": "input"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatal("malformed ACK kept socket open")
	}
	action := desktop.ActionRequest{SessionID: h.session.ID, Revision: h.session.Revision,
		ViewID: "v", ObservationID: frame.ID, Action: desktop.Action{ElementID: "$surface", Kind: "click"}}
	// Handler cleanup follows transport closure; wait for its own view revocation.
	deadline := time.Now().Add(time.Second)
	for {
		_, err := h.manager.Act(context.Background(), h.taskID, "owner", action)
		if err != nil && desktop.ErrorCode(err) == "desktop_stale_observation" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected view frames survived", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDesktopFrameEnvelopeLimits(t *testing.T) {
	for _, frame := range []desktop.Observation{
		{Image: "bad-base64"}, {Image: "YQ==", Mime: "image/svg+xml"},
		{Image: "YQ==", Surface: strings.Repeat("x", 65536)},
		{Image: strings.Repeat("x", 12*1024*1024)},
	} {
		if _, err := encodeDesktopFrame(frame, 1, 100); err == nil {
			t.Fatal("invalid frame encoded")
		}
	}
}

func TestDesktopFrameMinimizedStatusNeedsNoBitmap(t *testing.T) {
	frame, err := encodeDesktopFrame(desktop.Observation{
		CaptureError: "window_minimized", InputActions: []string{"focus"}, Mime: "image/jpeg",
	}, 10, 100)
	if err != nil || len(frame) < 5 {
		t.Fatal("minimized status frame broke the stream", err)
	}
	if int(binary.BigEndian.Uint32(frame[:4])) != len(frame)-4 {
		t.Fatal("unexpected bitmap on minimized frame")
	}
	if _, err := encodeDesktopFrame(desktop.Observation{Mime: "image/jpeg"}, 10, 100); err == nil {
		t.Fatal("missing capture with no diagnostic was accepted")
	}
}

func TestDesktopStreamingCSPAllowsImagesButNotBlobScripts(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	policy := w.Header().Get("Content-Security-Policy")
	directives := map[string]string{}
	for _, directive := range strings.Split(policy, ";") {
		key, value, _ := strings.Cut(strings.TrimSpace(directive), " ")
		directives[key] = value
	}
	if !strings.Contains(directives["img-src"], "blob:") || directives["script-src"] != "'self'" ||
		directives["connect-src"] != "'self'" {
		t.Fatal("CSP blocks streamed images or broadens script/network access", policy)
	}
}

func TestDesktopStreamRejectsDuplicateViewAndOversizedMessages(t *testing.T) {
	h := newStreamHarness(t)
	first := h.connect(t, h.server.URL)
	startStream(t, h, first, "same")
	frame := streamFrame(t, first)
	streamFrame(t, first)
	second := h.connect(t, h.server.URL)
	sendStream(t, second, desktopStreamMessage{Type: "start", CSRFToken: h.csrf, ViewID: "same"})
	_, data := readStream(t, second)
	if !strings.Contains(string(data), "view_in_use") {
		t.Fatal("duplicate view connected")
	}
	sendStream(t, first, desktopStreamMessage{Type: "ack", FrameID: frame.ID})
	streamFrame(t, first)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = first.Write(ctx, websocket.MessageText, []byte(strings.Repeat("x", 5000)))
	_, _, err := first.Read(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatal("oversized message did not close stream")
	}
}

func TestDesktopStreamStalledAckTimesOut(t *testing.T) {
	if testing.Short() {
		t.Skip("real transport ACK timeout")
	}
	h := newStreamHarness(t)
	c := h.connect(t, h.server.URL)
	startStream(t, h, c, "slow")
	streamFrame(t, c)
	streamFrame(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil || ctx.Err() != nil || h.fixture.captures.Load() != 2 {
		t.Fatal("stalled client retained or accumulated frames", err)
	}
}

func TestDesktopHTTPPreviewOptionsAndViewIsolation(t *testing.T) {
	h := newStreamHarness(t)
	for _, query := range []string{"max_width=wrong", "quality=999", "view_id=agent:turn", "max_height=-1"} {
		response := requestJSON(t, h.client, http.MethodGet, h.base+"/observation?session_id="+h.session.ID+"&"+query, nil, "")
		if response.StatusCode != http.StatusBadRequest {
			t.Fatal("invalid preview accepted", query, response.StatusCode)
		}
		response.Body.Close()
	}
	if h.fixture.captures.Load() != 0 {
		t.Fatal("invalid request reached capture")
	}
	response := requestJSON(t, h.client, http.MethodGet,
		h.base+"/observation?session_id="+h.session.ID+"&view_id=http&max_width=640&max_height=360&quality=40", nil, "")
	var body struct {
		Observation desktop.Observation `json:"observation"`
	}
	decodeResponse(t, response, &body)
	if response.StatusCode != http.StatusOK || body.Observation.Mime != "image/jpeg" {
		t.Fatal("preview HTTP fallback failed")
	}
	action := desktop.ActionRequest{SessionID: h.session.ID, Revision: h.session.Revision, ObservationID: body.Observation.ID,
		ViewID: "other", Action: desktop.Action{ElementID: "$surface", Kind: "click"}}
	response = requestJSON(t, h.client, http.MethodPost, h.base+"/actions", action, h.csrf)
	if response.StatusCode != http.StatusConflict || h.fixture.calls.Load() != 0 {
		t.Fatal("HTTP view isolation failed")
	}
	response.Body.Close()
}
