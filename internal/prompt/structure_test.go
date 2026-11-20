package prompt

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// compliantStructureFragments is the minimal layout that satisfies every
// required block and required fragment, in the recommended order. Tests mutate
// one property at a time so a failure names the rule under test.
func compliantStructureFragments() []StructureFragment {
	return []StructureFragment{
		{ID: "identity.task-agent", Name: "Task Agent Identity", Block: BlockIdentity, Order: 0, Stable: true, Resident: true,
			Content: "## Task Agent Identity\nIdentity: AHA Task Agent."},
		{ID: "role.main", Name: "Main Agent", Block: BlockIdentity, Order: 1, Stable: true, Mixed: true, Resident: true,
			Content: "## Main Agent\nIntegrate, verify, and deliver the result."},
		{ID: "channel.web", Name: "AHA Web Channel", Block: BlockIdentity, Order: 2, Stable: true, Resident: true,
			Content: "## AHA Web Channel\nChannel: AHA Web."},
		{ID: "core.default", Name: "AHA Core", Block: BlockPrinciples, Order: 0, Stable: true, Resident: true,
			Content: "## AHA Core\nUse the selected Task workspace as the default workspace."},
		{ID: "policy.single", Name: "Single Agent", Block: BlockPrinciples, Order: 1, Stable: true, Resident: true,
			Content: "## Single Agent\nThis Task uses single-agent mode."},
		{ID: "protocol.knowledge", Name: "Knowledge Protocol", Block: BlockPrinciples, Order: 2, Stable: true, Mixed: true, Resident: true,
			Content: "## Knowledge Protocol\nRead knowledge progressively by index."},
		{ID: "protocol.agent-api", Name: "Agent Control API Protocol", Block: BlockControl, Order: 0, Stable: true, Mixed: true, Resident: true,
			Content: "## Agent Control API Protocol\nUse the Task-scoped Agent Control API."},
		{ID: "protocol.attachment-delivery", Name: "Attachment Delivery Protocol", Block: BlockControl, Order: 1, Stable: true, Resident: true,
			Content: "## Attachment Delivery Protocol\nOnly a confirmed upload and binding is a delivery."},
		{ID: "language.directive", Name: "Response language", Block: BlockControl, Order: 2, Stable: true, Resident: true,
			Content: "## Response language\nUse Simplified Chinese."},
		{ID: "section.task-workspace", Name: "Task And Workspace Section", Block: BlockContext, Order: 1, Stable: false, Mixed: true, Resident: true,
			Content: "## Task and workspace\n- Project: demo"},
		{ID: "section.available-context", Name: "Available Context Section", Block: BlockContext, Order: 2, Stable: false, Mixed: true, Resident: true,
			Content: "## Available context\n- `task.md` (content template)"},
		{ID: "section.current-inbox", Name: "Current Inbox Section", Block: BlockContext, Order: 3, Stable: false, Mixed: true, Resident: true,
			Content: "## Current Inbox Batch\nProcess the routed message."},
	}
}

func issueCodes(report LintReport) map[string]bool {
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
	}
	return codes
}

