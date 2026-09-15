package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/desktop"
	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/coder/websocket"
)

type desktopStreamMessage struct {
	Type       string `json:"type"`
	CSRFToken  string `json:"csrf_token,omitempty"`
	ViewID     string `json:"view_id,omitempty"`
	FrameID    string `json:"frame_id,omitempty"`
	MaxWidth   int    `json:"max_width,omitempty"`
	MaxHeight  int    `json:"max_height,omitempty"`
	Quality    int    `json:"quality,omitempty"`
	IntervalMS int    `json:"interval_ms,omitempty"`
}

func (message desktopStreamMessage) preferences() (desktop.FrameOptions, time.Duration, error) {
	options, err := desktop.NormalizeFrameOptions(desktop.FrameOptions{
		MaxWidth: message.MaxWidth, MaxHeight: message.MaxHeight, Quality: message.Quality,
	})
	interval := message.IntervalMS
	if interval == 0 {
		interval = 100
	}
	if interval < 33 || interval > 2000 {
		return options, 0, &desktop.Error{Code: "desktop_invalid_preview"}
	}
	return options, time.Duration(interval) * time.Millisecond, err
}

func decodeDesktopStreamMessage(data []byte) (desktopStreamMessage, error) {
	var message desktopStreamMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return message, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return message, errors.New("invalid message")
	}
	return message, nil
}

// Unlike general GET APIs, desktop sockets always require an explicit Origin.
// Forwarded HTTPS deployments can use the same Host without disabling the check.
func desktopStreamOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && (origin.Scheme == "http" || origin.Scheme == "https") &&
		origin.User == nil && origin.Path == "" && origin.RawQuery == "" && origin.Fragment == "" &&
		strings.EqualFold(origin.Host, r.Host)
}

func (s *Server) desktopStreamAllowed(ctx context.Context, authSession domain.Session, taskID, sessionID string, revision uint64) bool {
	auth, err := s.store.SessionByTokenHash(ctx, authSession.TokenHash)
	if err != nil || auth.ID != authSession.ID || !auth.RevokedAt.IsZero() || !auth.ExpiresAt.After(time.Now()) {
		return false
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.desktop.Revoke(taskID)
		}
		return false
	}
	if task.Status.Terminal() {
		s.desktop.Revoke(taskID)
		return false
	}
	if _, err := s.store.RetiredChannelInstanceForTask(ctx, taskID); err == nil {
		s.desktop.Revoke(taskID)
		return false
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false
	}
	status := s.desktop.Status(taskID)
	return status.StreamSupported && status.Session != nil && status.Session.ID == sessionID &&
		status.Session.Revision == revision && status.Session.Mode == "foreground" && !status.Session.Switching
}

