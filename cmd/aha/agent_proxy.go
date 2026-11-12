package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/ChinaKai/AHA2/internal/agentproxy"
)

// runAgentProxy is the workspace side of the Agent API tunnel. It is a hidden
// subcommand: operators never type it, and it only makes sense with an SSH
// session's stdin and stdout attached.
func runAgentProxy(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("agent-proxy", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	stdio := flags.Bool("stdio", false, "serve the tunnel over stdin/stdout")
	protocol := flags.Int("protocol", agentproxy.Protocol, "wire protocol version")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("agent-proxy: %w", err)
	}
	if !*stdio {
		return fmt.Errorf("agent-proxy 只支持 --stdio")
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("agent-proxy 不接受额外参数")
	}
	return agentproxy.RunHelper(stdin, stdout, *protocol)
}