func TestPromptStructureSpecsAreExecutable(t *testing.T) {
	t.Parallel()
	blocks := PromptBlocks()
	if len(blocks) != 4 {
		t.Fatalf("expected four blocks, got %d", len(blocks))
	}
	for index, block := range blocks {
		if block.Order != index {
			t.Fatalf("block %s order=%d, want %d", block.ID, block.Order, index)
		}
		if block.MaxRunes <= 0 {
			t.Fatalf("block %s has no rune cap", block.ID)
		}
		if !block.Required {
			t.Fatalf("block %s is not required; the four-block contract needs every block present", block.ID)
		}
		for _, fragmentID := range block.RequiredFragments {
			if _, ok := promptFragmentPlanByID(fragmentID); !ok {
				t.Fatalf("block %s requires unplanned fragment %s", block.ID, fragmentID)
			}
		}
	}
	priorities := PromptConflictPriorities()
	if len(priorities) != 5 {
		t.Fatalf("expected five conflict priority tiers, got %d", len(priorities))
	}
	for index, priority := range priorities {
		if priority.Rank != index+1 {
			t.Fatalf("priority %q rank=%d, want %d", priority.NameZH, priority.Rank, index+1)
		}
		if len(priority.Sources) == 0 {
			t.Fatalf("priority %q names no source", priority.NameZH)
		}
	}
	// The owner's required precedence must stay visible and ordered.
	for _, name := range []string{"平台与核心边界", "Owner 明确指令", "Task 目标", "技能/知识", "页面内容"} {
		found := false
		for _, priority := range priorities {
			if priority.NameZH == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("conflict priority %q is missing", name)
		}
	}
}

func TestPromptFragmentPlanCoversEveryBuiltinTemplateAndResidentSelection(t *testing.T) {
	t.Parallel()
	for _, template := range builtinTemplates {
		if _, ok := promptFragmentPlanByID(template.ID); !ok {
			t.Errorf("builtin template %s has no four-block assignment", template.ID)
		}
	}
	ids := map[string]bool{"language.directive": true}
	for _, role := range []string{"main", "sub"} {
		for _, mode := range []string{"auto", "single"} {
			for _, knowledge := range []bool{true, false} {
				for _, identity := range []string{identityTaskAgent, identityChannelAssistant, identityChannelDigitalHuman, "pet-task"} {
					for _, channel := range []string{channelWeb, channelExternal} {
						for _, id := range residentTemplateIDs(role, mode, knowledge, "identity."+identity, "channel."+channel) {
							ids[id] = true
						}
					}
				}
			}
		}
	}
	for id := range ids {
		plan, ok := promptFragmentPlanByID(id)
		if !ok {
			t.Errorf("resident fragment %s has no four-block assignment", id)
			continue
		}
		if plan.Block == "" {
			t.Errorf("resident fragment %s has an empty block", id)
		}
	}
}

func TestLintStructureAcceptsCompliantLayout(t *testing.T) {
	t.Parallel()
	report := LintStructure("compliant", compliantStructureFragments())
	if !report.OK() {
		t.Fatalf("compliant layout failed: %v\nissues: %#v", report.Err(), report.Issues)
	}
	if err := report.Err(); err != nil {
		t.Fatalf("Err() returned %v for a passing report", err)
	}
	// Mixed fragments are reported as info, never as a failure.
	codes := issueCodes(report)
	if !codes[IssueStableVolatileMixed] {
		t.Fatal("expected the stable/volatile mixing advisories to be reported")
	}
	if !codes[IssueMultiBlockFragment] {
		t.Fatal("expected the multi-block fragment advisories to be reported")
	}
}

