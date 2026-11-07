package workspace

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type LocalRunner struct{}

// DetectedPlatform identifies the host this runner executes on. Without it,
// IsWindowsRunner treats a native Windows workspace as POSIX and tries to run
// sh/tail there; Claude transcript recovery then silently finds nothing.
func (LocalRunner) DetectedPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// StreamsInput reports that a local child's stdin stays open for
// Command.Input.
func (LocalRunner) StreamsInput() bool { return true }

func (runner LocalRunner) Run(parent context.Context, command Command, onLine LineHandler) (Result, error) {
	env := localEnvironment(os.Environ(), command.Env)
	command.Executable = resolveExecutable(command.Executable, env)
	return runProcess(parent, command, env, onLine)
}

// resolveExecutable finds executable inside the PATH of env. os/exec only
// consults the PATH of the current process when the command name has no path
// separator, so a directory added by localEnvironment would otherwise never be
// searched. Names that already carry a separator and lookups that fail fall
// through unchanged, keeping the original exec error visible.
func resolveExecutable(executable string, env []string) string {
	if executable == "" || strings.ContainsRune(executable, '/') ||
		(runtime.GOOS == "windows" && strings.ContainsRune(executable, '\\')) {
		return executable
	}
	if resolved, err := exec.LookPath(executable); err == nil {
		return resolved
	}
	for _, dir := range strings.Split(environmentValue(env, "PATH"), string(os.PathListSeparator)) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		for _, name := range executableNames(executable) {
			candidate := filepath.Join(dir, name)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() {
				continue
			}
			if runtime.GOOS == "windows" || info.Mode()&0o111 != 0 {
				return candidate
			}
		}
	}
	return executable
}

// executableNames expands a bare command name with the Windows PATHEXT
// candidates; other platforms use the name as is.
func executableNames(executable string) []string {
	names := []string{executable}
	if runtime.GOOS != "windows" {
		return names
	}
	extensions := strings.Split(strings.TrimSpace(os.Getenv("PATHEXT")), ";")
	if strings.TrimSpace(os.Getenv("PATHEXT")) == "" {
		extensions = []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	for _, extension := range extensions {
		extension = strings.TrimSpace(extension)
		if extension != "" {
			names = append(names, executable+extension)
		}
	}
	return names
}

// localEnvironment mirrors the PATH augmentation remoteScript already performs
// for SSH and WSL runners. A native host launched from systemd, a desktop
// session or a Windows tray inherits the process environment, which does not
// include the user-level bin directories an interactive shell adds from
// ~/.bashrc or ~/.profile. Without this, a CLI installed to ~/.local/bin (for
// example DSH from its installer) looks missing to workspace detection and to
// every Turn, even though the operator can run it in a terminal.
func localEnvironment(base []string, overrides map[string]string) []string {
	env := mergeEnvironment(base, nil)
	if dirs := localPathDirectories(); len(dirs) > 0 {
		env = mergeEnvironment(env, map[string]string{"PATH": prependPathDirectories(environmentValue(env, "PATH"), dirs)})
	}
	// An explicit command environment always wins over the inherited PATH.
	return mergeEnvironment(env, overrides)
}

// localPathDirectories lists user-level binary directories that are normally
// only added by an interactive shell profile.
func localPathDirectories() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	dirs := []string{filepath.Join(home, ".local", "bin")}
	if runtime.GOOS == "windows" {
		if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
			dirs = append(dirs, filepath.Join(appData, "npm"))
		}
		return dirs
	}
	dirs = append(dirs, filepath.Join(home, "bin"))
	matches, err := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin"))
	if err == nil {
		sort.Strings(matches)
		dirs = append(dirs, matches...)
	}
	return dirs
}

// environmentValue reads an environment entry case-insensitively.
func environmentValue(env []string, key string) string {
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

// prependPathDirectories puts dirs in front of value, dropping duplicates and
// keeping the caller's original ordering after them.
func prependPathDirectories(value string, dirs []string) string {
	seen := make(map[string]bool, len(dirs))
	ordered := make([]string, 0, len(dirs)+8)
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		ordered = append(ordered, dir)
	}
	for _, dir := range strings.Split(value, string(os.PathListSeparator)) {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		ordered = append(ordered, dir)
	}
	return strings.Join(ordered, string(os.PathListSeparator))
}

// processPipeDrainTimeout bounds how long runProcess waits for a descendant
// that inherited the child's stdout/stderr handles to close them after the
// direct child exits. When the grace expires the runner closes its read ends;
// a drainer still blocked on a full spool means unread pipe data was lost,
// while a drainer waiting on an empty pipe has already captured everything.
const processPipeDrainTimeout = 3 * time.Second

