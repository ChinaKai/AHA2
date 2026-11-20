package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// runMarkEnv names the environment variable that tags every process of one
// remote run. A remote command cannot be stopped the way a local one is: the
// local runner kills the process tree it started, but behind an SSH or WSL hop
// only the local client goes away. An SSH session without a terminal sends no
// signal when its connection closes, so an interrupted Agent, and whatever it
// had started, kept working on the remote host after AHA showed the Turn as
// interrupted.
//
// The mark is inherited by the whole tree, including children that detached
// themselves into another session or process group, so it finds processes a
// process-group kill would miss.
const runMarkEnv = "AHA2_RUN_MARK"

// remoteKillTimeout bounds the cleanup, so a slow or dead remote host cannot
// keep an interrupt from returning.
const remoteKillTimeout = 8 * time.Second

// newRunMark returns a fresh mark, or "" when the system has no randomness to
// give; an unmarked run simply behaves as it did before.
func newRunMark() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	return hex.EncodeToString(buffer)
}

// markCommand returns a copy of command whose environment carries a new run
// mark, and the mark. Only commands that asked for their tree to be killed are
// marked. The caller's Env map is never modified.
func markCommand(command Command) (Command, string) {
	if !command.KillTree {
		return command, ""
	}
	mark := newRunMark()
	if mark == "" {
		return command, ""
	}
	env := make(map[string]string, len(command.Env)+1)
	for key, value := range command.Env {
		env[key] = value
	}
	env[runMarkEnv] = mark
	command.Env = env
	return command, mark
}

// killMarkedScript ends every process whose environment carries the mark given
// as $1: TERM first so an Agent can flush its session file, KILL for whatever
// is still there three seconds later. It is POSIX sh because the remote "sh"
// is dash on Debian and Ubuntu, and it reads /proc, so a host without /proc is
// left as it was. A process this user may not inspect cannot carry the mark.
const killMarkedScript = `mark=$1
[ -n "$mark" ] || exit 0
marked() {
  for pid_dir in /proc/[0-9]*; do
    pid=${pid_dir#/proc/}
    [ "$pid" = "$$" ] && continue
    if tr '\0' '\n' < "$pid_dir/environ" 2>/dev/null | grep -qxF "` + runMarkEnv + `=$mark"; then
      printf '%s ' "$pid"
    fi
  done
}
pids=$(marked)
[ -n "$pids" ] || exit 0
kill -TERM $pids 2>/dev/null
tries=0
while [ "$tries" -lt 10 ]; do
  sleep 0.3 2>/dev/null || sleep 1
  pids=$(marked)
  [ -n "$pids" ] || exit 0
  tries=$((tries + 1))
done
kill -KILL $pids 2>/dev/null
exit 0`

// KillMarkedProcesses ends the processes of one marked run through runner. It
// is best effort: the Turn is already being interrupted, and a failed cleanup
// must not turn that into an error.
func KillMarkedProcesses(runner Runner, mark string) {
	if mark == "" || runner == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteKillTimeout)
	defer cancel()
	_, _ = runner.Run(ctx, Command{
		Executable: "sh",
		Args:       []string{"-c", killMarkedScript, "aha-kill", mark},
		Timeout:    remoteKillTimeout,
	}, nil)
}
