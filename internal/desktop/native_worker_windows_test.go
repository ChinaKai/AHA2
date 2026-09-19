//go:build windows

package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func streamFixture(t *testing.T, ctx context.Context) Window {
	t.Helper()
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	// Reuse the non-activating fixture; only its own viewport is enlarged.
	script := strings.Replace(fixtureScript, "new Size(460, 380)", "new Size(1600, 900)", 1)
	script = strings.Replace(script, `button.Click += delegate { edit.Text = "invoked-fixture"; };`,
		`int invocations = 0; button.Click += delegate { edit.Text = (++invocations).ToString(); };`, 1)
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe"),
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-WindowStyle", "Hidden",
		"-EncodedCommand", encodeNativeCommand(script))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=" + dir}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(stdout)
	var window Window
	if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &window) != nil || window.ID == "" {
		t.Fatal("non-activating preview fixture did not start")
	}
	return window
}

func nativeTestPool(t *testing.T, idle time.Duration) (*nativeProvider, *nativeWorkerPool) {
	t.Helper()
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	pool := newNativeWorkerPool(filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe"), dir, idle)
	p := &nativeProvider{run: pool.run, closeWorkers: pool.close}
	t.Cleanup(func() { _ = p.Close() })
	return p, pool
}

func residentPID(lane *nativeWorkerLane) int {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.worker == nil {
		return 0
	}
	return lane.worker.cmd.Process.Pid
}

func TestNativeResidentPreviewPerformance(t *testing.T) {
	if os.Getenv("AHA_NATIVE_STREAM_TEST") != "1" {
		t.Skip("opt-in; only screenshots a newly created non-activating fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	window := streamFixture(t, ctx)
	p, pool := nativeTestPool(t, nativeWorkerIdle)
	before := fixtureInputState()
	start := time.Now()
	first, err := p.ObservePreview(ctx, window, FrameOptions{})
	cold := time.Since(start)
	if err != nil || first.Image == "" {
		t.Fatalf("cold JPEG capture: %v %s", err, first.CaptureError)
	}
	pid := residentPID(&pool.capture)
	t.Logf("cold resident preview=%s; logical=%dx%d", cold.Round(time.Microsecond), first.Width, first.Height)
	for _, options := range []FrameOptions{
		{MaxWidth: 1280, MaxHeight: 720, Quality: 65},
		{MaxWidth: 640, MaxHeight: 360, Quality: 45},
		{MaxWidth: 320, MaxHeight: 180, Quality: 35},
		{MaxWidth: 1920, MaxHeight: 1080, Quality: 85},
	} {
		var durations []time.Duration
		var imageBytes int
		var last Observation
		for i := 0; i < 12; i++ {
			start = time.Now()
			last, err = p.ObservePreview(ctx, window, options)
			durations = append(durations, time.Since(start))
			if err != nil || last.Image == "" {
				t.Fatalf("warm JPEG capture %d: %v %s", i, err, last.CaptureError)
			}
			if last.Surface != first.Surface || last.Width != first.Width || last.Height != first.Height ||
				last.Mime != "image/jpeg" || last.PreviewWidth > options.MaxWidth || last.PreviewHeight > options.MaxHeight ||
				last.PreviewWidth > last.Width || last.PreviewHeight > last.Height {
				t.Fatal("preview changed logical geometry or upscaled bitmap")
			}
			data, err := base64.StdEncoding.DecodeString(last.Image)
			if err != nil {
				t.Fatal(err)
			}
			image, err := jpeg.DecodeConfig(bytes.NewReader(data))
			if err != nil || image.Width != last.PreviewWidth || image.Height != last.PreviewHeight ||
				image.Width*last.Height != image.Height*last.Width {
				t.Fatalf("JPEG shape/aspect mismatch: %+v, %v", image, err)
			}
			imageBytes = len(data)
		}
		slices.Sort(durations)
		t.Logf("warm n=%d JPEG %dx%d q=%d bytes=%d median=%s p95=%s",
			len(durations), last.PreviewWidth, last.PreviewHeight, options.Quality, imageBytes,
			durations[len(durations)/2].Round(time.Microsecond), durations[len(durations)-1].Round(time.Microsecond))
	}
	if residentPID(&pool.capture) != pid {
		t.Fatal("warm observations did not reuse the capture worker")
	}
	png, err := p.ObserveForeground(ctx, window)
	if err != nil || png.Image == "" {
		t.Fatalf("legacy PNG capture failed: %v", err)
	}
	full, _ := base64.StdEncoding.DecodeString(png.Image)
	t.Logf("legacy full-size PNG=%d bytes; resident capture PID reused for preview and legacy", len(full))
	if fixtureInputState() != before {
		t.Fatal("preview changed actual foreground/focus")
	}
}

func TestNativeResidentCloseInterruptsPendingReply(t *testing.T) {
	if os.Getenv("AHA_NATIVE_STREAM_TEST") != "1" {
		t.Skip("opt-in; malformed target and delayed response only")
	}
	p, pool := nativeTestPool(t, nativeWorkerIdle)
	source := nativeSource + "\n" + foregroundNativeSource + "\n" + nativePreviewSource + "\n" +
		strings.Replace(nativeWorkerSource, "Console.Out.Write(result);", "Thread.Sleep(10000); Console.Out.Write(result);", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	worker, err := pool.start(ctx, source, "capture")
	if err != nil {
		t.Fatal(err)
	}
	pool.capture.mu.Lock()
	pool.capture.worker = worker
	pool.capture.mu.Unlock()
	// Hold the lane exactly like a request while Close cancels the process lifetime.
	pool.capture.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := worker.exchange(ctx, []byte(`{"id":1,"request":{"operation":"observe","window_id":"invalid-fixture-id"}}`), 1)
		pool.capture.mu.Unlock()
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("Close let a pending reply succeed")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Close waited for the slow operation")
	}
	select {
	case <-worker.done:
	default:
		t.Fatal("Close did not reap pending worker")
	}
}

func TestNativeResidentErrorMarkersAreRequestScoped(t *testing.T) {
	w := &nativeWorkerErrors{cancel: func() {}}
	w.Write([]byte("aha_input_begin;1;aha_input_end;1;aha_input_begin;2;aha_input_end;1;"))
	if w.interrupted(1) || !w.interrupted(2) {
		t.Fatal("delayed old markers changed the active request state")
	}
	w.Write([]byte("aha_input_end;2;"))
	if w.interrupted(2) {
		t.Fatal("completed batch still appears active")
	}
}

func TestNativeResidentContentionIsRetryable(t *testing.T) {
	p, pool := nativeTestPool(t, nativeWorkerIdle)
	pool.capture.mu.Lock()
	_, err := p.Windows(context.Background())
	pool.capture.mu.Unlock()
	if err == nil || ErrorCode(err) != "desktop_busy" {
		t.Fatalf("enumeration/capture contention lost its retryable error: %v", err)
	}
	if residentPID(&pool.capture) != 0 {
		t.Fatal("contention launched a second worker")
	}
}

func TestNativeResidentRejectsMalformedReplies(t *testing.T) {
	if os.Getenv("AHA_NATIVE_STREAM_TEST") != "1" {
		t.Skip("opt-in; protocol-only helper with no desktop API")
	}
	for _, test := range []struct{ name, reply string }{
		{"wrong_id", `Console.Out.WriteLine("{\"id\":9,\"result\":{\"ok\":true}}");`},
		{"missing_id", `Console.Out.WriteLine("{\"result\":{\"ok\":true}}");`},
		{"oversize", `Console.Out.WriteLine(new string('x', 13 * 1024 * 1024));`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, pool := nativeTestPool(t, nativeWorkerIdle)
			source := `using System; using System.Threading; namespace AHADesktop { public static class Native {
				public static void Serve(string lane) {
					Console.Out.WriteLine("{\"id\":0,\"result\":{\"ok\":true}}"); Console.Out.Flush();
					Console.In.ReadLine(); ` + test.reply + ` Console.Out.Flush(); Thread.Sleep(10000);
				} } }`
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			worker, err := pool.start(ctx, source, "capture")
			if err != nil {
				t.Fatal(err)
			}
			defer worker.stop()
			if _, err := worker.exchange(ctx, []byte(`{"id":1,"request":{}}`), 1); err == nil {
				t.Fatal("malformed helper reply accepted")
			}
		})
	}
}
