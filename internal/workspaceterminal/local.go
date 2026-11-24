package workspaceterminal

import (
	"os"
	"strings"
)

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
