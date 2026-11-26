package app

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// petInsightsDays is the number of whole days before today in the workbench
// "last 7 days" block. The window is those days plus today so far, aligned to
// local midnight on the server.
const petInsightsDays = 7

// Token scope values. The page labels the number by this field.
const (
	// PetTokenScopeWindow means the tokens were summed over the Main Turns
	// that started inside the window.
	PetTokenScopeWindow = "window"
	// PetTokenScopeCumulative means the number is the pet Task's whole
	// history, because the window could not be split reliably.
	PetTokenScopeCumulative = "cumulative"
)

// PetInsightApprovals is the approvals block: requests the pet put to the
// Owner (everything except ideas) by outcome, and ideas on their own.
type PetInsightApprovals struct {
	Total     int `json:"total"`
	Approved  int `json:"approved"`
	Rejected  int `json:"rejected"`
	Cancelled int `json:"cancelled"`
	// Pending also counts "executing": the Owner approved and AHA is still
	// carrying it out, so the outcome is not settled yet.
	Pending int `json:"pending"`
	Ideas   int `json:"ideas"`
}

// PetInsightWakes is how often the pet was woken and what it cost.
type PetInsightWakes struct {
	Turns int `json:"turns"`
	Noop  int `json:"noop"`
	// Tokens uses the Task list's token algorithm (TokenTotal). TokenScope
	// says whether it covers the window or the Task's whole history.
	Tokens     int64  `json:"tokens"`
	TokenScope string `json:"token_scope"`
}

// PetInsights is the workbench "last 7 days" block. Every number is counted
// from records AHA already keeps; nothing is estimated.
type PetInsights struct {
	From        time.Time           `json:"from"`
	To          time.Time           `json:"to"`
	Actions     int                 `json:"actions"`
	Approvals   PetInsightApprovals `json:"approvals"`
	Corrections int                 `json:"corrections"`
	Wakes       PetInsightWakes     `json:"wakes"`
	// The records page asks for these two; the overview does not, so its
	// "last 7 days" block never waits for a knowledge read.
	Knowledge         *PetKnowledgeHealth    `json:"knowledge,omitempty"`
	KnowledgeError    string                 `json:"knowledge_error,omitempty"`
	CorrectionHistory *[]PetCorrectionRecord `json:"correction_history,omitempty"`
	CorrectionsError  string                 `json:"corrections_error,omitempty"`
	// Ledgers holds the status counts of loops.md and agenda.md.
	Ledgers []PetLedgerCounts `json:"ledgers,omitempty"`
}

// PetInsightsOptions selects the optional parts of the insights response.
type PetInsightsOptions struct {
	// Records adds the correction history and the knowledge health. A
	// failure in either is reported in its own error field; the counts are
	// still returned.
	Records bool
}

// PetInsightsTokenCounter totals token usage for Turns the same way the Task
// list does. The HTTP layer owns that algorithm (taskTotalTokens), so it is
// injected instead of copied here.
type PetInsightsTokenCounter func(ctx context.Context, taskID string, windowTurns []domain.Turn) (int64, string, error)

// petInsightsWindow returns [from, to): local midnight petInsightsDays days
// before today, up to now.
func petInsightsWindow(now time.Time) (time.Time, time.Time) {
	local := now.Local()
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	return midnight.AddDate(0, 0, -petInsightsDays), now
}

// PetInsights counts the pet's last seven days. tokens may be nil, in which
// case the token number is left at zero with the cumulative scope.
func (s *Service) PetInsights(ctx context.Context, tokens PetInsightsTokenCounter, options ...PetInsightsOptions) (PetInsights, error) {
	from, to := petInsightsWindow(s.now())
	result := PetInsights{From: from.UTC(), To: to.UTC()}

	kinds := make([]string, 0, len(petWorkKinds))
	for kind := range petWorkKinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	events, err := s.store.ListPetActionEventsInWindow(ctx, "pet", kinds, from, to)
	if err != nil {
		return PetInsights{}, err
	}
	// Same dedupe as the work state's recent-work list: one action per kind
	// and target, so retried tool calls on the same object count once.
	seen := map[string]bool{}
	for _, event := range events {
		target := strings.TrimSpace(event.Detail)
		if target == "" {
			continue
		}
		seen[event.Kind+":"+target] = true
	}
	result.Actions = len(seen)

	counts, err := s.store.PetApprovalCountsInWindow(ctx, from, to)
	if err != nil {
		return PetInsights{}, err
	}
	for _, group := range counts {
		if group.Kind == "idea" {
			result.Approvals.Ideas += group.Count
			continue
		}
		result.Approvals.Total += group.Count
		switch group.Status {
		case petApprovalApproved:
			result.Approvals.Approved += group.Count
		case petApprovalRejected:
			result.Approvals.Rejected += group.Count
		case petApprovalCancelled:
			result.Approvals.Cancelled += group.Count
		default:
			result.Approvals.Pending += group.Count
		}
	}

	// Every owner_correction event in the window, absorbed or not.
	if result.Corrections, err = s.store.CountPetEventsInWindow(ctx, petCorrectionEventKind, from, to); err != nil {
		return PetInsights{}, err
	}

	wakes, err := s.store.CountMainTurnsStartedInWindow(ctx, petTaskID, from, to)
	if err != nil {
		return PetInsights{}, err
	}
	result.Wakes.Turns, result.Wakes.Noop = wakes.Started, wakes.Noop
	result.Wakes.TokenScope = PetTokenScopeCumulative
	if tokens != nil {
		turns, err := s.store.ListMainTurnTokenUsageInWindow(ctx, petTaskID, from, to)
		if err != nil {
			return PetInsights{}, err
		}
		total, scope, err := tokens(ctx, petTaskID, turns)
		if err != nil {
			return PetInsights{}, err
		}
		result.Wakes.Tokens, result.Wakes.TokenScope = total, scope
	}
	if len(options) > 0 && options[0].Records {
		// A pointer, so an empty history is still sent as [] while the
		// overview read (not asked) leaves the field out; the records page
		// treats a missing field as "could not read".
		if history, err := s.PetCorrectionHistory(ctx); err != nil {
			result.CorrectionsError = "pet_corrections_failed"
		} else {
			result.CorrectionHistory = &history
		}
		result.Ledgers = s.PetLedgerStatusCounts(ctx)
		if health, err := s.PetKnowledgeHealth(ctx); err != nil {
			result.KnowledgeError = "pet_knowledge_health_failed"
		} else {
			result.Knowledge = &health
		}
	}
	return result, nil
}