func (s *Server) desktopStream(w http.ResponseWriter, r *http.Request) {
	if !desktopStreamOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_rejected")
		return
	}
	taskID, actor, ok := s.desktopTarget(w, r)
	if !ok {
		return
	}
	authSession, authenticated := sessionFromContext(r.Context())
	if !authenticated || actor != "owner" {
		writeError(w, http.StatusForbidden, "authentication_required")
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	status := s.desktop.Status(taskID)
	if !status.StreamSupported || status.Session == nil || status.Session.ID != sessionID || status.Session.Mode != "foreground" || status.Session.Switching {
		writeError(w, http.StatusConflict, "desktop_stream_unavailable")
		return
	}
	revision := status.Session.Revision
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(4096)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 5*time.Second)
	kind, data, err := connection.Read(handshakeCtx)
	handshakeCancel()
	if err != nil {
		return
	}
	start, err := decodeDesktopStreamMessage(data)
	if err != nil || kind != websocket.MessageText || start.Type != "start" ||
		!desktop.ValidViewID(start.ViewID) || start.FrameID != "" ||
		subtle.ConstantTimeCompare([]byte(start.CSRFToken), []byte(authSession.CSRFToken)) != 1 ||
		authSession.CSRFToken == "" {
		_ = writeHardwareWSJSON(ctx, connection, map[string]string{"type": "error", "error": "desktop_stream_unauthorized"})
		return
	}
	options, interval, err := start.preferences()
	if err != nil || !s.desktopStreamAllowed(ctx, authSession, taskID, sessionID, revision) {
		_ = writeHardwareWSJSON(ctx, connection, map[string]string{"type": "error", "error": "desktop_stream_unavailable"})
		return
	}
	actor, err = desktop.ViewActor(actor, start.ViewID)
	if err != nil {
		return
	}
	release, err := s.desktop.RegisterStream(taskID, sessionID, actor)
	if err != nil {
		_ = writeHardwareWSJSON(ctx, connection, map[string]string{"type": "error", "error": desktop.ErrorCode(err)})
		return
	}
	defer release()
	if err := writeHardwareWSJSON(ctx, connection, map[string]any{"type": "ready", "protocol": 1, "status": status}); err != nil {
		return
	}

	// Independent revocation checks interrupt blocked capture/writes as well as
	// idle sockets. Video must never keep a revoked grant alive.
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		lastAuthCheck := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state := s.desktop.Status(taskID).Session
				if state == nil || state.ID != sessionID || state.Revision != revision {
					cancel()
					return
				}
				if time.Since(lastAuthCheck) >= time.Second {
					checkCtx, checkCancel := context.WithTimeout(ctx, time.Second)
					allowed := s.desktopStreamAllowed(checkCtx, authSession, taskID, sessionID, revision)
					checkCancel()
					if !allowed {
						cancel()
						return
					}
					lastAuthCheck = time.Now()
				}
			}
		}
	}()
	incoming := make(chan hardwareWSRead, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			kind, data, readErr := connection.Read(ctx)
			select {
			case incoming <- hardwareWSRead{messageType: kind, data: data, err: readErr}:
			case <-ctx.Done():
				return
			}
			if readErr != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		connection.CloseNow()
		<-watchDone
		<-readDone
	}()
	pending := make(map[string]time.Time, 2)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-incoming:
			if event.err != nil || event.messageType != websocket.MessageText {
				return
			}
			ack, err := decodeDesktopStreamMessage(event.data)
			if err != nil || ack.Type != "ack" || ack.ViewID != "" || ack.CSRFToken != "" {
				return
			}
			if _, exists := pending[ack.FrameID]; !exists {
				return
			}
			options, interval, err = ack.preferences()
			if err != nil {
				return
			}
			delete(pending, ack.FrameID)
		case <-timer.C:
			for _, at := range pending {
				if time.Since(at) > 10*time.Second {
					return
				}
			}
			if len(pending) >= 2 {
				timer.Reset(50 * time.Millisecond)
				continue
			}
			started := time.Now()
			observation, err := s.desktop.ObserveStream(ctx, taskID, sessionID, actor, options)
			captureMS := time.Since(started).Milliseconds()
			if err != nil {
				code := desktop.ErrorCode(err)
				if code == "desktop_busy" || code == "desktop_frame_superseded" || code == "desktop_foreground_unavailable" {
					timer.Reset(33 * time.Millisecond)
					continue
				}
				_ = writeHardwareWSJSON(ctx, connection, map[string]string{"type": "error", "error": code})
				return
			}
			frame, err := encodeDesktopFrame(observation, captureMS, interval.Milliseconds())
			if err != nil {
				_ = writeHardwareWSJSON(ctx, connection, map[string]string{"type": "error", "error": "desktop_invalid_frame"})
				return
			}
			if err := ctx.Err(); err != nil {
				return
			}
			state := s.desktop.Status(taskID).Session
			if state == nil || state.ID != sessionID || state.Revision != revision {
				return
			}
			pending[observation.ID] = time.Now()
			if err := writeHardwareWS(ctx, connection, websocket.MessageBinary, frame); err != nil {
				return
			}
			timer.Reset(max(time.Millisecond, interval-time.Since(started)))
		}
	}
}

func encodeDesktopFrame(observation desktop.Observation, captureMS, intervalMS int64) ([]byte, error) {
	if len(observation.Image) > base64.StdEncoding.EncodedLen(8*1024*1024) {
		return nil, errors.New("frame too large")
	}
	image, err := base64.StdEncoding.DecodeString(observation.Image)
	if err != nil || len(image) == 0 && observation.CaptureError == "" || len(image) > 8*1024*1024 {
		return nil, errors.New("invalid frame")
	}
	if observation.Mime == "" {
		observation.Mime = "image/png"
	}
	if observation.Mime != "image/png" && observation.Mime != "image/jpeg" {
		return nil, errors.New("invalid mime")
	}
	observation.Image = ""
	if observation.PreviewWidth == 0 {
		observation.PreviewWidth, observation.PreviewHeight = observation.Width, observation.Height
	}
	metadata, err := json.Marshal(map[string]any{"type": "frame", "observation": observation,
		"capture_ms": captureMS, "interval_ms": intervalMS})
	if err != nil || len(metadata) > 64*1024 {
		return nil, errors.New("frame metadata too large")
	}
	frame := make([]byte, 4+len(metadata)+len(image))
	binary.BigEndian.PutUint32(frame, uint32(len(metadata)))
	copy(frame[4:], metadata)
	copy(frame[4+len(metadata):], image)
	return frame, nil
}
