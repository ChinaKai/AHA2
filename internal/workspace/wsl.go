package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// WSLRunner executes commands inside a WSL distro. It is the "execution bridge"
// for a workspace that lives on this Windows machine but inside the Linux
// subsystem. The workspace root_path is expected to be a WSL-native path
// (e.g. /home/user/project) and the distro is stored on the workspace.
type WSLRunner struct {
	Distro string
}

func (runner WSLRunner) Run(parent context.Context, command Command, onLine LineHandler) (Result, error) {
	distro := strings.TrimSpace(runner.Distro)
	if distro == "" {
		return Result{}, fmt.Errorf("wsl distro is required")
	}
	script, err := remoteScript(command)
	if err != nil {
		return Result{}, err
	}
	return runProcess(parent, Command{
		Executable: findWSLExecutable(),
		Args:       []string{"-d", distro, "--", "bash", "-s"},
		Stdin:      script,
		Timeout:    command.Timeout,
		KillTree:   command.KillTree,
	}, wslHostEnvironment(), onLine)
}

// wslHostEnvironment returns a minimal Windows environment for the wsl.exe hop.
// wsl.exe flows the caller's PATH into the distro ahead of the Linux dirs, so a
// full environment would let Windows shims hijack codex/claude/python lookups
// inside the distro. Only SystemRoot is required for wsl.exe itself; all real
// backend env vars are exported inside the bash script by remoteScript.
func wslHostEnvironment() []string {
	env := []string{}
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		env = append(env, "SystemRoot="+systemRoot)
	}
	if windir := os.Getenv("WINDIR"); windir != "" {
		env = append(env, "WINDIR="+windir)
	}
	return env
}

func findWSLExecutable() string {
	if path, err := exec.LookPath("wsl.exe"); err == nil {
		return path
	}
	if runtime.GOOS == "windows" {
		if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
			candidate := filepath.Join(systemRoot, "System32", "wsl.exe")
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return "wsl.exe"
}

// WSLExecutableFound reports whether wsl.exe is resolvable on the host. It is
// the gate for offering the WSL execution bridge in the workspace form.
func WSLExecutableFound() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if path, err := exec.LookPath("wsl.exe"); err == nil {
		_, statErr := os.Stat(path)
		return statErr == nil
	}
	if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
		_, err := os.Stat(filepath.Join(systemRoot, "System32", "wsl.exe"))
		return err == nil
	}
	return false
}

// WSLDistros lists the installed WSL distros via `wsl.exe -l -q`. The output is
// UTF-16LE, so it is decoded here. Returns nil when wsl is unavailable.
func WSLDistros() []string {
	if !WSLExecutableFound() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, findWSLExecutable(), "-l", "-q").Output()
	if err != nil {
		return nil
	}
	var result []string
	for _, line := range strings.Split(decodeUTF16LE(output), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.EqualFold(name, "wsl") {
			continue
		}
		result = append(result, name)
	}
	return result
}

func decodeUTF16LE(data []byte) string {
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		data = data[2:] // strip BOM
	}
	var builder strings.Builder
	for index := 0; index+1 < len(data); index += 2 {
		code := uint16(data[index]) | uint16(data[index+1])<<8
		if code == 0 {
			break
		}
		// Handle the ASCII range directly; surrogate pairs fall back to a rune.
		if code < 0xD800 || code > 0xDFFF {
			builder.WriteRune(rune(code))
		}
	}
	return builder.String()
}
