package desktop

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// Loaded only for explicitly selected foreground operations.
//
//go:embed foreground_native.cs
var foregroundBaseSource string

var foregroundNativeSource = foregroundBaseSource + "\n" + nativeTargetsSource + "\n" + nativeBackgroundSource + "\n" + nativeBackgroundLifetimeSource + "\n" + nativeBrowserSource

var _ ForegroundProvider = (*nativeProvider)(nil)

func nativeInputInterrupted(markers []byte) bool {
	return bytes.LastIndex(markers, []byte("aha_input_begin;")) > bytes.LastIndex(markers, []byte("aha_input_end;"))
}

func (p *nativeProvider) foregroundCall(ctx context.Context, req nativeRequest) (nativeResponse, error) {
	return p.callSource(ctx, req, nativeSource+"\n"+foregroundNativeSource)
}

func (p *nativeProvider) NewDesktop(ctx context.Context) (Window, error) {
	result, err := p.foregroundCall(ctx, nativeRequest{Operation: "foreground_new_desktop"})
	if err == nil && (!strings.HasPrefix(result.Window.ID, "desktop:") || result.Window.Kind != "desktop") {
		return Window{}, errors.New("desktop desktop_creation_unverified")
	}
	return result.Window, err
}

func (p *nativeProvider) ObserveForeground(ctx context.Context, window Window) (Observation, error) {
	if window.ID == "" || len(window.ID) > 128 {
		return Observation{}, errors.New("desktop invalid_request")
	}
	result, err := p.foregroundCall(ctx, nativeRequest{Operation: "foreground_observe", WindowID: window.ID})
	if err == nil && (result.Observation.Window.ID != window.ID || result.Observation.Surface == "") {
		return Observation{}, errors.New("desktop observation target mismatch")
	}
	return result.Observation, err
}

func (p *nativeProvider) ActForeground(ctx context.Context, window Window, action Action, observation Observation) error {
	if window.ID == "" || len(window.ID) > 128 || observation.Window.ID != window.ID ||
		observation.Surface == "" || len(observation.Surface) > 2048 || action.ElementID != "$surface" ||
		!slices.Contains(observation.InputActions, action.Kind) {
		return errors.New("desktop invalid_request")
	}
	if err := validateNativeForegroundAction(action, observation.Width, observation.Height); err != nil {
		return err
	}
	_, err := p.foregroundCall(ctx, nativeRequest{
		Operation: "foreground_act", WindowID: window.ID, Action: action,
		Surface: observation.Surface, Width: observation.Width, Height: observation.Height,
	})
	return err
}

func validateNativeForegroundAction(a Action, width, height int) error {
	invalid := errors.New("desktop invalid_request")
	for _, n := range []float64{a.X, a.Y, a.EndX, a.EndY, a.DeltaX, a.DeltaY} {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return invalid
		}
	}
	point := func(x, y float64) bool { return x >= 0 && y >= 0 && x < float64(width) && y < float64(height) }
	mouse := a.Kind == "click" || a.Kind == "double_click" || a.Kind == "drag" || a.Kind == "scroll"
	if mouse && !point(a.X, a.Y) {
		return invalid
	}
	if !mouse && (a.X != 0 || a.Y != 0) || a.Kind != "drag" && (a.EndX != 0 || a.EndY != 0) ||
		a.Kind != "scroll" && (a.DeltaX != 0 || a.DeltaY != 0) ||
		a.Kind != "text" && a.Value != "" || a.Kind != "key" && len(a.Keys) != 0 {
		return invalid
	}
	if a.Kind == "click" || a.Kind == "double_click" || a.Kind == "drag" {
		if a.Button != "" && a.Button != "left" && a.Button != "right" && a.Button != "middle" {
			return invalid
		}
	} else if a.Button != "" {
		return invalid
	}
	switch a.Kind {
	case "focus":
	case "click", "double_click":
	case "drag":
		if !point(a.EndX, a.EndY) {
			return invalid
		}
	case "scroll":
		if math.Abs(a.DeltaX) > 2400 || math.Abs(a.DeltaY) > 2400 || (a.DeltaX == 0 && a.DeltaY == 0) {
			return invalid
		}
	case "text":
		if a.Value == "" || !utf8.ValidString(a.Value) || len(a.Value) > 4096 {
			return invalid
		}
		for _, r := range a.Value {
			if r < 32 && r != '\r' && r != '\n' && r != '\t' || r == 127 {
				return invalid
			}
		}
	case "key":
		return validateNativeKeys(a.Keys)
	default:
		return errors.New("desktop unsupported_action")
	}
	return nil
}

func validateNativeKeys(keys []string) error {
	if len(keys) < 1 || len(keys) > 4 {
		return errors.New("desktop invalid_request")
	}
	seen := map[string]bool{}
	nonModifier := 0
	for _, key := range keys {
		if seen[key] {
			return errors.New("desktop invalid_request")
		}
		seen[key] = true
		if slices.Contains([]string{"CTRL", "ALT", "SHIFT", "WIN"}, key) {
			continue
		}
		nonModifier++
		valid := len(key) == 1 && (key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9')
		valid = valid || slices.Contains([]string{"ENTER", "ESC", "ESCAPE", "TAB", "SPACE", "BACKSPACE",
			"DELETE", "INSERT", "HOME", "END", "PAGEUP", "PAGEDOWN", "LEFT", "RIGHT", "UP", "DOWN",
			"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12"}, key)
		if !valid {
			return errors.New("desktop invalid_request")
		}
	}
	if nonModifier > 1 {
		return errors.New("desktop invalid_request")
	}
	if seen["CTRL"] && seen["ALT"] && seen["DELETE"] || seen["WIN"] && seen["CTRL"] && seen["F4"] ||
		seen["WIN"] && seen["L"] || seen["WIN"] && seen["U"] {
		return errors.New("desktop unsafe_key_chord")
	}
	return nil
}