// processOutputSpoolLimit caps how many unread bytes each stream may buffer
// before its pipe drainer is backpressured. The spool decouples the drainer
// from the line handler, so a busy handler does not discard the tail; beyond
// this backlog the command is deliberately slowed down instead of growing
// memory without bound. If a read end must be closed while the drainer is
// still blocked writing here, the capture is reported as truncated.
const processOutputSpoolLimit = 4 * 1024 * 1024

// errProcessOutputTruncated reports that a process pipe was closed while the
// consumer was backpressured, so output still in that pipe was lost. Callers
// must not mistake this for a successful run with complete output.
var errProcessOutputTruncated = fmt.Errorf("workspace: process output truncated after backpressure stall: %w", exec.ErrWaitDelay)

// runProcess executes a command on the local Windows host with the given
// environment and streams stdout lines through onLine.
func runProcess(parent context.Context, command Command, env []string, onLine LineHandler) (Result, error) {
	ctx := parent
	cancel := func() {}
	if command.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, command.Timeout)
	}
	defer cancel()
	start := time.Now()
	process := exec.CommandContext(ctx, command.Executable, command.Args...)
	if command.KillTree {
		configureProcessTree(process)
	}
	process.Dir = command.Dir
	process.Env = env
	var stdinPipe io.WriteCloser
	if command.Input == nil {
		process.Stdin = strings.NewReader(command.Stdin)
	} else {
		// Wait closes this pipe once the child exits, which also unblocks a
		// feeder stuck writing to a child that stopped reading.
		var err error
		if stdinPipe, err = process.StdinPipe(); err != nil {
			return Result{}, err
		}
	}
	// Own the stdout/stderr pipes instead of letting os/exec copy them. The
	// runner has to decide when a pipe held open by a descendant may be closed
	// and whether the drainer was idle or blocked on a full spool at that
	// moment; os/exec's WaitDelay exposes neither, and it also discards the
	// delay error when the process exits non-zero.
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return Result{}, err
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return Result{}, err
	}
	process.Stdout = stdoutWrite
	process.Stderr = stderrWrite
	// No c.Pipe is managed by exec here, so WaitDelay only bounds a child that
	// ignores cancellation; it cannot cut off our output pipes.
	process.WaitDelay = processPipeDrainTimeout
	if err := process.Start(); err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		_ = stderrRead.Close()
		_ = stderrWrite.Close()
		return Result{}, err
	}
	// The child inherited the write ends; keeping the parent's copies open
	// would keep the drainers from ever seeing EOF.
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	stdoutSpool := newProcessSpool(processOutputSpoolLimit)
	stderrSpool := newProcessSpool(processOutputSpoolLimit)
	stdoutDrainer := startProcessDrainer(stdoutRead, stdoutSpool)
	stderrDrainer := startProcessDrainer(stderrRead, stderrSpool)
	stdout, stderr := newOutputBuffer(command.OutputLimit), newOutputBuffer(command.OutputLimit)
	var wait sync.WaitGroup
	wait.Add(2)
	go scanOutput(stdoutSpool, stdout, onLine, &wait)
	go scanOutput(stderrSpool, stderr, nil, &wait)
	exited := make(chan struct{})
	feederDone := make(chan struct{})
	if stdinPipe != nil {
		go feedProcessInput(stdinPipe, command.Stdin, command.Input, exited, feederDone)
	} else {
		close(feederDone)
	}
	processErr := process.Wait()
	close(exited)
	<-feederDone
	// Wait returns as soon as the direct child exits. Give the drainers a
	// bounded window to reach EOF; a descendant holding the pipes keeps them
	// open, so close the read ends after the grace period. If a drainer was
	// blocked writing into a full spool at that point, unread pipe data is
	// lost and the result must be reported as truncated regardless of the
	// process exit code.
	truncated := finishProcessDrainers(processPipeDrainTimeout, stdoutDrainer, stderrDrainer)
	stdoutSpool.CloseWrite()
	stderrSpool.CloseWrite()
	wait.Wait()
	if errors.Is(processErr, exec.ErrWaitDelay) {
		processErr = nil
	}
	result := Result{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start)}
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if truncated {
		return result, errProcessOutputTruncated
	}
	if processErr != nil {
		var exitErr *exec.ExitError
		if errors.As(processErr, &exitErr) {
			return result, nil
		}
		return result, processErr
	}
	return result, nil
}

// feedProcessInput writes initial and then every value from input to a
// running child's stdin. It closes stdin when input is closed and stops when
// the child has exited or stopped accepting writes; a value that could not be
// written is dropped, so callers confirm delivery from the child's output
// rather than from this write.
func feedProcessInput(stdin io.WriteCloser, initial string, input <-chan string, exited <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	defer func() { _ = stdin.Close() }()
	if initial != "" {
		if _, err := io.WriteString(stdin, initial); err != nil {
			return
		}
	}
	for {
		select {
		case <-exited:
			return
		case value, ok := <-input:
			if !ok {
				return
			}
			if _, err := io.WriteString(stdin, value); err != nil {
				return
			}
		}
	}
}

