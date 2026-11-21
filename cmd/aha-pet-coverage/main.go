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
	"os"
	"path/filepath"

	"github.com/ChinaKai/AHA2/internal/httpapi"
	"github.com/ChinaKai/AHA2/internal/petcoverage"
)

func main() {
	update := flag.Bool("update", false, "rewrite the inventories; reviewed columns are kept, changed rows become stale")
	complete := flag.Bool("complete", false, "fail unless every operation and tool row has a closing status with valid evidence")
	flag.Parse()
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	report, err := petcoverage.Check(root, petcoverage.NewCatalog(httpapi.MCPToolCatalog()), *update, *complete)
	for _, key := range report.Stale {
		fmt.Println("stale (re-verify):", key)
	}
	for _, key := range report.Removed {
		fmt.Println("removed:", key)
	}
	fmt.Printf("HTTP operations: %d (closed %d, open %d); Web literal call sites: %d; ordinary-Task MCP tools: %d (closed %d)\n",
		report.Operations, report.Final, report.Open, report.WebSites, report.Tools, report.ToolsFinal)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
