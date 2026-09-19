package desktop

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	nativeTimeout   = 15 * time.Second
	nativeMaxOutput = 12 << 20
	nativeMaxValue  = 32 << 10
)

//go:embed native.ps1
var nativeBootstrap string

//go:embed native.cs
var nativeSource string

type nativeRequest struct {
	Operation string           `json:"operation"`
	WindowID  string           `json:"window_id,omitempty"`
	DesktopID string           `json:"desktop_id,omitempty"`
	Action    Action           `json:"action"`
	Surface   string           `json:"surface,omitempty"`
	Width     int              `json:"width,omitempty"`
	Height    int              `json:"height,omitempty"`
	Frame     *FrameOptions    `json:"frame,omitempty"`
	Selection *TargetSelection `json:"selection,omitempty"`
}

type nativeResponse struct {
	OK          bool        `json:"ok"`
	Error       string      `json:"error"`
	Windows     []Window    `json:"windows"`
	Window      Window      `json:"window"`
	Observation Observation `json:"observation"`
	Targets     Targets     `json:"targets"`
}

type nativeProvider struct {
	run          func(context.Context, []byte) ([]byte, error)
	reason       string
	mu           sync.Mutex
	closeWorkers func() error
}

func (p *nativeProvider) Close() error {
	if p.closeWorkers != nil {
		return p.closeWorkers()
	}
	return nil
}

func (p *nativeProvider) Support() (bool, string) {
	return p.run != nil, p.reason
}

func (p *nativeProvider) call(ctx context.Context, req nativeRequest) (nativeResponse, error) {
	return p.callSource(ctx, req, nativeSource)
}

func (p *nativeProvider) callSource(ctx context.Context, req nativeRequest, source string) (nativeResponse, error) {
	var result nativeResponse
	if p.run == nil {
		return result, fmt.Errorf("desktop unsupported: %s", p.reason)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	input, err := json.Marshal(struct {
		Source  string        `json:"source"`
		Request nativeRequest `json:"request"`
	}{source, req})
	if err != nil {
		return result, errors.New("desktop request encoding failed")
	}
	ctx, cancel := context.WithTimeout(ctx, nativeTimeout)
	defer cancel()
	output, err := p.run(ctx, input)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		var coded *Error
		if errors.As(err, &coded) && coded.Code == "desktop_busy" {
			return result, coded
		}
		return result, errors.New("desktop helper failed")
	}
	if len(output) > nativeMaxOutput {
		return result, errors.New("desktop helper output limit exceeded")
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&result); err != nil {
		return result, errors.New("desktop helper returned invalid JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, errors.New("desktop helper returned trailing output")
	}
	if !result.OK {
		// Never forward exception text or arbitrary provider content as an error.
		switch result.Error {
		case "background_desktop_changed", "background_context_changed",
			"background_control_changed", "background_child_unverifiable",
			"browser_accessibility_unavailable", "browser_scope_changed", "browser_page_unavailable",
			"target_state_unavailable", "target_process_unavailable", "target_token_unavailable",
			"target_foreign_session", "target_foreign_user", "foreground_unavailable":
			return result, fmt.Errorf("desktop %s", result.Error)
		case "unsupported_action", "unsupported_target", "stale_target", "password_element",
			"element_unavailable", "element_disabled", "element_read_only", "focus_side_effect",
			"focus_unverifiable", "element_limit", "invalid_request", "native_unavailable":
			return result, fmt.Errorf("desktop %s", result.Error)
		case "secure_desktop", "elevated_target", "foreground_denied", "foreground_changed",
			"caption_unavailable", "foreground_activation_unconfirmed", "target_disabled",
			"desktop_changed",
			"monitor_changed", "monitor_required", "monitor_unavailable", "foreground_outside_monitor",
			"desktop_enumeration_unavailable", "desktop_state_inconsistent", "desktop_switch_unverified", "desktop_switch_limit",
			"refresh_required", "desktop_identity_unavailable", "desktop_creation_unverified",
			"input_blocked", "input_busy", "input_partial", "unsafe_key_chord", "point_obscured":
			return result, fmt.Errorf("desktop %s", result.Error)
		default:
			return result, errors.New("desktop native operation failed")
		}
	}
	return result, nil
}

func (p *nativeProvider) Windows(ctx context.Context) ([]Window, error) {
	result, err := p.call(ctx, nativeRequest{Operation: "windows"})
	if result.Windows == nil {
		result.Windows = []Window{}
	}
	return result.Windows, err
}

type nativeBoundedWriter struct {
	buffer bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (w *nativeBoundedWriter) Write(b []byte) (int, error) {
	if len(b) > w.limit-w.buffer.Len() {
		w.cancel()
		return 0, errors.New("desktop helper output limit exceeded")
	}
	return w.buffer.Write(b)
}
