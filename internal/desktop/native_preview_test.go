package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestNativePreviewOptionsAndLogicalCoordinates(t *testing.T) {
	p := &nativeProvider{run: func(_ context.Context, input []byte) ([]byte, error) {
		var packet struct {
			Request nativeRequest `json:"request"`
		}
		if err := json.Unmarshal(input, &packet); err != nil {
			t.Fatal(err)
		}
		if packet.Request.Operation != "foreground_preview" || packet.Request.Frame == nil ||
			*packet.Request.Frame != (FrameOptions{MaxWidth: 1280, MaxHeight: 720, Quality: 65}) {
			t.Fatalf("incorrect default preview request: %+v", packet.Request)
		}
		return []byte(`{"ok":true,"observation":{"window":{"id":"fixture"},"surface":"trusted",
			"width":2560,"height":1440,"preview_width":1280,"preview_height":720,"mime":"image/jpeg"}}`), nil
	}}
	observation, err := p.ObservePreview(context.Background(), Window{ID: "fixture"}, FrameOptions{})
	if err != nil || observation.Width != 2560 || observation.Height != 1440 ||
		observation.PreviewWidth != 1280 || observation.PreviewHeight != 720 {
		t.Fatalf("logical coordinates not preserved: %+v, %v", observation, err)
	}
}

func TestNativePreviewRejectsInvalidOptions(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		t.Fatal("invalid preview reached helper")
		return nil, nil
	}}
	for _, frame := range []FrameOptions{
		{MaxWidth: 319}, {MaxWidth: 1921}, {MaxHeight: 179}, {MaxHeight: 1081}, {Quality: 34}, {Quality: 86},
	} {
		if _, err := p.ObservePreview(context.Background(), Window{ID: "fixture"}, frame); err == nil {
			t.Fatalf("invalid frame accepted: %+v", frame)
		}
	}
}

func TestNativePreviewAndWorkerSourceBoundaries(t *testing.T) {
	if strings.Contains(nativePreviewSource, "SendInput(") || strings.Contains(nativePreviewSource, "Activate(") {
		t.Fatal("preview unexpectedly controls input")
	}
	for _, code := range []string{"quality < 35", "quality > 85", "maxWidth > 1920", "maxHeight > 1080",
		"Math.Min(1.0", "scaled.Save", "ReadSurface(id, true)", "ReconcileCapture(surface, after",
		"CaptureActions(surface, image)"} {
		if !strings.Contains(nativePreviewSource, code) {
			t.Errorf("missing preview guard %q", code)
		}
	}
	for _, code := range []string{"id <= previous", "lane == \"capture\" && !capture", "line.Length >= 131072"} {
		if !strings.Contains(nativeWorkerSource, code) {
			t.Errorf("missing resident protocol guard %q", code)
		}
	}
}

func TestNativeCloseCallback(t *testing.T) {
	called := false
	p := &nativeProvider{closeWorkers: func() error { called = true; return nil }}
	if err := p.Close(); err != nil || !called {
		t.Fatal("Close did not close native workers")
	}
	if err := (&nativeProvider{}).Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCaptureContentionKeepsTypedError(t *testing.T) {
	p := &nativeProvider{run: func(context.Context, []byte) ([]byte, error) {
		return nil, failure("busy")
	}}
	_, err := p.ObservePreview(context.Background(), Window{ID: "fixture"}, FrameOptions{})
	if err == nil || ErrorCode(err) != "desktop_busy" {
		t.Fatalf("capture contention became a native failure: %v", err)
	}
}
