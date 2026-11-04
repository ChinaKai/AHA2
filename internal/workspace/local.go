package workspace

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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

// processPipeDrainTimeout bounds how long Wait may block on stdout/stderr
// pipes that a descendant keeps open after the direct child exits. The timer
// starts when the child exits, so commands that close their own output still
// return as soon as it has been drained. Without this bound a daemonized
// helper holding the inherited handles would hang the runner forever.
const processPipeDrainTimeout = 3 * time.Second

// processOutputSpoolLimit caps how many unread bytes each stream may buffer
// before the os/exec copying goroutine is backpressured. The spool decouples
// the copying goroutine from the line handler, so a handler that is busy for
// longer than processPipeDrainTimeout does not lose the tail: WaitDelay then
// only fires when a descendant keeps the child's output handles open. Beyond
// this backlog the command is deliberately slowed down instead of growing
// memory without bound.
const processOutputSpoolLimit = 4 * 1024 * 1024

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
	process.Stdin = strings.NewReader(command.Stdin)
	// Feed stdout/stderr through bounded spools instead of StdoutPipe so that
	// os/exec owns the copying goroutines: Wait drains them before returning
	// and WaitDelay bounds the drain when a descendant inherits the child's
	// output handles and outlives it. StdoutPipe would instead be closed by
	// Wait, truncating a fast-exiting command's buffered output. The spool
	// also keeps the copying goroutines independent of onLine progress, so a
	// blocked handler cannot make WaitDelay discard output.
	stdoutSpool := newProcessSpool(processOutputSpoolLimit)
	stderrSpool := newProcessSpool(processOutputSpoolLimit)
	process.Stdout = stdoutSpool
	process.Stderr = stderrSpool
	process.WaitDelay = processPipeDrainTimeout
	if err := process.Start(); err != nil {
		stdoutSpool.CloseWrite()
		stderrSpool.CloseWrite()
		return Result{}, err
	}
	stdout, stderr := newOutputBuffer(command.OutputLimit), newOutputBuffer(command.OutputLimit)
	var wait sync.WaitGroup
	wait.Add(2)
	go scanOutput(stdoutSpool, stdout, onLine, &wait)
	go scanOutput(stderrSpool, stderr, nil, &wait)
	processErr := process.Wait()
	// The copy goroutines have finished, so closing the writers only delivers
	// EOF to the scanners; no pending output can be cut off here.
	stdoutSpool.CloseWrite()
	stderrSpool.CloseWrite()
	wait.Wait()
	// ErrWaitDelay means the child exited successfully but a descendant kept
	// its output handles open past the drain bound; that is not a command
	// failure.
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
	if processErr != nil {
		var exitErr *exec.ExitError
		if errors.As(processErr, &exitErr) {
			return result, nil
		}
		return result, processErr
	}
	return result, nil
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
	// producer may still be writing. Keep draining so a blocked writer (the
	// os/exec copy goroutine and, behind it, the command) can finish instead
	// of deadlocking Wait forever.
	_, _ = io.Copy(io.Discard, reader)
}

// processSpool is a bounded in-memory byte pipe. Writes block only when the
// backlog reaches limit, so the os/exec copy goroutines never wait for a slow
// line handler; reads block until data arrives or CloseWrite is called.
type processSpool struct {
	mu     sync.Mutex
	ready  *sync.Cond
	chunks [][]byte
	offset int
	queued int
	limit  int
	closed bool
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
			spool.ready.Wait()
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

// CloseWrite marks EOF for readers once the os/exec copy goroutines are done.
func (spool *processSpool) CloseWrite() {
	spool.mu.Lock()
	spool.closed = true
	spool.ready.Broadcast()
	spool.mu.Unlock()
}

type outputBuffer struct {
	bytes.Buffer
	limit int
}

func newOutputBuffer(limit int) *outputBuffer { return &outputBuffer{limit: limit} }

func (buffer *outputBuffer) Write(value []byte) (int, error) {
	written := len(value)
	if buffer.limit <= 0 {
		_, err := buffer.Buffer.Write(value)
		return written, err
	}
	if len(value) >= buffer.limit {
		buffer.Buffer.Reset()
		_, err := buffer.Buffer.Write(value[len(value)-buffer.limit:])
		return written, err
	}
	overflow := buffer.Len() + len(value) - buffer.limit
	if overflow > 0 {
		current := append([]byte(nil), buffer.Buffer.Bytes()[overflow:]...)
		buffer.Buffer.Reset()
		_, _ = buffer.Buffer.Write(current)
	}
	_, err := buffer.Buffer.Write(value)
	return written, err
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
