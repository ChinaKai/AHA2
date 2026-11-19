// Command aha-prompt-lint is a read-only structural diagnostic for the AHA
// prompt. It renders the ten stable preview scenarios, checks the four-block
// contract (identity -> principles -> context -> control), and prints a
// before/after diff produced by reordering the existing fragments only.
//
// It never starts the AHA service, never reads Task state, never writes prompt
// overrides, and never changes the production Build output.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/prompt"
)

// emptyRepository keeps the Engine read-only. Any write attempt is a bug in a
// diagnostic tool, so it returns an error instead of silently doing nothing.
type emptyRepository struct{}

func (emptyRepository) PromptTemplateOverrides(context.Context) (map[string]domain.PromptTemplate, error) {
	return map[string]domain.PromptTemplate{}, nil
}

func (emptyRepository) UpsertPromptTemplateOverride(context.Context, string, string, time.Time) error {
	return errors.New("aha-prompt-lint is read-only")
}

func (emptyRepository) DeletePromptTemplateOverride(context.Context, string) error {
	return errors.New("aha-prompt-lint is read-only")
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		lang       = flag.String("lang", "zh-CN", "prompt language: zh-CN or en")
		scenario   = flag.String("scenario", "", "limit to one stable scenario ID")
		source     = flag.String("source", "", "message source for -scenario; defaults to the scenario default")
		message    = flag.String("message", prompt.DefaultScenarioTestMessage, "test message used by the demo Turn")
		check      = flag.String("check", "after", "fail on: after (proposed layout), before (current layout), or none")
		fullDiff   = flag.Bool("diff", true, "print the before/after unified diff")
		jsonOutput = flag.Bool("json", false, "emit the full report as JSON")
	)
	flag.Parse()

	engine := prompt.NewEngine(emptyRepository{})
	ctx := context.Background()
	previews, err := collectPreviews(ctx, engine, *lang, *scenario, *source, *message)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(previews)
	}

	fmt.Printf("prompt-structure-lint lang=%s scenarios=%d\n", *lang, len(previews))
	failures := 0
	for _, preview := range previews {
		printPreview(preview, *fullDiff)
		failed, err := checkPreview(preview, *check)
		if err != nil {
			return err
		}
		if failed {
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("prompt structure lint failed for %d scenario(s) under -check=%s", failures, *check)
	}
	fmt.Printf("prompt structure lint passed: %d scenario(s), check=%s\n", len(previews), *check)
	return nil
}

func collectPreviews(
	ctx context.Context, engine *prompt.Engine, lang, scenario, source, message string,
) ([]prompt.ScenarioStructurePreview, error) {
	scenario = strings.TrimSpace(scenario)
	if scenario == "" {
		if strings.TrimSpace(source) != "" {
			return nil, errors.New("-source requires -scenario")
		}
		return engine.ScenarioStructurePreviews(ctx, message, lang)
	}
	preview, err := engine.PreviewScenarioStructure(ctx, scenario, message, source, lang)
	if err != nil {
		return nil, err
	}
	return []prompt.ScenarioStructurePreview{preview}, nil
}

func checkPreview(preview prompt.ScenarioStructurePreview, check string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(check)) {
	case "none", "off":
		return false, nil
	case "before":
		if err := (prompt.LintReport{Label: preview.Scenario + " before", Issues: preview.Before.Issues}).Err(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return true, nil
		}
	case "after":
		if err := (prompt.LintReport{Label: preview.Scenario + " after", Issues: preview.After.Issues}).Err(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return true, nil
		}
	case "both":
		failed := false
		if err := (prompt.LintReport{Label: preview.Scenario + " before", Issues: preview.Before.Issues}).Err(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = true
		}
		if err := (prompt.LintReport{Label: preview.Scenario + " after", Issues: preview.After.Issues}).Err(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = true
		}
		return failed, nil
	default:
		return false, fmt.Errorf("unknown -check %q; want after, before, both, or none", check)
	}
	return false, nil
}

func printPreview(preview prompt.ScenarioStructurePreview, fullDiff bool) {
	fmt.Printf("\n== %s | %s | source=%s ==\n", preview.Scenario, preview.ScenarioName, preview.MessageSource)
	fmt.Printf("  before blocks: %s\n", blockSummary(preview.Before))
	fmt.Printf("  after  blocks: %s\n", blockSummary(preview.After))
	fmt.Printf("  diff: +%d -%d lines (text preserved, order only)\n", preview.AddedLines, preview.RemovedLines)
	printIssues("before", preview.Before.Issues)
	printIssues("after", preview.After.Issues)
	if fullDiff && preview.Diff != "" {
		for _, line := range strings.Split(strings.TrimRight(preview.Diff, "\n"), "\n") {
			fmt.Printf("  %s\n", line)
		}
	}
}

func blockSummary(view prompt.PromptStructureView) string {
	parts := make([]string, 0, len(view.Blocks))
	for _, block := range view.Blocks {
		parts = append(parts, fmt.Sprintf("%s(%d/%d)", block.ID, block.Chars, block.MaxRunes))
	}
	return strings.Join(parts, " ")
}

func printIssues(label string, issues []prompt.LintIssue) {
	if len(issues) == 0 {
		fmt.Printf("  %s issues: none\n", label)
		return
	}
	errors := 0
	for _, issue := range issues {
		if issue.Severity == prompt.SeverityError {
			errors++
		}
	}
	fmt.Printf("  %s issues: %d (%d error)\n", label, len(issues), errors)
	for _, issue := range issues {
		fmt.Printf("    [%s] %s\n", issue.Severity, issue.String())
	}
}
