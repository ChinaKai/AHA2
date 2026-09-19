package desktop

import "context"

// Window IDs must identify a particular process lifetime, not just a reusable HWND.
type Window struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Process      string `json:"process"`
	Kind         string `json:"kind,omitempty"` // window or desktop
	DesktopID    string `json:"desktop_id,omitempty"`
	MonitorID    string `json:"monitor_id,omitempty"`
	DesktopName  string `json:"desktop_name,omitempty"`
	OtherDesktop bool   `json:"other_desktop,omitempty"`
}

type Monitor struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Primary bool   `json:"primary"`
}

type VirtualDesktop struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

type Targets struct {
	Windows                []Window         `json:"windows"`
	Monitors               []Monitor        `json:"monitors"`
	Desktops               []VirtualDesktop `json:"desktops"`
	DesktopSwitchSupported bool             `json:"desktop_switch_supported"`
	DesktopReason          string           `json:"desktop_reason,omitempty"`
}

type TargetSelection struct {
	Kind      string `json:"kind"` // window, desktop or new-desktop
	WindowID  string `json:"window_id,omitempty"`
	DesktopID string `json:"desktop_id,omitempty"`
	MonitorID string `json:"monitor_id,omitempty"`
}

// Selection is Owner-only and may physically switch the current virtual desktop.
type TargetProvider interface {
	Targets(context.Context) (Targets, error)
	SelectTarget(context.Context, TargetSelection) (Window, error)
}

type Element struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Role    string   `json:"role"`
	Value   string   `json:"value,omitempty"`
	Actions []string `json:"actions"`
	X       float64  `json:"x"`
	Y       float64  `json:"y"`
	Width   float64  `json:"width"`
	Height  float64  `json:"height"`
}

type Observation struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id"`
	Revision      uint64    `json:"revision"`
	Window        Window    `json:"window"`
	Elements      []Element `json:"elements"`
	Image         string    `json:"image"` // PNG bytes encoded as base64, never a URL.
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	CaptureError  string    `json:"capture_error,omitempty"`
	ControlError  string    `json:"control_error,omitempty"`
	InputActions  []string  `json:"input_actions,omitempty"`
	Surface       string    `json:"surface,omitempty"` // opaque, checked again before foreground input
	Mime          string    `json:"mime,omitempty"`
	PreviewWidth  int       `json:"preview_width,omitempty"`
	PreviewHeight int       `json:"preview_height,omitempty"`
}

type Action struct {
	Kind      string   `json:"kind"`
	ElementID string   `json:"element_id"`
	Value     string   `json:"value,omitempty"`
	X         float64  `json:"x,omitempty"`
	Y         float64  `json:"y,omitempty"`
	EndX      float64  `json:"end_x,omitempty"`
	EndY      float64  `json:"end_y,omitempty"`
	DeltaX    float64  `json:"delta_x,omitempty"`
	DeltaY    float64  `json:"delta_y,omitempty"`
	Button    string   `json:"button,omitempty"`
	Keys      []string `json:"keys,omitempty"`
}

// Provider enumerates shareable targets. Control itself always goes through the
// ForegroundProvider contract with its own explicit grant; there is no silent
// fallback to a less visible mode.
type Provider interface {
	Support() (bool, string)
	Windows(context.Context) ([]Window, error)
}

type ForegroundProvider interface {
	NewDesktop(context.Context) (Window, error)
	ObserveForeground(context.Context, Window) (Observation, error)
	ActForeground(context.Context, Window, Action, Observation) error
}

type FrameOptions struct {
	MaxWidth  int `json:"max_width"`
	MaxHeight int `json:"max_height"`
	Quality   int `json:"quality"`
}

// Preview dimensions may differ from Width/Height, which always remain the
// logical desktop coordinate space used to validate and dispatch input.
type PreviewProvider interface {
	ObservePreview(context.Context, Window, FrameOptions) (Observation, error)
}

type Session struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	Window     Window `json:"window"`
	Controller string `json:"controller"` // shared; owner/agent only for legacy grants
	Revision   uint64 `json:"revision"`
	ExpiresAt  string `json:"expires_at"`
	Claimed    bool   `json:"claimed"`
	Mode       string `json:"mode"`
	Switching  bool   `json:"switching,omitempty"`
}

type Status struct {
	Supported              bool     `json:"supported"`
	Reason                 string   `json:"reason,omitempty"`
	Session                *Session `json:"session"`
	ForegroundSupported    bool     `json:"foreground_supported"`
	StreamSupported        bool     `json:"stream_supported"`
	TargetsSupported       bool     `json:"targets_supported"`
	SharedControlSupported bool     `json:"shared_control_supported"`
}

type ActionRequest struct {
	SessionID     string `json:"session_id"`
	Revision      uint64 `json:"revision"`
	ObservationID string `json:"observation_id"`
	ViewID        string `json:"view_id,omitempty"`
	ActionID      string `json:"action_id,omitempty"`
	Action
}