func TestLintStructureRejectsEachInvariantWithAClearError(t *testing.T) {
	t.Parallel()
	clone := func() []StructureFragment {
		return append([]StructureFragment(nil), compliantStructureFragments()...)
	}
	withContent := func(fragment StructureFragment, content string) StructureFragment {
		fragment.Content = content
		fragment.Chars = len([]rune(content))
		return fragment
	}
	cases := []struct {
		name    string
		build   func() []StructureFragment
		code    string
		want    string
		warning bool
	}{
		{
			name: "missing block",
			build: func() []StructureFragment {
				result := clone()
				filtered := result[:0]
				for _, fragment := range result {
					if fragment.Block != BlockIdentity {
						filtered = append(filtered, fragment)
					}
				}
				return filtered
			},
			code: IssueMissingBlock,
			want: "required block \"identity\"",
		},
		{
			name: "missing required fragment",
			build: func() []StructureFragment {
				result := clone()
				filtered := result[:0]
				for _, fragment := range result {
					if fragment.ID != "protocol.attachment-delivery" {
						filtered = append(filtered, fragment)
					}
				}
				return filtered
			},
			code: IssueMissingRequiredFragment,
			want: "missing required fragment \"protocol.attachment-delivery\"",
		},
		{
			name: "unknown fragment",
			build: func() []StructureFragment {
				result := clone()
				return append(result, StructureFragment{ID: "mystery.template", Content: "unknown"})
			},
			code: IssueUnknownFragment,
			want: "no block assignment",
		},
		{
			name: "duplicate fragment",
			build: func() []StructureFragment {
				result := clone()
				core := result[3]
				return append(result, core)
			},
			code: IssueDuplicateFragment,
			want: "appears more than once",
		},
		{
			name: "duplicate heading",
			build: func() []StructureFragment {
				result := clone()
				duplicate := result[0]
				duplicate.ID = "role.sub"
				duplicate.Order = 9
				return append(result, duplicate)
			},
			code: IssueDuplicateHeading,
			want: "heading \"## Task Agent Identity\"",
		},
		{
			name: "explicit truncation marker",
			build: func() []StructureFragment {
				result := clone()
				result[9] = withContent(result[9], "## Task and workspace\n- detail [truncated]")
				return result
			},
			code: IssueTruncationMarker,
			want: "explicit truncation marker",
		},
		{
			name: "chinese truncation marker",
			build: func() []StructureFragment {
				result := clone()
				result[9] = withContent(result[9], "## Task and workspace\n- [纯文本正文过长，已按上下文预算截断]")
				return result
			},
			code: IssueTruncationMarker,
			want: "line 2",
		},
		{
			name: "silently truncated task goal",
			build: func() []StructureFragment {
				result := clone()
				result[9] = withContent(result[9], "## Task and workspace\n- 当前目标：定位并修复登录超时的根因...")
				return result
			},
			code: IssueTruncationMarker,
			want: "当前目标",
		},
		{
			name: "overlong block",
			build: func() []StructureFragment {
				result := clone()
				result[9] = withContent(result[9], "## Task and workspace\n"+strings.Repeat("长", 7000))
				return result
			},
			code: IssueBlockTooLong,
			want: "max 6000",
		},
		{
			name: "block order",
			build: func() []StructureFragment {
				result := clone()
				// Move the principles fragments in front of identity.
				return append(append([]StructureFragment{}, result[3:6]...), append(result[:3], result[6:]...)...)
			},
			code: IssueBlockOrder,
			want: "want identity -> principles -> control -> context",
		},
		{
			name: "fragment order inside a block",
			build: func() []StructureFragment {
				result := clone()
				result[5], result[3] = result[3], result[5] // knowledge (order 2) before core (order 0)
				return result
			},
			code: IssueFragmentOrder,
			want: "inside block \"principles\"",
		},
		{
			name: "empty fragment",
			build: func() []StructureFragment {
				result := clone()
				result[0].Content = "   "
				result[0].Chars = 0
				return result
			},
			code: IssueEmptyFragment,
			want: "fragment is empty",
		},
		{
			name: "duplicate content",
			build: func() []StructureFragment {
				result := clone()
				body := strings.Repeat("同一段足够长的规则文本。", 10)
				result[4] = withContent(result[4], body)
				result[5] = withContent(result[5], body)
				return result
			},
			code:    IssueDuplicateContent,
			want:    "duplicates fragment",
			warning: true,
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			report := LintStructure(testCase.name, testCase.build())
			if !issueCodes(report)[testCase.code] {
				t.Fatalf("%s: expected code %s, got %#v", testCase.name, testCase.code, report.Issues)
			}
			if testCase.warning {
				if !report.OK() {
					t.Fatalf("%s: warning-only layout failed: %#v", testCase.name, report.Issues)
				}
				return
			}
			if report.OK() {
				t.Fatalf("%s: lint passed, want %s", testCase.name, testCase.code)
			}
			err := report.Err()
			if err == nil {
				t.Fatal("Err() returned nil for a failing report")
			}
			if !strings.Contains(err.Error(), testCase.code) || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error does not explain the failure: %v", err)
			}
		})
	}
}