func scanOutput(reader io.Reader, destination io.Writer, handler LineHandler, wait *sync.WaitGroup) {
	defer wait.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = io.WriteString(destination, line+"\n")
		if handler != nil {
			handler(line)
		}
	}
	// A line longer than the scanner's maximum stops the scan loop, but the
	// pipe drainer may still be delivering data. Keep consuming so a
	// backpressured drainer (and the command behind it) can finish instead of
	// blocking forever.
	_, _ = io.Copy(io.Discard, reader)
}

// processSpool is a bounded in-memory byte pipe. Writes block only when the
// backlog reaches limit, so the pipe drainer never waits for a slow line
// handler; reads block until data arrives or CloseWrite is called.
type processSpool struct {
	mu            sync.Mutex
	ready         *sync.Cond
	chunks        [][]byte
	offset        int
	queued        int
	limit         int
	writerWaiting bool
	closed        bool
}

func newProcessSpool(limit int) *processSpool {
	spool := &processSpool{limit: limit}
	spool.ready = sync.NewCond(&spool.mu)
	return spool
}

func (spool *processSpool) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		spool.mu.Lock()
		for !spool.closed && spool.queued >= spool.limit {
			spool.writerWaiting = true
			spool.ready.Wait()
			spool.writerWaiting = false
		}
		if spool.closed {
			spool.mu.Unlock()
			return written, io.ErrClosedPipe
		}
		size := len(data)
		if space := spool.limit - spool.queued; size > space {
			size = space
		}
		chunk := make([]byte, size)
		copy(chunk, data[:size])
		spool.chunks = append(spool.chunks, chunk)
		spool.queued += size
		written += size
		data = data[size:]
		spool.ready.Broadcast()
		spool.mu.Unlock()
	}
	return written, nil
}

func (spool *processSpool) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	spool.mu.Lock()
	defer spool.mu.Unlock()
	for spool.queued == 0 && !spool.closed {
		spool.ready.Wait()
	}
	if spool.queued == 0 {
		return 0, io.EOF
	}
	read := 0
	for read < len(buffer) && len(spool.chunks) > 0 {
		chunk := spool.chunks[0]
		copied := copy(buffer[read:], chunk[spool.offset:])
		read += copied
		spool.offset += copied
		spool.queued -= copied
		if spool.offset == len(chunk) {
			spool.chunks[0] = nil
			spool.chunks = spool.chunks[1:]
			spool.offset = 0
		}
	}
	spool.ready.Broadcast()
	return read, nil
}

// WriterWaiting reports whether a writer is currently blocked because the
// backlog reached the limit.
func (spool *processSpool) WriterWaiting() bool {
	spool.mu.Lock()
	defer spool.mu.Unlock()
	return spool.writerWaiting
}

// CloseWrite marks EOF for readers once the pipe drainer is done.
func (spool *processSpool) CloseWrite() {
	spool.mu.Lock()
	spool.closed = true
	spool.ready.Broadcast()
	spool.mu.Unlock()
}

// processDrainer copies one owned process pipe into a spool. Keeping the read
// side here lets runProcess decide when a descendant-held pipe may be closed
// and whether closing it would discard unread output.
type processDrainer struct {
	source *os.File
	spool  *processSpool
	wg     sync.WaitGroup
	mu     sync.Mutex
	err    error
	closed bool
}

func startProcessDrainer(source *os.File, spool *processSpool) *processDrainer {
	drainer := &processDrainer{source: source, spool: spool}
	drainer.wg.Add(1)
	go drainer.run()
	return drainer
}

func (drainer *processDrainer) run() {
	defer drainer.wg.Done()
	// Release the read end even on a normal EOF, when closeSource was not
	// called; the finalizer would otherwise be the only owner.
	defer func() { _ = drainer.source.Close() }()
	buffer := make([]byte, 32*1024)
	for {
		read, readErr := drainer.source.Read(buffer)
		if read > 0 {
			if _, writeErr := drainer.spool.Write(buffer[:read]); writeErr != nil {
				drainer.recordError(writeErr)
				return
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				drainer.recordError(readErr)
			}
			return
		}
	}
}

func (drainer *processDrainer) recordError(err error) {
	drainer.mu.Lock()
	defer drainer.mu.Unlock()
	if drainer.closed {
		return
	}
	if drainer.err == nil {
		drainer.err = err
	}
}

