package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// petInsightsFixture fixes the clock at local 2026-11-26 10:00 so the window
// is exactly [2026-11-19 00:00, now) in the server's local time zone.
type petInsightsFixture struct {
	*petTaskFixture
	t        *testing.T
	now      time.Time
	from     time.Time
	pet      domain.Task
	sequence int
}

func newPetInsightsFixture(t *testing.T) *petInsightsFixture {
	t.Helper()
	base := newPetTaskFixture(t)
	pet := base.createPetDraft(t, "insights fixture")
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.Local)
	base.service.now = func() time.Time { return now }
	return &petInsightsFixture{
		petTaskFixture: base, t: t, now: now, pet: pet,
		from: time.Date(2026, 11, 19, 0, 0, 0, 0, time.Local),
	}
}

func (f *petInsightsFixture) event(actor, kind, detail string, at time.Time) {
	f.t.Helper()
	if err := f.database.AppendPetEvent(f.ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: at.UTC(), Actor: actor, Kind: kind, Detail: detail, Status: "open",
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *petInsightsFixture) approval(id, kind, status string, at time.Time) {
	f.t.Helper()
	if err := f.database.CreatePetApproval(f.ctx, domain.PetApproval{
		ID: id, TaskID: petTaskID, Kind: kind, Title: id, Summary: id, Risk: "low", Status: status, CreatedAt: at.UTC(),
	}); err != nil {
		f.t.Fatal(err)
	}
}

// turn writes a finished pet Main Turn. sourceKinds are the inbox items of
// the batch it consumed; none means a Turn without a batch.
func (f *petInsightsFixture) turn(agentID string, started time.Time, result string, usage map[string]any, sourceKinds ...string) domain.Turn {
	f.t.Helper()
	f.sequence++
	message := domain.Message{ID: fmt.Sprintf("message-insights-%d", f.sequence), TaskID: petTaskID, Role: "user", Sender: "aha", Content: "wake", CreatedAt: started.UTC()}
	if err := f.database.AddMessage(f.ctx, message); err != nil {
		f.t.Fatal(err)
	}
	turn := domain.Turn{
		ID: fmt.Sprintf("turn-insights-%d", f.sequence), TaskID: petTaskID, AgentID: agentID, Sequence: f.sequence,
		InputMessageID: message.ID, Status: domain.TurnSucceeded, Result: result, Usage: usage,
		RoundID: fmt.Sprintf("round-insights-%d", f.sequence), RuntimeConfigSnapshotID: f.pet.RuntimeConfigSnapshotID,
		QueuedAt: started.UTC(), StartedAt: started.UTC(), FinishedAt: started.Add(time.Minute).UTC(),
	}
	if len(sourceKinds) > 0 {
		turn.InboxBatchID = fmt.Sprintf("batch-insights-%d", f.sequence)
	}
	if agentID != "main" {
		if _, err := f.database.UpsertTaskAgent(f.ctx, domain.TaskAgent{TaskID: petTaskID, AgentID: agentID, Role: "sub", Status: "idle", RuntimeConfigSnapshotID: f.pet.RuntimeConfigSnapshotID, CreatedAt: started.UTC(), UpdatedAt: started.UTC()}); err != nil {
			f.t.Fatal(err)
		}
	}
	if err := f.database.CreateTurn(f.ctx, turn); err != nil {
		f.t.Fatal(err)
	}
	if len(sourceKinds) > 0 {
		raw, err := sql.Open("sqlite", f.databasePath+"?_pragma=busy_timeout(5000)")
		if err != nil {
			f.t.Fatal(err)
		}
		defer raw.Close()
		for index, kind := range sourceKinds {
			// A separate connection without foreign_keys, so the row needs no
			// Round; only batch_id and source_kind matter to the desk.
			result, err := raw.Exec(`
				INSERT INTO agent_inbox(id,task_id,round_id,target_agent_id,source_agent_id,source_kind,content,status,batch_id,created_at)
				VALUES(?,?,?,'main','aha',?,'wake','processed',?,?)`,
				fmt.Sprintf("inbox-insights-%d-%d", f.sequence, index), petTaskID, turn.RoundID, kind, turn.InboxBatchID, timeString(started.UTC()),
			)
			if err != nil {
				f.t.Fatal(err)
			}
			if rows, _ := result.RowsAffected(); rows != 1 {
				f.t.Fatalf("inbox insert affected %d rows", rows)
			}
		}
	}
	return turn
}

func TestPetInsightsCountsTheSevenDayWindowAndItsEdges(t *testing.T) {
	f := newPetInsightsFixture(t)
	inside := f.from.Add(2 * time.Hour)
	edge := f.from                         // exactly at the start: inside
	before := f.from.Add(-time.Nanosecond) // one tick before: outside

	// Actions: deduped by kind + target; non-actions, other actors and
	// out-of-window rows are not counted.
	f.event("pet", "task_created", "task-a", inside)
	f.event("pet", "task_created", "task-a", inside.Add(time.Minute)) // same action again
	f.event("pet", "task_message_sent", "task-a", inside)             // different kind, same target
	f.event("pet", "knowledge_submitted", "k-1", edge)
	f.event("pet", "task_created", "task-old", before)
	f.event("pet", "core_changed", "principles.md", inside) // not an action
	f.event("owner", "task_created", "task-owner", inside)  // not the pet
	f.event("pet", "task_started", "", inside)              // no target

	// Approvals: ideas separate; executing counts as still pending.
	f.approval("a-approved", "owner_decision", petApprovalApproved, inside)
	f.approval("a-rejected", "delete_task", petApprovalRejected, inside)
	f.approval("a-cancelled", "owner_decision", petApprovalCancelled, edge)
	f.approval("a-pending", "owner_decision", petApprovalPending, inside)
	f.approval("a-executing", "delete_workspace", petApprovalExecuting, inside)
	f.approval("a-idea", "idea", petApprovalPending, inside)
	f.approval("a-idea-done", "idea", petApprovalApproved, inside)
	f.approval("a-old", "owner_decision", petApprovalApproved, before)
	f.approval("a-old-idea", "idea", petApprovalPending, before)

	// Corrections: absorbed ones still count; out-of-window ones do not.
	f.event("owner", petCorrectionEventKind, `{"owner_note":"a"}`, inside)
	f.event("owner", petCorrectionEventKind, `{"owner_note":"b"}`, edge)
	f.event("owner", petCorrectionEventKind, `{"owner_note":"c"}`, before)
	if _, err := f.database.MarkOpenPetEvents(f.ctx, []string{petCorrectionEventKind}, nil, "absorbed"); err != nil {
		t.Fatal(err)
	}

	// Wakes: Main Turns started in the window; NOOP replies counted apart.
	f.turn("main", edge, "NOOP · scope all, nothing changed", nil, domain.PetWakeSourceKind)
	f.turn("main", inside, "  noop", nil, domain.PetWakeSourceKind)
	f.turn("main", inside.Add(time.Hour), "Started task-a for the owner.", nil, domain.PetWakeSourceKind)
	f.turn("main", before, "NOOP", nil, domain.PetWakeSourceKind)
	f.turn("helper", inside, "NOOP", nil) // not the Main Agent

	// Rows after the window end (the fixed clock) are outside too. The store
	// would hold them when the server clock lags a writer, or in a test.
	after := f.now.Add(time.Second)
	f.event("pet", "task_created", "task-future", after)
	f.event("pet", "task_created", "task-at-end", f.now) // [from, to): the end is exclusive
	f.approval("a-future", "owner_decision", petApprovalPending, after)
	f.approval("a-future-idea", "idea", petApprovalPending, after)
	f.event("owner", petCorrectionEventKind, `{"owner_note":"future"}`, after)
	f.turn("main", after, "NOOP", nil, domain.PetWakeSourceKind)
	f.turn("main", f.now, "at the end", nil, domain.PetWakeSourceKind)

	insights, err := f.service.PetInsights(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !insights.From.Equal(f.from) || !insights.To.Equal(f.now) {
		t.Fatalf("window = %s .. %s, want %s .. %s", insights.From, insights.To, f.from, f.now)
	}
	if insights.Actions != 3 {
		t.Fatalf("actions = %d, want 3 (task_created:task-a, task_message_sent:task-a, knowledge_submitted:k-1)", insights.Actions)
	}
	want := PetInsightApprovals{Total: 5, Approved: 1, Rejected: 1, Cancelled: 1, Pending: 2, Ideas: 2}
	if insights.Approvals != want {
		t.Fatalf("approvals = %+v, want %+v", insights.Approvals, want)
	}
	if insights.Corrections != 2 {
		t.Fatalf("corrections = %d, want 2", insights.Corrections)
	}
	if insights.Wakes.Turns != 3 || insights.Wakes.Noop != 2 {
		t.Fatalf("wakes = %+v, want 3 turns with 2 NOOP", insights.Wakes)
	}
	if insights.Wakes.TokenScope != PetTokenScopeCumulative || insights.Wakes.Tokens != 0 {
		t.Fatalf("without a token counter the scope is cumulative and zero: %+v", insights.Wakes)
	}
}

func TestPetInsightsPassesOnlyWindowTurnsToTheTokenCounter(t *testing.T) {
	f := newPetInsightsFixture(t)
	inWindow := f.turn("main", f.from.Add(time.Hour), "done", map[string]any{"input_tokens": float64(10)}, domain.PetWakeSourceKind)
	f.turn("main", f.from.Add(-time.Hour), "done", map[string]any{"input_tokens": float64(99)}, domain.PetWakeSourceKind)
	var got []string
	insights, err := f.service.PetInsights(f.ctx, func(_ context.Context, taskID string, turns []domain.Turn) (int64, string, error) {
		if taskID != petTaskID {
			t.Fatalf("token counter task = %q", taskID)
		}
		for _, turn := range turns {
			got = append(got, turn.ID)
		}
		return 42, PetTokenScopeWindow, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != inWindow.ID {
		t.Fatalf("window turns = %v, want only %s", got, inWindow.ID)
	}
	if insights.Wakes.Tokens != 42 || insights.Wakes.TokenScope != PetTokenScopeWindow {
		t.Fatalf("wakes = %+v", insights.Wakes)
	}
}

func TestPetDeskLastReplyPrefersWakesAndMarksNoop(t *testing.T) {
	f := newPetInsightsFixture(t)
	if reply, err := f.service.petDeskLastReply(f.ctx); err != nil || reply != nil {
		t.Fatalf("no finished Turn: reply=%+v err=%v", reply, err)
	}
	long := strings.Repeat("长", 450)
	f.turn("main", f.now.Add(-3*time.Hour), long, nil, domain.PetWakeSourceKind)
	f.turn("main", f.now.Add(-2*time.Hour), "chatting with the owner", nil, "owner_message")
	reply, err := f.service.petDeskLastReply(f.ctx)
	if err != nil || reply == nil {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
	if !reply.FromWake || reply.Noop || len([]rune(reply.Text)) != petLastReplyRunes || !strings.HasSuffix(reply.Text, "…") {
		t.Fatalf("the newest wake reply wins over a newer chat reply and is cut to 400 runes: %+v (len %d)", reply, len([]rune(reply.Text)))
	}
	f.turn("main", f.now.Add(-time.Hour), "NOOP · nothing to move", nil, domain.PetWakeSourceKind, "owner_message")
	if reply, _ = f.service.petDeskLastReply(f.ctx); !reply.Noop || !reply.FromWake || !reply.FinishedAt.Equal(f.now.Add(-time.Hour+time.Minute).UTC()) {
		t.Fatalf("a mixed wake batch still counts as a wake, NOOP detected: %+v", reply)
	}

	// Without any wake reply the latest reply is used and marked as such.
	g := newPetInsightsFixture(t)
	g.turn("main", g.now.Add(-time.Hour), "hello owner", nil, "owner_message")
	g.turn("main", g.now.Add(-30*time.Minute), "", nil, domain.PetWakeSourceKind) // no text: skipped
	if reply, err = g.service.petDeskLastReply(g.ctx); err != nil || reply == nil || reply.FromWake || reply.Text != "hello owner" {
		t.Fatalf("fallback reply = %+v err=%v", reply, err)
	}
}

func TestPetDeskListsLedgerWaitsExceptClosedAndApprovalLinked(t *testing.T) {
	f := newPetDeskFixture(t)
	root := f.service.petWorkspaceRoot(f.ctx)
	if err := os.MkdirAll(filepath.Join(root, "core"), 0o700); err != nil {
		t.Fatal(err)
	}
	loops := strings.Join([]string{
		"# 开放闭环",
		"- 每项写：状态（open/waiting/blocked）| 事项 | 下一步 | 关闭条件",
		"- waiting | 等合作方答复接口格式 | 周五再问一次 | 拿到格式",
		"- open | 自己能推进的事 | 写脚本 | 跑通",
		"  - waiting | 缩进的备注不是条目 | x | y",
		"- waiting | 等 owner 决定 pet_approval_0123456789abcdef0123 | 等审批 | 批了",
		"## 已关闭",
		"- waiting | 已关闭小节里的等待 | 不该出现 | -",
		"## 其他",
		"- [waiting] | 等评审意见 | 看完再改 | 评审通过",
	}, "\n")
	// The approval id only in the fourth column (the "needs the Owner"
	// column) still links the row to the pending approval.
	agenda := "# 自主议程\n- waiting | 等渠道恢复 | 恢复后重发 | 发出 | 否\n- next | 下一项 | 做 | 完 | 否\n" +
		"- waiting | 等 owner 拍板 | 等 | 批了 | 是，见 pet_approval_0123456789abcdef0123\n"
	for name, content := range map[string]string{petCoreLoopsPath: loops, petCoreAgendaPath: agenda} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.database.CreatePetApproval(f.ctx, domain.PetApproval{
		ID: "pet_approval_0123456789abcdef0123", TaskID: petTaskID, Kind: "owner_decision", Title: "decide", Summary: "x",
		Status: petApprovalPending, CreatedAt: f.now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	desk := f.desk()
	var got []string
	for _, wait := range desk.WaitingOthers {
		if wait.Kind != PetDeskWaitLoop {
			continue
		}
		if !wait.Since.IsZero() {
			t.Fatalf("loop waits carry no time: %+v", wait)
		}
		got = append(got, wait.On+"|"+wait.Title+"|"+wait.Detail)
	}
	want := []string{
		"core/loops.md|等合作方答复接口格式|周五再问一次",
		"core/loops.md|等评审意见|看完再改",
		"core/agenda.md|等渠道恢复|恢复后重发",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("loop waits:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if desk.WaitingOthersTotal != len(desk.WaitingOthers) || desk.WaitingOthersTotal < 3 {
		t.Fatalf("loop waits count toward the total: total=%d rows=%d", desk.WaitingOthersTotal, len(desk.WaitingOthers))
	}
}

func TestPetDeskLedgerWaitsShareTheWaitingLimit(t *testing.T) {
	f := newPetDeskFixture(t)
	root := f.service.petWorkspaceRoot(f.ctx)
	if err := os.MkdirAll(filepath.Join(root, "core"), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{"# 开放闭环"}
	for index := 0; index < petDeskWaitingLimit+5; index++ {
		lines = append(lines, fmt.Sprintf("- waiting | item %02d | next | done", index))
	}
	if err := os.WriteFile(filepath.Join(root, petCoreLoopsPath), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	// A timed wait (an approval being carried out) must survive the limit.
	if err := f.database.CreatePetApproval(f.ctx, domain.PetApproval{
		ID: "approval-running", TaskID: petTaskID, Kind: "delete_workspace", Title: "deleting", Summary: "x",
		Status: petApprovalExecuting, CreatedAt: f.now.Add(-time.Hour), ResolvedAt: f.now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	desk := f.desk()
	if len(desk.WaitingOthers) != petDeskWaitingLimit || desk.WaitingOthersTotal != petDeskWaitingLimit+6 {
		t.Fatalf("waiting rows=%d total=%d, want %d of %d", len(desk.WaitingOthers), desk.WaitingOthersTotal, petDeskWaitingLimit, petDeskWaitingLimit+6)
	}
	if first := desk.WaitingOthers[0]; first.Kind != PetDeskWaitExecuting {
		t.Fatalf("timed waits lead the untimed loop waits: first=%+v", first)
	}
	if second := desk.WaitingOthers[1]; second.Kind != PetDeskWaitLoop || second.Title != "item 00" {
		t.Fatalf("loop waits keep file order after the timed ones: second=%+v", second)
	}
}

func TestPetApprovalReadsOneApprovalOrNoRows(t *testing.T) {
	f := newPetInsightsFixture(t)
	f.approval("pet_approval_one", "owner_decision", petApprovalPending, f.now)
	item, err := f.service.PetApproval(f.ctx, " pet_approval_one ")
	if err != nil || item.ID != "pet_approval_one" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if _, err := f.service.PetApproval(f.ctx, "missing"); err != sql.ErrNoRows {
		t.Fatalf("missing approval err = %v, want sql.ErrNoRows", err)
	}
}
