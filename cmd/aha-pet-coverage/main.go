// Command aha-pet-coverage checks the pet MCP coverage ledgers against the
// registered HTTP operations, Web call sites and built-in MCP tool catalog.
//
//	go run ./cmd/aha-pet-coverage            # drift and evidence check
//	go run ./cmd/aha-pet-coverage -update    # rewrite inventories, then review
//	go run ./cmd/aha-pet-coverage -complete  # release gate: every row closed
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ChinaKai/AHA2/internal/httpapi"
	"github.com/ChinaKai/AHA2/internal/petcoverage"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("aha-pet-coverage", flag.ContinueOnError)
	flags.SetOutput(stderr)
	update := flags.Bool("update", false, "rewrite the inventories; reviewed columns are kept, changed rows become stale")
	complete := flags.Bool("complete", false, "fail unless every operation and tool row has a closing status with valid evidence")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintln(stderr, "unexpected arguments:", flags.Args())
		return 2
	}
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	report, err := petcoverage.Check(root, petcoverage.NewCatalog(httpapi.MCPToolCatalog()), *update, *complete)
	for _, key := range report.Stale {
		fmt.Fprintln(stdout, "stale (re-verify):", key)
	}
	for _, key := range report.Removed {
		fmt.Fprintln(stdout, "removed:", key)
	}
	for _, call := range report.NonRouteCalls {
		fmt.Fprintln(stdout, "non-route Handle call:", call)
	}
	fmt.Fprintf(stdout, "Handle/HandleFunc calls: %d (route registrations %d, non-route %d)\n", report.HandleCalls, report.RegisteringCalls, len(report.NonRouteCalls))
	fmt.Fprintf(stdout, "HTTP operations: %d (closed %d, open %d); Web API call sites: %d; ordinary-Task MCP tools: %d (closed %d)\n",
		report.Operations, report.Final, report.Open, report.WebSites, report.Tools, report.ToolsFinal)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the working directory")
		}
		dir = parent
	}
}
