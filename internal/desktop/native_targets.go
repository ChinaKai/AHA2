package desktop

import (
	"context"
	_ "embed"
	"errors"
)

//go:embed native_targets.cs
var nativeTargetsSource string

var _ TargetProvider = (*nativeProvider)(nil)

func (p *nativeProvider) Targets(ctx context.Context) (Targets, error) {
	result, err := p.foregroundCall(ctx, nativeRequest{Operation: "targets"})
	if result.Targets.Windows == nil {
		result.Targets.Windows = []Window{}
	}
	if result.Targets.Monitors == nil {
		result.Targets.Monitors = []Monitor{}
	}
	if result.Targets.Desktops == nil {
		result.Targets.Desktops = []VirtualDesktop{}
	}
	return result.Targets, err
}

func (p *nativeProvider) SelectTarget(ctx context.Context, selection TargetSelection) (Window, error) {
	invalid := errors.New("desktop invalid_request")
	if len(selection.WindowID) > 128 || len(selection.DesktopID) > 64 || len(selection.MonitorID) > 128 {
		return Window{}, invalid
	}
	switch selection.Kind {
	case "window":
		if selection.WindowID == "" || selection.DesktopID != "" || selection.MonitorID != "" {
			return Window{}, invalid
		}
	case "desktop":
		if selection.DesktopID == "" || selection.WindowID != "" {
			return Window{}, invalid
		}
	case "new-desktop":
		if selection.DesktopID != "" || selection.WindowID != "" {
			return Window{}, invalid
		}
	default:
		return Window{}, invalid
	}
	result, err := p.foregroundCall(ctx, nativeRequest{Operation: "target_select", Selection: &selection})
	if err == nil {
		if selection.Kind == "window" && result.Window.ID != selection.WindowID ||
			selection.Kind != "window" && (result.Window.Kind != "desktop" || result.Window.DesktopID == "" || result.Window.MonitorID == "") {
			return Window{}, errors.New("desktop stale_target")
		}
	}
	return result.Window, err
}
