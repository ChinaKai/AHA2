//go:build windows

package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const nativeWorkerIdle = 30 * time.Second

type nativeWorkerPool struct {
	ctx        context.Context
	cancel     context.CancelFunc
	executable string
	systemDir  string
	idle       time.Duration
	capture    nativeWorkerLane
	action     nativeWorkerLane
	background nativeWorkerLane
}

type nativeWorkerLane struct {
	mu     sync.Mutex
	worker *nativeResident
	nextID uint64
}

type nativeResident struct {
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	job      windows.Handle
	input    io.WriteCloser
	scanner  *bufio.Scanner
	stderr   *nativeWorkerErrors
	done     chan struct{}
	source   string
	idle     *time.Timer
	lastUsed time.Time
}

// Diagnostics never leave the helper boundary. Per-request IDs avoid mixing
// delayed stderr markers from a completed request with the next input batch.
type nativeWorkerErrors struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	cancel context.CancelFunc
}

func (w *nativeWorkerErrors) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(data) > (16<<10)-w.buffer.Len() {
		w.cancel()
		return 0, errors.New("desktop helper output limit exceeded")
	}
	return w.buffer.Write(data)
}

func (w *nativeWorkerErrors) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer.Reset()
}

func (w *nativeWorkerErrors) interrupted(id uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	suffix := strconv.FormatUint(id, 10) + ";"
	return bytes.LastIndex(w.buffer.Bytes(), []byte("aha_input_begin;"+suffix)) >
		bytes.LastIndex(w.buffer.Bytes(), []byte("aha_input_end;"+suffix))
}

func NativeProvider() Provider {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil || session == 0 {
		return &nativeProvider{reason: "desktop_interactive_required"}
	}
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return &nativeProvider{reason: "desktop_native_unavailable"}
	}
	executable := filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe")
	if info, err := os.Stat(executable); err != nil || info.IsDir() {
		return &nativeProvider{reason: "desktop_powershell_required"}
	}
	pool := newNativeWorkerPool(executable, systemDir, nativeWorkerIdle)
	return &nativeProvider{run: pool.run, closeWorkers: pool.close}
}

func newNativeWorkerPool(executable, systemDir string, idle time.Duration) *nativeWorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &nativeWorkerPool{ctx: ctx, cancel: cancel, executable: executable, systemDir: systemDir, idle: idle}
}

func (pool *nativeWorkerPool) run(ctx context.Context, packet []byte) ([]byte, error) {
	var decoded struct {
		Source  string        `json:"source"`
		Request nativeRequest `json:"request"`
	}
	if len(packet) > 262144 || json.Unmarshal(packet, &decoded) != nil {
		return nil, errors.New("desktop invalid_request")
	}
	var lane *nativeWorkerLane
	laneName := "capture"
	switch decoded.Request.Operation {
	case "windows", "observe", "foreground_observe", "foreground_preview", "targets":
		lane = &pool.capture
	case "act", "foreground_act", "foreground_new_desktop", "target_select":
		lane, laneName = &pool.action, "action"
	case "background_select", "background_observe", "background_act":
		// Observation receipts and lifecycle invalidation live in the same worker.
		lane, laneName = &pool.background, "background"
	default:
		return nil, errors.New("desktop invalid_request")
	}
	if !lane.mu.TryLock() {
		return nil, failure("busy")
	}
	defer lane.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if pool.ctx.Err() != nil {
		return nil, errors.New("desktop provider closed")
	}
	// Standard requests share a fixed compilation. Internal fault-injection tests
	// can supply an alternate source, never exposed in the owner/agent protocol.
	source := decoded.Source
	if source == nativeSource {
		source += "\n" + foregroundNativeSource
	}
	source += "\n" + nativePreviewSource + "\n" + nativeWorkerSource
	if lane.worker != nil && lane.worker.source != source {
		lane.worker.stop()
		lane.worker = nil
	}
	if lane.worker != nil {
		select {
		case <-lane.worker.done:
			lane.worker.stop()
			lane.worker = nil
		default:
		}
	}
	if lane.worker == nil {
		worker, err := pool.start(ctx, source, laneName)
		if err != nil {
			return nil, err
		}
		lane.worker = worker
	}
	worker := lane.worker
	if worker.idle != nil {
		worker.idle.Stop()
	}
	lane.nextID++
	id := lane.nextID
	request, err := json.Marshal(struct {
		ID      uint64        `json:"id"`
		Request nativeRequest `json:"request"`
	}{id, decoded.Request})
	if err != nil {
		return nil, errors.New("desktop request encoding failed")
	}
	worker.stderr.reset()
	result, err := worker.exchange(ctx, request, id)
	if err != nil || ctx.Err() != nil || pool.ctx.Err() != nil {
		// Do not restart/replay this request: the target may have accepted input.
		worker.stop()
		lane.worker = nil
		if worker.stderr.interrupted(id) {
			cleanupForegroundInput(packet)
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			err = errors.New("desktop provider closed")
		}
		return nil, err
	}
	worker.lastUsed = time.Now()
	worker.idle = time.AfterFunc(pool.idle, func() {
		lane.mu.Lock()
		defer lane.mu.Unlock()
		if lane.worker == worker && time.Since(worker.lastUsed) >= pool.idle {
			worker.stop()
			lane.worker = nil
		}
	})
	return result, nil
}