func (drainer *processDrainer) closeSource() {
	drainer.mu.Lock()
	drainer.closed = true
	drainer.mu.Unlock()
	_ = drainer.source.Close()
}

func (drainer *processDrainer) failed() bool {
	drainer.mu.Lock()
	defer drainer.mu.Unlock()
	return drainer.err != nil
}

// finishProcessDrainers waits for both pipes to reach EOF, or closes their
// read ends after grace when a descendant keeps the write ends open. It
// reports truncation when a pipe is closed while its drainer is blocked on a
// full spool (unread bytes are still in that pipe), and when a drainer fails
// for any reason other than our own close.
func finishProcessDrainers(grace time.Duration, drainers ...*processDrainer) (truncated bool) {
	done := make(chan *processDrainer, len(drainers))
	for _, drainer := range drainers {
		go func(drainer *processDrainer) {
			drainer.wg.Wait()
			done <- drainer
		}(drainer)
	}
	pending := make(map[*processDrainer]bool, len(drainers))
	for _, drainer := range drainers {
		pending[drainer] = true
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for len(pending) > 0 {
		select {
		case drainer := <-done:
			delete(pending, drainer)
			if drainer.failed() {
				truncated = true
			}
		case <-timer.C:
			for drainer := range pending {
				if drainer.spool.WriterWaiting() {
					truncated = true
				}
				drainer.closeSource()
			}
			// A drainer blocked on a full spool resumes once the handler
			// catches up; Run must not return before that handler call ends.
			for len(pending) > 0 {
				drainer := <-done
				delete(pending, drainer)
				if drainer.failed() {
					truncated = true
				}
			}
			return truncated
		}
	}
	return truncated
}

// outputBuffer keeps only the last limit bytes written to it. limit <= 0
// disables the cap.
//
// bytes.Buffer is embedded for its read-side methods, but every write method it
// promotes is overridden below: io.WriteString and io.Copy select WriteString
// and ReadFrom through interfaces before falling back to Write, so an inherited
// method would append past the cap.
type outputBuffer struct {
	bytes.Buffer
	limit int
}

func newOutputBuffer(limit int) *outputBuffer { return &outputBuffer{limit: limit} }

func (buffer *outputBuffer) Write(value []byte) (int, error) {
	consumed := len(value)
	skip := buffer.retainTail(consumed)
	_, err := buffer.Buffer.Write(value[skip:])
	return consumed, err
}

func (buffer *outputBuffer) WriteString(value string) (int, error) {
	consumed := len(value)
	skip := buffer.retainTail(consumed)
	_, err := buffer.Buffer.WriteString(value[skip:])
	return consumed, err
}

func (buffer *outputBuffer) WriteByte(value byte) error {
	buffer.retainTail(1)
	return buffer.Buffer.WriteByte(value)
}

func (buffer *outputBuffer) WriteRune(value rune) (int, error) {
	return buffer.Write([]byte(string(value)))
}

// ReadFrom drains reader completely while enforcing the same cap, so
// io.Copy(destination, reader) cannot bypass OutputLimit.
func (buffer *outputBuffer) ReadFrom(reader io.Reader) (int64, error) {
	var consumed int64
	chunk := make([]byte, 32*1024)
	for {
		read, readErr := reader.Read(chunk)
		if read > 0 {
			written, writeErr := buffer.Write(chunk[:read])
			consumed += int64(written)
			if writeErr != nil {
				return consumed, writeErr
			}
		}
		if readErr == io.EOF {
			return consumed, nil
		}
		if readErr != nil {
			return consumed, readErr
		}
	}
}

// retainTail makes room for size more bytes and reports how many leading bytes
// of the incoming value to skip, so only the last limit bytes survive.
func (buffer *outputBuffer) retainTail(size int) int {
	if buffer.limit <= 0 {
		return 0
	}
	if size >= buffer.limit {
		buffer.Buffer.Reset()
		return size - buffer.limit
	}
	if overflow := buffer.Len() + size - buffer.limit; overflow > 0 {
		buffer.discardPrefix(overflow)
	}
	return 0
}

func (buffer *outputBuffer) discardPrefix(count int) {
	remaining := append([]byte(nil), buffer.Buffer.Bytes()[count:]...)
	buffer.Buffer.Reset()
	_, _ = buffer.Buffer.Write(remaining)
}

func mergeEnvironment(base []string, overrides map[string]string) []string {
	index := make(map[string]int, len(base))
	result := append([]string(nil), base...)
	for position, item := range result {
		if key, _, ok := strings.Cut(item, "="); ok {
			index[strings.ToUpper(key)] = position
		}
	}
	for key, value := range overrides {
		item := key + "=" + value
		if position, ok := index[strings.ToUpper(key)]; ok {
			result[position] = item
		} else {
			result = append(result, item)
		}
	}
	return result
}
