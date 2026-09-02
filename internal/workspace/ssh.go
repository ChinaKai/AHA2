package workspace

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type SSHRunner struct {
	Host   string
	User   string
	Port   int
	SSHBin string
}

func (runner SSHRunner) Run(ctx context.Context, command Command, onLine LineHandler) (Result, error) {
	if runner.Host == "" {
		return Result{}, fmt.Errorf("ssh host is required")
	}
	executable := runner.SSHBin
	if executable == "" {
		executable = "ssh"
	}
	port := runner.Port
	if port == 0 {
		port = 22
	}
	target := runner.Host
	if runner.User != "" {
		target = runner.User + "@" + runner.Host
	}
	args := []string{
		"-T",
		"-p", strconv.Itoa(port),
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		target,
		"sh", "-s",
	}
	script, err := remoteScript(command)
	if err != nil {
		return Result{}, err
	}
	return (LocalRunner{}).Run(ctx, Command{
		Executable: executable,
		Args:       args,
		Stdin:      script,
		Timeout:    command.Timeout,
	}, onLine)
}

func remoteScript(command Command) (string, error) {
	if command.Executable == "" {
		return "", fmt.Errorf("remote executable is required")
	}
	var script strings.Builder
	script.WriteString("#!/bin/sh\nset -eu\n")
	if command.Dir != "" {
		script.WriteString("cd ")
		script.WriteString(shellQuote(command.Dir))
		script.WriteByte('\n')
	}
	for key, value := range command.Env {
		if !environmentName.MatchString(key) {
			return "", fmt.Errorf("invalid environment name %q", key)
		}
		script.WriteString("export ")
		script.WriteString(key)
		script.WriteByte('=')
		script.WriteString(shellQuote(value))
		script.WriteByte('\n')
	}
	if command.Stdin != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(command.Stdin))
		script.WriteString("printf %s ")
		script.WriteString(shellQuote(encoded))
		script.WriteString(" | base64 -d | ")
	}
	script.WriteString("exec ")
	script.WriteString(shellQuote(command.Executable))
	for _, argument := range command.Args {
		script.WriteByte(' ')
		script.WriteString(shellQuote(argument))
	}
	script.WriteByte('\n')
	return script.String(), nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