// A sentence that merely mentions truncation is not a truncation marker. The
// pet core digest and normal prose both contain the word; flagging them would
// make the lint unusable.
func TestTruncationMarkerDoesNotMatchMentionedWord(t *testing.T) {
	t.Parallel()
	result := cloneFragments(compliantStructureFragments())
	result[9].Content = "## Task and workspace\n- 标记截断或规则不清时读取全文。\n- The digest may be marked truncated or unclear."
	if codes := issueCodes(LintStructure("mention", result)); codes[IssueTruncationMarker] {
		t.Fatal("a sentence mentioning truncation was treated as a marker")
	}
}

func cloneFragments(fragments []StructureFragment) []StructureFragment {
	return append([]StructureFragment(nil), fragments...)
}

func TestScenarioStructurePreviewCoversTenScenariosAndOnlyMovesText(t *testing.T) {
	t.Parallel()
	repository := &fixturePromptRepository{}
	engine := NewEngine(repository)
	ctx := context.Background()
	for _, language := range []string{domain.LanguageSimplified, domain.LanguageEnglish} {
		language := language
		t.Run(language, func(t *testing.T) {
			previews, err := engine.ScenarioStructurePreviews(ctx, DefaultScenarioTestMessage, language)
			if err != nil {
				t.Fatal(err)
			}
			if len(previews) != 10 {
				t.Fatalf("expected the 10 stable scenarios, got %d", len(previews))
			}
			seen := map[string]bool{}
			for _, preview := range previews {
				if seen[preview.Scenario] {
					t.Fatalf("duplicate scenario %s", preview.Scenario)
				}
				seen[preview.Scenario] = true

				if strings.TrimSpace(preview.Before.Prompt) == "" || strings.TrimSpace(preview.After.Prompt) == "" {
					t.Fatalf("%s produced an empty prompt", preview.Scenario)
				}
				if preview.Before.PromptChars != len([]rune(preview.Before.Prompt)) {
					t.Fatalf("%s before prompt_chars disagrees with its prompt", preview.Scenario)
				}
				if preview.After.PromptChars != len([]rune(preview.After.Prompt)) {
					t.Fatalf("%s after prompt_chars disagrees with its prompt", preview.Scenario)
				}

				// Production already renders the recommended order (fixed rules
				// first, the per-Turn context last), so the proposed layout is the
				// production layout and there is nothing left to move.
				want := []PromptBlockID{BlockIdentity, BlockPrinciples, BlockControl, BlockContext}
				if !equalBlockOrder(preview.Before.BlockOrder, want) {
					t.Fatalf("%s production block order=%v, want %v", preview.Scenario, preview.Before.BlockOrder, want)
				}
				if !equalBlockOrder(preview.After.BlockOrder, want) {
					t.Fatalf("%s after block order=%v, want %v", preview.Scenario, preview.After.BlockOrder, want)
				}
				if err := (LintReport{Label: preview.Scenario, Issues: preview.Before.Issues}).Err(); err != nil {
					t.Fatalf("%s production layout failed lint: %v", preview.Scenario, err)
				}
				if err := (LintReport{Label: preview.Scenario, Issues: preview.After.Issues}).Err(); err != nil {
					t.Fatalf("%s after layout failed lint: %v", preview.Scenario, err)
				}
				if preview.Before.Prompt != preview.After.Prompt || preview.AddedLines != 0 || preview.RemovedLines != 0 {
					t.Fatalf("%s proposed layout still differs from production: added=%d removed=%d",
						preview.Scenario, preview.AddedLines, preview.RemovedLines)
				}
				if !sameLineMultiset(preview.Before.Prompt, preview.After.Prompt) {
					t.Fatalf("%s preview changed prompt text instead of only reordering it", preview.Scenario)
				}
				for _, block := range preview.Before.Blocks {
					if block.Chars > block.MaxRunes {
						t.Fatalf("%s block %s is over its cap: %d > %d", preview.Scenario, block.ID, block.Chars, block.MaxRunes)
					}
				}
			}
		})
	}
	if repository.upserts != 0 || repository.deletes != 0 {
		t.Fatalf("structure preview wrote to the repository: upserts=%d deletes=%d", repository.upserts, repository.deletes)
	}
}

