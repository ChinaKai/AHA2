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

//go:embed native_background.cs
var nativeBackgroundSource string

//go:embed native_background_lifetime.cs
var nativeBackgroundLifetimeSource string

//go:embed native_browser.cs
var nativeBrowserSource string

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
	// A provider that changed focus must not receive another action.
	unsafeWindows map[string]bool
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

func (p *nativeProvider) Observe(ctx context.Context, window Window) (Observation, error) {
	if window.ID == "" || len(window.ID) > 128 {
		return Observation{}, errors.New("desktop invalid window")
	}
	operation := "observe"
	if window.DesktopID != "" {
		operation = "background_observe"
	}
	result, err := p.call(ctx, nativeRequest{Operation: operation, WindowID: window.ID, DesktopID: window.DesktopID})
	if err == nil && (result.Observation.Window.ID != window.ID ||
		window.DesktopID != "" && result.Observation.Window.DesktopID != window.DesktopID) {
		return Observation{}, errors.New("desktop observation target mismatch")
	}
	if result.Error == "focus_side_effect" {
		p.markUnsafe(window.ID)
	}
	p.mu.Lock()
	unsafe := p.unsafeWindows[window.ID]
	p.mu.Unlock()
	if unsafe {
		result.Observation.ControlError = "desktop_focus_side_effect"
		for i := range result.Observation.Elements {
			result.Observation.Elements[i].Actions = []string{}
		}
	}
	return result.Observation, err
}

func (p *nativeProvider) markUnsafe(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unsafeWindows == nil {
		p.unsafeWindows = make(map[string]bool)
	}
	p.unsafeWindows[id] = true
}

func (p *nativeProvider) Act(ctx context.Context, window Window, action Action) error {
	switch action.Kind {
	case "invoke", "set_value", "toggle", "select", "expand", "collapse":
	default:
		return errors.New("desktop unsupported_action")
	}
	if window.ID == "" || len(window.ID) > 128 || action.ElementID == "" ||
		len(action.ElementID) > 1024 || len(action.Value) > nativeMaxValue ||
		(action.Kind != "set_value" && action.Value != "") || action.X != 0 || action.Y != 0 ||
		action.EndX != 0 || action.EndY != 0 || action.DeltaX != 0 || action.DeltaY != 0 ||
		action.Button != "" || len(action.Keys) != 0 {
		return errors.New("desktop invalid_request")
	}
	p.mu.Lock()
	unsafe := p.unsafeWindows[window.ID]
	p.mu.Unlock()
	if unsafe {
		return errors.New("desktop focus_side_effect")
	}
	operation := "act"
	if window.DesktopID != "" {
		operation = "background_act"
	}
	result, err := p.call(ctx, nativeRequest{Operation: operation, WindowID: window.ID, DesktopID: window.DesktopID, Action: action})
	if result.Error == "focus_side_effect" {
		p.markUnsafe(window.ID)
	}
	return err
}

func (p *nativeProvider) SelectBackgroundWindow(ctx context.Context, id string) (Window, error) {
	if id == "" || len(id) > 128 {
		return Window{}, errors.New("desktop invalid_request")
	}
	result, err := p.call(ctx, nativeRequest{Operation: "background_select", WindowID: id})
	if err == nil && (result.Window.ID != id || result.Window.DesktopID == "" || result.Window.Kind != "window") {
		return Window{}, errors.New("desktop stale_target")
	}
	return result.Window, err
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
