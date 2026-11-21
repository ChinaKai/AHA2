package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func TestConversationPageLatestDoesNotSkipUnreturnedItems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	add := func(category, summary string) int64 {
		t.Helper()
		item, addErr := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, TurnID: "turn-page",
			AgentID: "main", StreamAgentID: "main", FromAgentID: "main", ToAgentID: "owner",
			RouteKind: "agent_progress", Category: category, Kind: "agent_message_update",
			Summary: summary, CreatedAt: now,
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		return item.Sequence
	}

	firstSequence := add("update", "first")
	add("tool", "hidden")
	initial, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 20, []string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Items) != 1 || initial.Latest != firstSequence {
		t.Fatalf("initial page advanced beyond returned items: %#v", initial)
	}

	secondSequence := add("update", "second")
	thirdSequence := add("update", "third")
	add("update", "fourth")
	incremental, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, initial.Latest, 2, []string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	if len(incremental.Items) != 2 || !incremental.HasMore || incremental.Latest != thirdSequence {
		t.Fatalf("incremental page skipped its remaining item: %#v", incremental)
	}
	if incremental.Items[0].Sequence != secondSequence || incremental.Items[1].Sequence != thirdSequence {
		t.Fatalf("incremental page order is invalid: %#v", incremental.Items)
	}

	remaining, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, incremental.Latest, 2, []string{"update"})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Items) != 1 || remaining.Items[0].Summary != "fourth" || remaining.Latest != remaining.Items[0].Sequence {
		t.Fatalf("remaining item was not reachable: %#v", remaining)
	}
}

// Thinking is split from Agent Update by the stored reasoning kind. The mixed
// fixture interleaves every visible category so filters, before-pagination
// and after-increments are checked against the exact expected row sets.
func TestConversationPageSplitsThinkingFromUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "aha2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	task := createStoreTestTask(t, database)
	now := time.Now().UTC()
	type row struct{ category, kind string }
	fixture := []row{
		{"chat", "user_message"},
		{"update", "agent_progress"},
		{"update", domain.ConversationKindReasoning},
		{"tool", "agent_command_started"},
		{"update", "agent_message_update"},
		{"update", domain.ConversationKindReasoning},
		{"tool", "agent_command_finished"},
		{"error", "agent_error"},
		{"update", domain.ConversationKindReasoning},
		{"update", "turn_duration"},
		{"chat", "agent_message"},
	}
	bucket := func(r row) string {
		if r.category == "update" && r.kind == domain.ConversationKindReasoning {
			return "thinking"
		}
		return r.category
	}
	sequences := map[string][]int64{}
	add := func(r row) int64 {
		t.Helper()
		item, addErr := database.AddConversationItem(ctx, domain.ConversationItem{
			ID: domain.NewID("conversation"), TaskID: task.ID, TurnID: "turn-split",
			AgentID: "main", StreamAgentID: "main", Category: r.category, Kind: r.kind,
			Summary: r.category + "/" + r.kind, CreatedAt: now,
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		sequences[bucket(r)] = append(sequences[bucket(r)], item.Sequence)
		return item.Sequence
	}
	for _, r := range fixture {
		add(r)
	}
	expected := func(categories ...string) []int64 {
		var result []int64
		for _, category := range categories {
			result = append(result, sequences[category]...)
		}
		sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
		return result
	}
	got := func(page domain.ConversationPage) []int64 {
		result := make([]int64, 0, len(page.Items))
		for _, item := range page.Items {
			result = append(result, item.Sequence)
		}
		return result
	}
	cases := []struct {
		name       string
		categories []string
		want       []int64
	}{
		{"default chat+error+update has no thinking", []string{"chat", "error", "update"}, expected("chat", "error", "update")},
		{"update only", []string{"update"}, expected("update")},
		{"thinking only", []string{"thinking"}, expected("thinking")},
		{"update and thinking", []string{"update", "thinking"}, expected("update", "thinking")},
		{"thinking with chat and tool", []string{"chat", "thinking", "tool"}, expected("chat", "thinking", "tool")},
		{"all five", []string{"chat", "error", "update", "thinking", "tool"}, expected("chat", "error", "update", "thinking", "tool")},
		{"duplicates collapse", []string{"thinking", "thinking", "error"}, expected("thinking", "error")},
		{"no filter returns all", nil, expected("chat", "error", "update", "thinking", "tool")},
	}
	for _, tc := range cases {
		page, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, tc.categories)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got(page)) != fmt.Sprint(tc.want) || page.HasMore {
			t.Fatalf("%s: got %v has_more=%t, want %v", tc.name, got(page), page.HasMore, tc.want)
		}
	}

	// Paging backwards one row at a time visits every filtered row once.
	for _, categories := range [][]string{{"thinking"}, {"update"}, {"chat", "error", "update"}, {"update", "thinking"}} {
		var walked []int64
		before := int64(0)
		for guard := 0; guard < 50; guard++ {
			page, err := database.ConversationPageForAgent(ctx, task.ID, "main", before, 0, 1, categories)
			if err != nil {
				t.Fatal(err)
			}
			walked = append(got(page), walked...)
			if !page.HasMore {
				break
			}
			before = page.NextBefore
		}
		want := map[string]bool{}
		for _, c := range categories {
			want[c] = true
		}
		var cats []string
		for c := range want {
			cats = append(cats, c)
		}
		if fmt.Sprint(walked) != fmt.Sprint(expected(cats...)) {
			t.Fatalf("paging %v: walked %v, want %v", categories, walked, expected(cats...))
		}
	}

	// Incremental refresh after the latest cursor: new rows of every kind
	// arrive; each filter sees only its own new rows and the cursor never
	// skips a row it did not return.
	thinkingLatest, _ := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, []string{"thinking"})
	updateLatest, _ := database.ConversationPageForAgent(ctx, task.ID, "main", 0, 0, 100, []string{"chat", "error", "update"})
	newUpdate := add(row{"update", "agent_progress"})
	newThinking := add(row{"update", domain.ConversationKindReasoning})
	newTool := add(row{"tool", "agent_command_started"})
	secondThinking := add(row{"update", domain.ConversationKindReasoning})
	incThinking, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, thinkingLatest.Latest, 1, []string{"thinking"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got(incThinking)) != fmt.Sprint([]int64{newThinking}) || !incThinking.HasMore || incThinking.Latest != newThinking {
		t.Fatalf("incremental thinking page: %#v", incThinking)
	}
	incThinking, err = database.ConversationPageForAgent(ctx, task.ID, "main", 0, incThinking.Latest, 1, []string{"thinking"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got(incThinking)) != fmt.Sprint([]int64{secondThinking}) || incThinking.HasMore {
		t.Fatalf("second incremental thinking page: %#v", incThinking)
	}
	incUpdate, err := database.ConversationPageForAgent(ctx, task.ID, "main", 0, updateLatest.Latest, 100, []string{"chat", "error", "update"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got(incUpdate)) != fmt.Sprint([]int64{newUpdate}) {
		t.Fatalf("incremental default page leaked thinking/tool: got %v want [%d] (tool %d)", got(incUpdate), newUpdate, newTool)
	}
}