func TestScenarioStructurePreviewBeforeMatchesProductionBuild(t *testing.T) {
	t.Parallel()
	engine := NewEngine(&fixturePromptRepository{})
	ctx := context.Background()
	before, err := engine.PreviewScenario(ctx, ScenarioChannelGroupTaskMulti, DefaultScenarioTestMessage,
		ScenarioSourceChannelGroupBot, domain.LanguageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	structure, err := engine.PreviewScenarioStructure(ctx, ScenarioChannelGroupTaskMulti, DefaultScenarioTestMessage,
		ScenarioSourceChannelGroupBot, domain.LanguageEnglish)
	if err != nil {
		t.Fatal(err)
	}
	if structure.Before.Prompt != before.Prompt {
		t.Fatal("structure preview before-prompt diverged from the production scenario preview")
	}
	if structure.MessageSource != ScenarioSourceChannelGroupBot || structure.MessageSourceName == "" {
		t.Fatalf("structure preview lost the message source: %#v", structure)
	}
}

func TestScenarioStructurePreviewRejectsUnknownScenarioAndSource(t *testing.T) {
	t.Parallel()
	engine := NewEngine(&fixturePromptRepository{})
	ctx := context.Background()
	if _, err := engine.PreviewScenarioStructure(ctx, "nope", "", "", ""); !errors.Is(err, ErrUnknownScenario) {
		t.Fatalf("unknown scenario error=%v, want ErrUnknownScenario", err)
	}
	if _, err := engine.PreviewScenarioStructure(ctx, ScenarioWebTaskSingleMain, "", "system", ""); !errors.Is(err, ErrInvalidMessageSource) {
		t.Fatalf("invalid source error=%v, want ErrInvalidMessageSource", err)
	}
	oversized := strings.Repeat("长", MaxScenarioTestMessageRunes+1)
	if _, err := engine.PreviewScenarioStructure(ctx, ScenarioWebTaskSingleMain, oversized, "", ""); !errors.Is(err, ErrScenarioMessageTooLong) {
		t.Fatalf("oversized message error=%v, want ErrScenarioMessageTooLong", err)
	}
}

func TestUnifiedDiffReportsAMovedBlockAndPreservesText(t *testing.T) {
	t.Parallel()
	before := "## A\nrule a\n\n## B\nrule b"
	after := "## B\nrule b\n\n## A\nrule a"
	diff, added, removed := unifiedDiff("before", "after", before, after)
	for _, want := range []string{"--- before", "+++ after", "## A", "## B"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
	if added == 0 || removed == 0 {
		t.Fatalf("diff counts added=%d removed=%d, want both nonzero", added, removed)
	}
	if !sameLineMultiset(before, after) {
		t.Fatal("test fixture is not a pure reorder")
	}
}

func equalBlockOrder(got, want []PromptBlockID) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func sameLineMultiset(left, right string) bool {
	leftLines := sortedNonEmptyLines(left)
	rightLines := sortedNonEmptyLines(right)
	if len(leftLines) != len(rightLines) {
		return false
	}
	for index := range leftLines {
		if leftLines[index] != rightLines[index] {
			return false
		}
	}
	return true
}

func sortedNonEmptyLines(value string) []string {
	lines := make([]string, 0, strings.Count(value, "\n")+1)
	for _, line := range strings.Split(value, "\n") {
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return lines
}
