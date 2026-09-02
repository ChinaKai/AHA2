package workspace

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type LocalRunner struct{}

func (runner LocalRunner) Run(parent context.Context, command Command, onLine LineHandler) (Result, error) {
	return runProcess(parent, command, mergeEnvironment(os.Environ(), command.Env), onLine)
}

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
	process.Dir = command.Dir
	process.Env = env
	process.Stdin = strings.NewReader(command.Stdin)
	stdoutPipe, err := process.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	stderrPipe, err := process.StderrPipe()
	if err != nil {
		return Result{}, err
	}
	if err := process.Start(); err != nil {
		return Result{}, err
	}
	var stdout, stderr bytes.Buffer
	var wait sync.WaitGroup
	wait.Add(2)
	go scanOutput(stdoutPipe, &stdout, onLine, &wait)
	go scanOutput(stderrPipe, &stderr, nil, &wait)
	processErr := process.Wait()
	wait.Wait()
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

func scanOutput(reader io.Reader, destination *bytes.Buffer, handler LineHandler, wait *sync.WaitGroup) {
	defer wait.Done()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		destination.WriteString(line)
		destination.WriteByte('\n')
		if handler != nil {
			handler(line)
		}
	}
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
