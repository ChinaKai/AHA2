package workspaceterminal

import (
	"os"
	"strings"
	"time"
)

// ObserveEarlyExit reports the output drained by the terminal reader. Waiting
// for the exit status happens independently of session startup and cleanup.
func ObserveEarlyExit(process Process, report func(exitCode int64, outputBytes uint64, err error)) Process {
	status, ok := process.(interface{ ExitStatus() (int64, error) })
	if !ok {
		return process
	}
	return &observedProcess{Process: process, status: status.ExitStatus, report: report, started: time.Now()}
}

type observedProcess struct {
	Process
	status  func() (int64, error)
	report  func(int64, uint64, error)
	started time.Time
	bytes   uint64 // Only the session's output reader updates this.
}

func (p *observedProcess) Read(data []byte) (int, error) {
	n, err := p.Process.Read(data)
	p.bytes += uint64(n)
	if err != nil && time.Since(p.started) <= 5*time.Second {
		go func(outputBytes uint64) {
			code, exitErr := p.status()
			p.report(code, outputBytes, exitErr)
		}(p.bytes)
	}
	return n, err
}

// The server's process environment can contain credentials for its own use.
func shellEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LC_CTYPE", "TZ",
			"SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "USERPROFILE",
			"USERNAME", "USERDOMAIN", "APPDATA", "LOCALAPPDATA", "HOMEDRIVE", "HOMEPATH",
			"PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "TEMP", "TMP", "TMPDIR",
			"PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS", "LANGUAGE":
			env = append(env, entry)
		}
	}
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
}
