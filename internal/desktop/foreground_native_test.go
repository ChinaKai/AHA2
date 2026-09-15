package desktop

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestForegroundNativeUsesSeparateSourceAndTrustedSurface(t *testing.T) {
	p := &nativeProvider{run: func(_ context.Context, input []byte) ([]byte, error) {
		var packet struct {
			Source  string        `json:"source"`
			Request nativeRequest `json:"request"`
		}
		if err := json.Unmarshal(input, &packet); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(packet.Source, foregroundNativeSource) || packet.Request.Surface != "trusted-surface" ||
			packet.Request.Width != 400 || packet.Request.Height != 300 || packet.Request.Operation != "foreground_act" {
			t.Fatal("foreground source or trusted geometry not forwarded")
		}
		return []byte(`{"ok":true}`), nil
	}}
	o := Observation{Window: Window{ID: "fixture"}, Surface: "trusted-surface", Width: 400, Height: 300, InputActions: []string{"click"}}
	if err := p.ActForeground(context.Background(), o.Window, Action{Kind: "click", ElementID: "$surface", X: 10, Y: 10}, o); err != nil {
		t.Fatal(err)
	}
}

func TestForegroundNativeValidation(t *testing.T) {
	cases := []Action{
		{Kind: "click", X: -1}, {Kind: "click", X: 400}, {Kind: "click", Y: math.Inf(1)},
		{Kind: "click", Button: "invalid"}, {Kind: "drag", EndX: 401},
		{Kind: "scroll", DeltaY: 2401}, {Kind: "scroll"},
		{Kind: "text", Value: strings.Repeat("a", 4097)}, {Kind: "text", Value: "null\x00byte"},
		{Kind: "text", Value: "x", Keys: []string{"ENTER"}},
		{Kind: "key", Keys: []string{"CTRL", "ALT", "DELETE"}},
		{Kind: "key", Keys: []string{"CTRL", "WIN", "F4"}},
		{Kind: "key", Keys: []string{"WIN", "L"}},
		{Kind: "key", Keys: []string{"UNKNOWN"}}, {Kind: "key", Keys: []string{"A", "B"}},
		{Kind: "key", Keys: []string{"CTRL", "CTRL"}}, {Kind: "key"},
		{Kind: "focus", X: 1}, {Kind: "invoke"},
	}
	for _, action := range cases {
		if err := validateNativeForegroundAction(action, 400, 300); err == nil {
			t.Errorf("accepted invalid %s action", action.Kind)
		}
	}
	for _, action := range []Action{
		{Kind: "focus"}, {Kind: "click", X: 399, Y: 299, Button: "left"},
		{Kind: "double_click"}, {Kind: "drag", X: 1, Y: 1, EndX: 200, EndY: 200},
		{Kind: "scroll", DeltaX: 2400, DeltaY: -2400}, {Kind: "text", Value: "Unicode \u4e2d\u6587 \U0001f680"},
		{Kind: "text", Value: "CRLF\r\nLF\nCR\rTab\t"},
		{Kind: "key", Keys: []string{"CTRL", "L"}}, {Kind: "key", Keys: []string{"WIN", "R"}},
		{Kind: "key", Keys: []string{"WIN"}}, {Kind: "key", Keys: []string{"ENTER"}},
	} {
		if err := validateNativeForegroundAction(action, 400, 300); err != nil {
			t.Errorf("valid %s rejected: %v", action.Kind, err)
		}
	}
}

func TestForegroundNativeRejectsUntrustedOrUnadvertisedAction(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		t.Fatal("invalid action reached helper")
		return nil, nil
	}}
	o := Observation{Window: Window{ID: "fixture"}, Surface: "trusted", Width: 400, Height: 300, InputActions: []string{"focus"}}
	if err := p.ActForeground(context.Background(), o.Window, Action{Kind: "click", ElementID: "$surface"}, o); err == nil {
		t.Fatal("unadvertised action accepted")
	}
	if err := p.ActForeground(context.Background(), Window{ID: "other"}, Action{Kind: "focus", ElementID: "$surface"}, o); err == nil {
		t.Fatal("wrong observation target accepted")
	}
	if err := p.ActForeground(context.Background(), o.Window, Action{Kind: "focus", ElementID: "element"}, o); err == nil {
		t.Fatal("foreground action accepted without surface element")
	}
}

func TestForegroundNativeDesktopCreationMustReturnIdentity(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) { return []byte(`{"ok":true}`), nil }}
	if _, err := p.NewDesktop(context.Background()); err == nil {
		t.Fatal("desktop creation accepted without verified identity")
	}
}

func TestForegroundNativeDoesNotBypassSystemPolicy(t *testing.T) {
	for _, forbidden := range []string{"AttachThreadInput", "SPI_SETFOREGROUNDLOCKTIMEOUT", "BlockInput", "SwitchDesktop",
		"Clipboard", "SendKeys", "keybd_event", "SetThreadDesktop", "SeDebugPrivilege"} {
		if strings.Contains(foregroundNativeSource, forbidden) {
			t.Errorf("foreground helper contains policy bypass %q", forbidden)
		}
	}
	for _, guard := range []string{"hit.ToInt64() != 2", "GetAncestor(WindowFromPoint(point), 2) != target.Handle", "ActivateByCaptionClick(target)"} {
		if !strings.Contains(foregroundNativeSource, guard) {
			t.Errorf("missing explicit caption activation guard %q", guard)
		}
	}
}

func TestNativeBackgroundRejectsForegroundFields(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		t.Fatal("background action forwarded foreground input")
		return nil, nil
	}}
	for _, action := range []Action{
		{Kind: "invoke", ElementID: "x", X: 1},
		{Kind: "set_value", ElementID: "x", Value: "x", Keys: []string{"ENTER"}},
		{Kind: "toggle", ElementID: "x", Button: "left"},
	} {
		if err := p.Act(context.Background(), Window{ID: "fixture"}, action); err == nil {
			t.Fatal("background input field accepted")
		}
	}
}

func TestForegroundNativeInterruptedBatchMarkers(t *testing.T) {
	for _, test := range []struct {
		markers string
		want    bool
	}{
		{"", false}, {"aha_input_begin;", true},
		{"aha_input_begin;aha_input_end;", false},
		{"aha_input_begin;aha_input_end;aha_input_begin;", true},
	} {
		if got := nativeInputInterrupted([]byte(test.markers)); got != test.want {
			t.Errorf("interrupted=%t, want %t", got, test.want)
		}
	}
}