func (pool *nativeWorkerPool) start(ctx context.Context, source, lane string) (*nativeResident, error) {
	lifetime, cancel := context.WithCancel(pool.ctx)
	cmd := exec.CommandContext(lifetime, pool.executable, "-NoLogo", "-NoProfile", "-NonInteractive",
		"-WindowStyle", "Hidden", "-EncodedCommand", encodeNativeCommand(nativeBootstrap))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=" + pool.systemDir}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		cancel()
		windows.CloseHandle(job)
		return nil, err
	}
	cmd.Cancel = func() error {
		_ = windows.TerminateJobObject(job, 1)
		return cmd.Process.Kill()
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		windows.CloseHandle(job)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		stdin.Close()
		windows.CloseHandle(job)
		return nil, err
	}
	worker := &nativeResident{cmd: cmd, cancel: cancel, job: job, input: stdin,
		scanner: bufio.NewScanner(stdout), done: make(chan struct{}), source: source}
	worker.scanner.Buffer(make([]byte, 64<<10), nativeMaxOutput+256)
	worker.stderr = &nativeWorkerErrors{cancel: cancel}
	cmd.Stderr = worker.stderr
	if err = cmd.Start(); err != nil {
		cancel()
		stdin.Close()
		stdout.Close()
		windows.CloseHandle(job)
		return nil, err
	}
	go func() { _ = cmd.Wait(); close(worker.done) }()
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
		_ = windows.CloseHandle(process)
	}
	if err != nil {
		worker.stop()
		return nil, err
	}
	// Compile only after Job containment; no request or task capability is setup data.
	setup, _ := json.Marshal(struct {
		Source string `json:"source"`
		Lane   string `json:"lane"`
	}{source, lane})
	ready, err := worker.exchange(ctx, setup, 0)
	if err != nil || !bytes.Equal(ready, []byte(`{"ok":true}`)) {
		worker.stop()
		return nil, errors.New("desktop helper startup failed")
	}
	return worker, nil
}

func (worker *nativeResident) exchange(ctx context.Context, request []byte, id uint64) ([]byte, error) {
	type reply struct {
		result []byte
		err    error
	}
	done := make(chan reply, 1)
	go func() {
		if _, err := worker.input.Write(append(request, '\n')); err != nil {
			done <- reply{err: err}
			return
		}
		if !worker.scanner.Scan() {
			done <- reply{err: errors.New("desktop helper ended without a response")}
			return
		}
		var frame struct {
			ID     *uint64         `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		data := worker.scanner.Bytes()
		if len(data) > nativeMaxOutput || json.Unmarshal(data, &frame) != nil || frame.ID == nil || *frame.ID != id ||
			len(frame.Result) == 0 {
			done <- reply{err: errors.New("desktop helper returned an invalid response")}
			return
		}
		done <- reply{result: frame.Result}
	}()
	select {
	case response := <-done:
		return response.result, response.err
	case <-ctx.Done():
		worker.cancel()
		_ = worker.input.Close()
		<-done
		return nil, ctx.Err()
	}
}

func (worker *nativeResident) stop() {
	if worker.idle != nil {
		worker.idle.Stop()
	}
	worker.cancel()
	_ = worker.input.Close()
	<-worker.done
	_ = windows.CloseHandle(worker.job)
}

func (pool *nativeWorkerPool) close() error {
	pool.cancel()
	for _, lane := range []*nativeWorkerLane{&pool.capture, &pool.action, &pool.background} {
		lane.mu.Lock()
		if lane.worker != nil {
			lane.worker.stop()
			lane.worker = nil
		}
		lane.mu.Unlock()
	}
	return nil
}

func encodeNativeCommand(script string) string {
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}
