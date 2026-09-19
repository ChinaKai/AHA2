package workspace

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CodexWriterStopTimeout bounds how long we wait for a leftover backend writer
// to exit before deciding that the session cannot be resumed safely.
const CodexWriterStopTimeout = 20 * time.Second

// WriterQuery identifies the process that may still be writing one provider
// session.
//
// A service restart cannot kill a backend running behind a WSL or SSH hop: only
// the local client goes away, and the remote CLI keeps appending to its rollout
// file. Reusing that provider session would then put two writers on one thread,
// which is what interleaves a new user message into an unfinished tool call.
type WriterQuery struct {
	// EnvName/EnvValue match a backend started with that variable set. AHA gives
	// Codex and isolated Claude runs a per-session home, which identifies their
	// writer exactly and never matches the operator's own sessions.
	EnvName  string
	EnvValue string
	// ResumeSessionID matches a backend started with "resume <id>". It covers
	// Claude's native login, which must keep the default config directory and so
	// can only be recognised from its command line. The two identifiers are
	// alternatives: a process matching either one is a writer for this session.
	ResumeSessionID string
}

// safeSessionID is the shape a provider session id must have for the Windows
// lookup, which embeds it in a PowerShell script. Provider ids are UUIDs and
// rollout names; anything else is refused rather than escaped, because this
// string reaches a command interpreter.
var safeSessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// FindWriterPIDs returns the PIDs of backend processes that match the query.
func FindWriterPIDs(ctx context.Context, runner Runner, query WriterQuery) ([]int, error) {
	envName := strings.TrimSpace(query.EnvName)
	envValue := strings.TrimSpace(query.EnvValue)
	resumeID := strings.TrimSpace(query.ResumeSessionID)
	if envName == "" && resumeID == "" {
		return nil, fmt.Errorf("writer query needs an environment variable or a resume session id")
	}
	if envName != "" && envValue == "" {
		return nil, fmt.Errorf("writer query needs a value for %s", envName)
	}
	if IsWindowsRunner(runner) {
		// A Windows target exposes a process command line but not its environment,
		// so the session home cannot be matched there; the resume id identifies the
		// same session just as precisely.
		if resumeID == "" {
			return nil, fmt.Errorf("a Windows target can only match a writer by resume session id")
		}
		return findWindowsWriterPIDs(ctx, runner, resumeID)
	}
	script := `env_name=$1
env_value=$2
resume_id=$3
for pid_dir in /proc/[0-9]*; do
  pid=${pid_dir#/proc/}
  # A plain -r test uses access(2), which does not apply the ptrace check that
  # open(2) applies to /proc/<pid>/environ. A process this user may not inspect —
  # on a normal host, every root-owned daemon — passes the test and then fails the
  # read, and that redirection error reaches stderr. The caller reads a non-zero
  # exit as a failure only when stderr says something, so this turned an ordinary
  # empty result into a reported error. Open the file for real instead: a writer
  # we cannot inspect is not evidence that no writer exists.
  cat "$pid_dir/environ" >/dev/null 2>&1 || continue
  hit=0
  if [ -n "$env_name" ]; then
    # /proc/<pid>/environ is NUL-separated and each value may contain spaces, so
    # split on NUL. This must stay POSIX: the runner executes the script with
    # "sh", which is dash on Debian/Ubuntu, and dash has no "read -d". Using it
    # there made the whole scan match nothing, silently reporting no writer.
    while IFS= read -r entry; do
      case "$entry" in
        "$env_name"=*) [ "${entry#*=}" = "$env_value" ] && hit=1 ;;
      esac
    done <<EOF
$(tr '\0' '\n' < "$pid_dir/environ" 2>/dev/null)
EOF
    # tr/build may have produced a trailing empty line; that cannot match the
    # NAME=value pattern above, so no extra filtering is needed.
  fi
  if [ "$hit" = 0 ] && [ -n "$resume_id" ]; then
    cmdline=$(tr '\0' ' ' < "$pid_dir/cmdline" 2>/dev/null) || cmdline=""
    # Match the flag as the backend actually passes it ("--resume <id>"), and
    # never anchor on a trailing space: the id may be the last argument, and a
    # command line built from NUL-separated argv can end with an extra space.
    case "$cmdline" in
      *"resume $resume_id"*) hit=1 ;;
    esac
  fi
  [ "$hit" = 1 ] && printf '%s\n' "$pid"
done
# End with success on purpose. Without this the shell's exit status is whatever
# the last loop iteration evaluated to, and that line is a "&&" test that is false
# whenever the final process examined is not a match — so an ordinary empty result
# exits 1. The caller reads any non-zero exit as a failure, making the outcome
# depend on which PID happens to sort last in /proc and therefore fail at random.
exit 0`
	command := Command{
		Executable: "sh",
		Args:       []string{"-c", script, "aha-writer", envName, envValue, resumeID},
		Timeout:    20 * time.Second,
	}
	result, err := runner.Run(ctx, command, nil)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", result.ExitCode)
		}
		return nil, fmt.Errorf("find backend writer: %s", detail)
	}
	return parseWriterPIDs(result.Stdout), nil
}

