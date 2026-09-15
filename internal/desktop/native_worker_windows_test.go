//go:build windows

package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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

func TestNativeResidentCancellationPriorityAndReap(t *testing.T) {
	if os.Getenv("AHA_NATIVE_STREAM_TEST") != "1" {
		t.Skip("opt-in; owned non-activating fixture, scoped background action only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	window := streamFixture(t, ctx)
	p, pool := nativeTestPool(t, nativeWorkerIdle)
	o, err := p.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	var edit string
	for _, element := range o.Elements {
		if element.Value == "initial-fixture" {
			edit = element.ID
		}
	}
	if edit == "" {
		t.Fatal("missing owned fixture edit")
	}
	if err := p.Act(ctx, window, Action{Kind: "set_value", ElementID: edit, Value: "action-worker-warmed"}); err != nil {
		t.Fatal(err)
	}
	capturePID, actionPID := residentPID(&pool.capture), residentPID(&pool.action)
	if capturePID == actionPID {
		t.Fatal("capture and action share a process")
	}
	slow := strings.Replace(foregroundNativeSource, "static object ObserveForegroundNative(string id) {",
		`static object ObserveForegroundNative(string id) { Console.Error.Write("fixture_capture_wait;"); Console.Error.Flush(); Thread.Sleep(5000);`, 1)
	if slow == foregroundNativeSource {
		t.Fatal("capture fault instrumentation did not apply")
	}
	_, err = p.callSource(ctx, nativeRequest{Operation: "foreground_preview", WindowID: window.ID,
		Frame: &FrameOptions{MaxWidth: 640, MaxHeight: 360, Quality: 45}}, nativeSource+"\n"+slow)
	if err != nil {
		t.Fatal(err)
	}
	pool.capture.mu.Lock()
	worker := pool.capture.worker
	pool.capture.mu.Unlock()
	blocked, stop := context.WithCancel(ctx)
	defer stop()
	waiting := make(chan error, 1)
	go func() {
		_, err := p.callSource(blocked, nativeRequest{Operation: "foreground_observe", WindowID: window.ID}, nativeSource+"\n"+slow)
		waiting <- err
	}()
	ready := false
	for i := 0; i < 400; i++ {
		worker.stderr.mu.Lock()
		ready = bytes.Contains(worker.stderr.buffer.Bytes(), []byte("fixture_capture_wait;"))
		worker.stderr.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		stop()
		<-waiting
		t.Fatal("capture did not reach its blocking fixture")
	}
	start := time.Now()
	if err := p.Act(ctx, window, Action{Kind: "set_value", ElementID: edit, Value: "independent-action"}); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("input waited behind capture: %s", elapsed)
	}
	if residentPID(&pool.action) != actionPID {
		t.Fatal("capture source/restart replaced the action worker")
	}
	stop()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("capture cancel error=%v", err)
	}
	select {
	case <-worker.done:
	default:
		t.Fatal("canceled capture worker was not reaped")
	}
	if residentPID(&pool.capture) != 0 {
		t.Fatal("canceled capture worker remained installed")
	}
	check, err := p.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for _, element := range check.Elements {
		changed = changed || element.ID == edit && element.Value == "independent-action"
	}
	if !changed {
		t.Fatal("independent background action did not update its target")
	}
	t.Logf("warm scoped input completed in %s while capture blocked for 5s; canceled capture reaped and fresh request restarted",
		elapsed.Round(time.Microsecond))
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ObservePreview(ctx, window, FrameOptions{}); err == nil {
		t.Fatal("closed provider started another worker")
	}
	if residentPID(&pool.capture) != 0 || residentPID(&pool.action) != 0 {
		t.Fatal("Close did not reap both workers")
	}
}

func TestNativeResidentIdleReap(t *testing.T) {
	if os.Getenv("AHA_NATIVE_STREAM_TEST") != "1" {
		t.Skip("opt-in resident lifecycle check; malformed target only")
	}
	p, pool := nativeTestPool(t, 100*time.Millisecond)
	_, _ = p.Observe(context.Background(), Window{ID: "invalid-fixture-id"})
	pool.capture.mu.Lock()
	worker := pool.capture.worker
	pool.capture.mu.Unlock()
	if worker == nil {
		t.Fatal("worker was not started")
	}
	select {
	case <-worker.done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle worker did not terminate")
	}
	if residentPID(&pool.capture) != 0 {
		t.Fatal("idle worker was not removed")
	}
	t.Log("idle reap terminated and removed the resident process")
}

func TestNativeResidentUncertainActionIsNotReplayed(t *testing.T) {
	if os.Getenv("AHA_NATIVE_STREAM_TEST") != "1" {
		t.Skip("opt-in; one background command to the owned non-activating fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	window := streamFixture(t, ctx)
	p, pool := nativeTestPool(t, nativeWorkerIdle)
	o, err := p.Observe(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	var button, edit string
	for _, element := range o.Elements {
		if element.Name == "Fixture Invoke" {
			button = element.ID
		}
		if element.Value == "initial-fixture" {
			edit = element.ID
		}
	}
	if button == "" || edit == "" {
		t.Fatal("missing owned fixture controls")
	}
	fault := strings.Replace(nativeSource, "Act(target, (Dictionary<string, object>)action);",
		`Act(target, (Dictionary<string, object>)action); Console.Error.Write("fixture_action_accepted;"); Console.Error.Flush(); Thread.Sleep(10000);`, 1)
	if fault == nativeSource {
		t.Fatal("action instrumentation did not apply")
	}
	// Start the instrumented action worker without mutating any target.
	_, err = p.callSource(ctx, nativeRequest{Operation: "act", WindowID: "invalid-fixture-id"},
		fault+"\n"+foregroundNativeSource)
	if err == nil || !strings.Contains(err.Error(), "stale_target") {
		t.Fatalf("fault helper startup=%v", err)
	}
	pool.action.mu.Lock()
	worker := pool.action.worker
	pool.action.mu.Unlock()
	uncertain, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() {
		_, err := p.callSource(uncertain, nativeRequest{Operation: "act", WindowID: window.ID,
			Action: Action{Kind: "invoke", ElementID: button}}, fault+"\n"+foregroundNativeSource)
		done <- err
	}()
	accepted := false
	for i := 0; i < 300; i++ {
		worker.stderr.mu.Lock()
		accepted = bytes.Contains(worker.stderr.buffer.Bytes(), []byte("fixture_action_accepted;"))
		worker.stderr.mu.Unlock()
		if accepted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) || !accepted {
		t.Fatalf("uncertain action outcome=%v accepted=%t", err, accepted)
	}
	if residentPID(&pool.action) != 0 {
		t.Fatal("failed action worker was restarted speculatively")
	}
	checkCount := func(want string) {
		t.Helper()
		o, err := p.Observe(ctx, window)
		if err != nil {
			t.Fatal(err)
		}
		for _, element := range o.Elements {
			if element.ID == edit && element.Value == want {
				return
			}
		}
		t.Fatalf("invocation counter is not %q", want)
	}
	checkCount("1")
	// Only a fresh explicit action is allowed to start the replacement worker.
	if err := p.Act(ctx, window, Action{Kind: "invoke", ElementID: button}); err != nil {
		t.Fatal(err)
	}
	checkCount("2")
	t.Log("accepted-but-canceled action executed exactly once; only a fresh request started the replacement worker")
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
