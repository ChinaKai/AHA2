package desktop

import (
	"context"
	_ "embed"
	"errors"
)

//go:embed native_preview.cs
var nativePreviewSource string

//go:embed native_worker.cs
var nativeWorkerSource string

var _ PreviewProvider = (*nativeProvider)(nil)

func (p *nativeProvider) ObservePreview(ctx context.Context, window Window, options FrameOptions) (Observation, error) {
	if window.ID == "" || len(window.ID) > 128 {
		return Observation{}, errors.New("desktop invalid_request")
	}
	if options.MaxWidth == 0 {
		options.MaxWidth = 1280
	}
	if options.MaxHeight == 0 {
		options.MaxHeight = 720
	}
	if options.Quality == 0 {
		options.Quality = 65
	}
	if options.MaxWidth < 320 || options.MaxWidth > 1920 || options.MaxHeight < 180 || options.MaxHeight > 1080 ||
		options.Quality < 35 || options.Quality > 85 {
		return Observation{}, errors.New("desktop invalid_request")
	}
	result, err := p.foregroundCall(ctx, nativeRequest{Operation: "foreground_preview", WindowID: window.ID, Frame: &options})
	if err == nil && (result.Observation.Window.ID != window.ID || result.Observation.Surface == "" ||
		result.Observation.Mime != "image/jpeg") {
		return Observation{}, errors.New("desktop observation target mismatch")
	}
	return result.Observation, err
}