// PetApproval reads one pet approval by ID for the Owner's workbench.
func (s *Service) PetApproval(ctx context.Context, id string) (domain.PetApproval, error) {
	return s.store.PetApproval(ctx, strings.TrimSpace(id))
}

/* Last reply (desk) ------------------------------------------------------ */

const petLastReplyRunes = 400

// PetDeskLastReply is the pet's most recent finished reply on the desk.
type PetDeskLastReply struct {
	Text       string    `json:"text"`
	FinishedAt time.Time `json:"finished_at"`
	Noop       bool      `json:"noop"`
	// FromWake is true when the Turn answered a server wake (agent_inbox
	// source_kind pet_wake). The desk prefers those; when no wake reply
	// exists it shows the latest reply with FromWake false.
	FromWake bool `json:"from_wake"`
}

// petReplyIsNoop matches the wake contract: a quiet wake replies "NOOP",
// optionally followed by the one-line inspection summary.
func petReplyIsNoop(text string) bool {
	trimmed := strings.TrimSpace(text)
	return len(trimmed) >= 4 && strings.EqualFold(trimmed[:4], "NOOP")
}

// petDeskLastReply returns the newest finished wake reply, else the newest
// finished reply of any kind, else nil.
func (s *Service) petDeskLastReply(ctx context.Context) (*PetDeskLastReply, error) {
	reply, err := s.store.LatestFinishedMainTurnReply(ctx, petTaskID, true)
	if store.IsNoRows(err) {
		reply, err = s.store.LatestFinishedMainTurnReply(ctx, petTaskID, false)
	}
	if store.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &PetDeskLastReply{
		Text:       truncateAssistantText(reply.Result, petLastReplyRunes),
		FinishedAt: reply.FinishedAt,
		Noop:       petReplyIsNoop(reply.Result),
		FromWake:   reply.FromWake,
	}, nil
}

/* Waiting items from the pet's ledgers ------------------------------------ */

// petDeskLedgerWaits turns the waiting rows of core/loops.md (outside its
// closed sections) and core/agenda.md into desk waits. A row that names a
// pending approval is skipped: that decision is already on the desk under
// "for you".
func petDeskLedgerWaits(loops, agenda string, pendingApprovalIDs []string) []PetDeskWait {
	result := []PetDeskWait{}
	for _, source := range []struct {
		path    string
		content string
	}{
		{petCoreLoopsPath, activePetLoops(loops)},
		{petCoreAgendaPath, agenda},
	} {
		for _, entry := range parsePetLedger(source.content) {
			item := entry.Column(0)
			if entry.Status != "waiting" || item == "" {
				continue
			}
			// The whole row: an approval can be cited in any column, for
			// example the close condition or the "needs the Owner" column.
			if petLedgerNamesApproval(entry.Body, pendingApprovalIDs) {
				continue
			}
			result = append(result, PetDeskWait{
				Kind:   PetDeskWaitLoop,
				Title:  truncateAssistantText(item, petDeskDetailRunes),
				Detail: truncateAssistantText(entry.Column(1), petDeskDetailRunes),
				On:     source.path,
			})
		}
	}
	return result
}

func petLedgerNamesApproval(text string, ids []string) bool {
	for _, id := range ids {
		if id != "" && strings.Contains(text, id) {
			return true
		}
	}
	return false
}

// petReadCoreFile reads one core ledger. A missing or unreadable file is an
// empty ledger: the desk must not fail because the pet has not written it.
func petReadCoreFile(root, relative string) string {
	if root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		return ""
	}
	return string(data)
}
