package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeOtherUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows behavior")
	}
	p := NativeProvider()
	if supported, reason := p.Support(); supported || reason == "" {
		t.Fatalf("Support = %v, %q", supported, reason)
	}
	if _, err := p.Windows(context.Background()); err == nil {
		t.Fatal("expected unsupported error")
	}
	if _, err := p.Observe(context.Background(), Window{ID: "fixture"}); err == nil {
		t.Fatal("expected unsupported error")
	}
	if err := p.Act(context.Background(), Window{ID: "fixture"}, Action{Kind: "invoke", ElementID: "fixture"}); err == nil {
		t.Fatal("expected unsupported error")
	}
}

func TestNativeRequestIsStructuredData(t *testing.T) {
	value := "'; $(throw 'injection');\n\"quoted\"\u4e2d\u6587"
	p := &nativeProvider{run: func(ctx context.Context, data []byte) ([]byte, error) {
		var packet struct {
			Source  string        `json:"source"`
			Request nativeRequest `json:"request"`
		}
		if err := json.Unmarshal(data, &packet); err != nil {
			t.Fatal(err)
		}
		if packet.Source != nativeSource || packet.Request.Action.Value != value || packet.Request.Operation != "act" {
			t.Fatal("request or embedded source was changed")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > nativeTimeout {
			t.Fatal("native operation has no bounded deadline")
		}
		return []byte(`{"ok":true}`), nil
	}}
	if err := p.Act(context.Background(), Window{ID: "fixture"}, Action{Kind: "set_value", ElementID: "fixture-element", Value: value}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeRejectsUnsupportedInput(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		t.Fatal("invalid request reached native helper")
		return nil, nil
	}}
	for _, action := range []Action{
		{Kind: "click", ElementID: "x"}, {Kind: "invoke"},
		{Kind: "invoke", ElementID: "x", Value: "unexpected"},
		{Kind: "set_value", ElementID: "x", Value: strings.Repeat("a", nativeMaxValue+1)},
		{Kind: "toggle", ElementID: strings.Repeat("a", 1025)},
	} {
		if err := p.Act(context.Background(), Window{ID: "fixture"}, action); err == nil {
			t.Fatalf("accepted invalid action %+v", action.Kind)
		}
	}
	if _, err := p.Observe(context.Background(), Window{}); err == nil {
		t.Fatal("empty target accepted")
	}
}

func TestNativeErrorsAreBoundedAndSanitized(t *testing.T) {
	for _, test := range []struct {
		name, output, want string
	}{
		{"invalid", "sensitive native diagnostics", "invalid JSON"},
		{"unknown", `{"ok":false,"error":"secret diagnostics"}`, "native operation failed"},
		{"known", `{"ok":false,"error":"password_element"}`, "password_element"},
		{"trailing", `{"ok":true} {"sensitive":"data"}`, "trailing output"},
		{"oversize", strings.Repeat(" ", nativeMaxOutput+1), "output limit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
				return []byte(test.output), nil
			}}
			_, err := p.Windows(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		return nil, errors.New("sensitive process stderr")
	}}
	if _, err := p.Windows(context.Background()); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("process error not sanitized: %v", err)
	}
}

func TestNativeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		t.Fatal("canceled request reached helper")
		return nil, nil
	}}
	if _, err := p.Windows(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	p.run = func(ctx context.Context, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := p.Windows(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestNativeFocusViolationDisablesFurtherActions(t *testing.T) {
	calls := 0
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		calls++
		return []byte(`{"ok":false,"error":"focus_side_effect"}`), nil
	}}
	for i := 0; i < 2; i++ {
		err := p.Act(context.Background(), Window{ID: "fixture"}, Action{Kind: "invoke", ElementID: "fixture"})
		if err == nil || !strings.Contains(err.Error(), "focus_side_effect") {
			t.Fatalf("error = %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("unsafe provider received %d actions", calls)
	}
	p.run = func(context.Context, []byte) ([]byte, error) {
		return []byte(`{"ok":true,"observation":{"window":{"id":"fixture"},"elements":[{"id":"e","actions":["invoke"]}]}}`), nil
	}
	observation, err := p.Observe(context.Background(), Window{ID: "fixture"})
	if err != nil || observation.ControlError != "desktop_focus_side_effect" ||
		len(observation.Elements[0].Actions) != 0 {
		t.Fatalf("quarantined window still advertises actions: %+v %v", observation, err)
	}
}

func TestNativeObservationTargetMismatch(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		return []byte(`{"ok":true,"observation":{"window":{"id":"other"}}}`), nil
	}}
	if _, err := p.Observe(context.Background(), Window{ID: "fixture"}); err == nil {
		t.Fatal("accepted wrong target observation")
	}
}

func TestNativeWriterLimitCancels(t *testing.T) {
	canceled := false
	w := &nativeBoundedWriter{limit: 3, cancel: func() { canceled = true }}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("write = %d, %v", n, err)
	}
	if _, err := w.Write([]byte("d")); err == nil || !canceled || w.buffer.Len() != 3 {
		t.Fatal("output bound was not enforced")
	}
}

func TestNativeSourceHasNoPhysicalInputFallback(t *testing.T) {
	for _, forbidden := range []string{
		"SendInput", "SendKeys", "SetForegroundWindow", "SetFocus", "Clipboard",
		"keybd_event", "mouse_event", "WM_KEY", "PostMessage", ".SetValue(value)",
		".Invoke()", ".Toggle()", ".Select()", ".Expand()", ".Collapse()",
		"CopyFromScreen", "GetDesktopWindow", "GetDC(", "Invoke-Expression",
	} {
		if strings.Contains(nativeSource, forbidden) || strings.Contains(nativeBootstrap, forbidden) {
			t.Errorf("native helper contains forbidden API %q", forbidden)
		}
	}
	for _, required := range []string{"IsPassword", "GetProcessTimes", "TokenSID", "process.SessionId", "PrintWindow", "focus.Check()"} {
		if !strings.Contains(nativeSource, required) {
			t.Errorf("missing native guard %q", required)
		}
	}
}

func TestNativeTextWritesAreScopedAndNotUIAInputProxies(t *testing.T) {
	for _, required := range []string{
		"NativeEdit(element)", "pid != target.PID", "GetAncestor(handle, 2) != target.Handle",
		"SendTextTimeout(handle, 0x000C", "element.Current.IsPassword", "element_read_only",
	} {
		if !strings.Contains(nativeSource, required) {
			t.Errorf("missing scoped text guard %q", required)
		}
	}
}
