package tray

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrUnsupported = errors.New("AHA2 tray is only supported on Windows")
var ErrAlreadyRunning = errors.New("AHA2 tray is already running")

const SingleInstanceName = `Local\AHA2Tray`

type Config struct {
	Server                string
	Listen                string
	DataDir               string
	AgentAPIURL           string
	AllowInsecureAgentAPI bool
	Version               string
}

type Command struct {
	Path string
	Args []string
	Dir  string
}

func ParseArgs(args []string, version string) (Config, error) {
	flags := flag.NewFlagSet("aha-tray", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", "", "path to aha2.exe")
	listen := flags.String("listen", "", "HTTP listen IP and port")
	dataDir := flags.String("data-dir", "", "AHA2 data directory")
	agentAPIURL := flags.String("agent-api-url", "", "Agent-reachable AHA2 base URL")
	allowInsecure := flags.Bool("allow-insecure-agent-api", false, "allow a non-loopback http Agent API URL")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	config := Config{
		Server: strings.TrimSpace(*server), Listen: strings.TrimSpace(*listen), DataDir: strings.TrimSpace(*dataDir),
		AgentAPIURL: strings.TrimSpace(*agentAPIURL), AllowInsecureAgentAPI: *allowInsecure, Version: version,
	}
	return normalizeConfig(config)
}

func normalizeConfig(config Config) (Config, error) {
	if config.Server == "" || config.Listen == "" || config.DataDir == "" {
		return Config{}, errors.New("usage: aha-tray --server <aha2.exe> --listen <ip:port> --data-dir <path> [--agent-api-url <url>] [--allow-insecure-agent-api]")
	}
	if _, err := normalizeListen(config.Listen); err != nil {
		return Config{}, err
	}
	var err error
	if config.AgentAPIURL != "" {
		parsed, err := url.Parse(config.AgentAPIURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return Config{}, errors.New("agent-api-url must be an http or https base URL without credentials, path, query, or fragment")
		}
		if parsed.Scheme == "http" && !config.AllowInsecureAgentAPI && !isLoopbackHost(parsed.Hostname()) {
			return Config{}, errors.New("non-loopback http agent-api-url requires --allow-insecure-agent-api")
		}
	}
	config.Server, err = filepath.Abs(config.Server)
	if err != nil {
		return Config{}, fmt.Errorf("resolve server path: %w", err)
	}
	config.DataDir, err = filepath.Abs(config.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data directory: %w", err)
	}
	return config, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	address := net.ParseIP(strings.TrimSpace(host))
	return address != nil && address.IsLoopback()
}

// EffectiveListen resolves the address the server should bind.
//
// The scheduled task carries the address chosen at install time, but the operator
// can change it from the UI, which stores it in the data directory. The stored
// value therefore wins, and the task argument is only the fallback. Any failure
// here returns the configured address: a resolver problem must never keep the
// server from starting.
func EffectiveListen(runner CommandRunner, config Config) string {
	resolved, err := runner.Output(Command{
		Path: config.Server,
		Args: []string{"listen", "--data-dir", config.DataDir, "--default", config.Listen},
		Dir:  filepath.Dir(config.Server),
	})
	if err != nil {
		return config.Listen
	}
	candidate := strings.TrimSpace(resolved)
	if candidate == "" {
		return config.Listen
	}
	if _, err := normalizeListen(candidate); err != nil {
		// A stored address that cannot be bound must not strand the tray on a
		// dead listener; fall back to the address the task was installed with.
		return config.Listen
	}
	return candidate
}

// CommandRunner runs the server binary for a one-shot query.
type CommandRunner interface {
	Output(command Command) (string, error)
}

func BuildServerCommand(config Config) (Command, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return Command{}, err
	}
	args := []string{"serve", "--listen", config.Listen, "--data-dir", config.DataDir}
	if config.AgentAPIURL != "" {
		args = append(args, "--agent-api-url", config.AgentAPIURL)
	}
	if config.AllowInsecureAgentAPI {
		args = append(args, "--allow-insecure-agent-api")
	}
	return Command{Path: config.Server, Args: args, Dir: filepath.Dir(config.Server)}, nil
}

func ManagementURL(listen string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return ""
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// normalizeListen validates an "ip:port" listen address the same way
// normalizeConfig does, so a resolved address is held to the same standard as a
// configured one.
func normalizeListen(value string) (string, error) {
	value = strings.TrimSpace(value)
	host, rawPort, err := net.SplitHostPort(value)
	if err != nil || net.ParseIP(strings.TrimSpace(host)) == nil {
		return "", errors.New("listen must be an IP address and port")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("listen port must be between 1 and 65535")
	}
	return value, nil
}