// parseWriterPIDs reads one PID per line, ignoring anything else a shell may
// have written to stdout.
func parseWriterPIDs(output string) []int {
	var pids []int
	for _, field := range strings.Fields(output) {
		pid, convErr := strconv.Atoi(field)
		if convErr != nil || pid <= 0 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// findWindowsWriterPIDs returns the PIDs of processes whose command line resumes
// the given provider session.
//
// Unlike a POSIX target, a Windows target does not expose another process's
// environment, so the per-session home cannot identify the writer there. The
// command line is readable through CIM, and matching the resume id restricts the
// result to this specific session: AHA spawns the backend directly, so only a
// process AHA started for this session carries it.
func findWindowsWriterPIDs(ctx context.Context, runner Runner, resumeID string) ([]int, error) {
	if !safeSessionID.MatchString(resumeID) {
		return nil, fmt.Errorf("unsafe session id for a Windows writer lookup")
	}
	// Contains() is a literal test, so the session id needs no escaping here.
	script := `$id = $args[0]
Get-CimInstance Win32_Process -Filter "Name='claude.exe' or Name='codex.exe'" |
  Where-Object { $_.CommandLine -and $_.CommandLine.Contains('resume ' + $id) } |
  ForEach-Object { [Console]::Out.WriteLine($_.ProcessId) }`
	result, err := runner.Run(ctx, Command{
		Executable: "powershell.exe",
		Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
			script, resumeID},
		Timeout: 30 * time.Second,
	}, nil)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = fmt.Sprintf("exit code %d", result.ExitCode)
		}
		return nil, fmt.Errorf("find backend writer: %s", detail)
	}
	return parseWriterPIDs(result.Stdout), nil
}

// TerminateProcesses signals the given PIDs and waits until they are gone.
// It returns an error when a process is still alive after the timeout, so the
// caller can refuse to reuse a session whose previous writer may still run.
func TerminateProcesses(ctx context.Context, runner Runner, pids []int) error {
	if len(pids) == 0 {
		return nil
	}
	if IsWindowsRunner(runner) {
		if _, err := runner.Run(ctx, Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`foreach ($id in [int[]]$args) { Stop-Process -Id $id -Force -ErrorAction SilentlyContinue }`,
			}, Timeout: 15 * time.Second,
		}, nil); err != nil {
			return err
		}
	} else {
		arguments := make([]string, 0, len(pids)*3+8)
		arguments = append(arguments, "-c", `for pid in "$@"; do kill -TERM "$pid" 2>/dev/null; done`)
		arguments = append(arguments, "aha-stop-writer")
		for _, pid := range pids {
			arguments = append(arguments, strconv.Itoa(pid))
		}
		if _, err := runner.Run(ctx, Command{Executable: "sh", Args: arguments, Timeout: 15 * time.Second}, nil); err != nil {
			return err
		}
	}

	deadline := time.Now().Add(CodexWriterStopTimeout)
	for {
		alive, err := runningPIDs(ctx, runner, pids)
		if err != nil {
			return err
		}
		if len(alive) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d backend writer(s) still running after %s", len(alive), CodexWriterStopTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// runningPIDs returns the subset of pids that are still executing.
//
// A process that has exited but not yet been reaped is a zombie: it holds no
// file handle and cannot append to a rollout file, so it must not count as a
// live writer. Otherwise a terminated backend would look alive until its parent
// happens to reap it, and the caller would refuse a session that is safe to use.
func runningPIDs(ctx context.Context, runner Runner, pids []int) ([]int, error) {
	if len(pids) == 0 {
		return nil, nil
	}
	if IsWindowsRunner(runner) {
		// Get-Process omits a PID that no longer exists, and a terminated Windows
		// process is reaped by the OS rather than left as a zombie, so no state
		// check is needed here.
		result, err := runner.Run(ctx, Command{
			Executable: "powershell.exe",
			Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
				`foreach ($id in [int[]]$args) { if (Get-Process -Id $id -ErrorAction SilentlyContinue) { [Console]::Out.WriteLine($id) } }`,
			}, Timeout: 15 * time.Second,
		}, nil)
		if err != nil {
			return nil, err
		}
		if result.ExitCode != 0 {
			return nil, fmt.Errorf("inspect backend writer: %s", remoteCommandDetail(result))
		}
		return parseWriterPIDs(result.Stdout), nil
	}
	// `ps -o state=` yields nothing for a PID that no longer exists, and a state
	// beginning with Z for a zombie. Fall back to kill -0 when ps is absent.
	script := `for pid in "$@"; do
  state=$(ps -o state= -p "$pid" 2>/dev/null | tr -d ' ')
  if [ -z "$state" ]; then
    kill -0 "$pid" 2>/dev/null && printf '%s\n' "$pid"
  else
    case "$state" in
      Z*) ;;
      *) printf '%s\n' "$pid" ;;
    esac
  fi
done`
	arguments := []string{"-c", script, "aha-running-writer"}
	for _, pid := range pids {
		arguments = append(arguments, strconv.Itoa(pid))
	}
	result, err := runner.Run(ctx, Command{Executable: "sh", Args: arguments, Timeout: 15 * time.Second}, nil)
	if err != nil {
		return nil, err
	}
	// The loop's last test decides the shell's exit status, so a non-zero code
	// here means "the last pid was not running", not that the check failed.
	// Only genuine shell failures, which write to stderr, are errors.
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return nil, fmt.Errorf("inspect backend writer: %s", detail)
	}
	var alive []int
	for _, field := range strings.Fields(result.Stdout) {
		pid, convErr := strconv.Atoi(field)
		if convErr == nil && pid > 0 {
			alive = append(alive, pid)
		}
	}
	return alive, nil
}
