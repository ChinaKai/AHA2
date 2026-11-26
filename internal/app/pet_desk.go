package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// The desk lists are bounded for rendering; every list keeps its untruncated
// total so the page can say how many rows it is not showing.
const (
	petDeskNeedLimit    = 50
	petDeskLaterLimit   = 30
	petDeskWaitingLimit = 30
	petDeskRunningLimit = 30
	petDeskDetailRunes  = 240
	petDeskStalledLimit = 30
	petDeskRecentLimit  = 30
	petDeskRecentWindow = 7 * 24 * time.Hour
)

// Desk item kinds. They are part of the workbench API contract.
const (
	// PetDeskNeedApproval is a pending pet approval other than an idea.
	PetDeskNeedApproval = "approval"
	// PetDeskNeedQuestion is a waiting Task whose state card lists decisions
	// waiting on the Owner.
	PetDeskNeedQuestion = "question"
	// PetDeskNeedBlocked and PetDeskNeedFailed mirror the Task status.
	PetDeskNeedBlocked = "blocked"
	PetDeskNeedFailed  = "failed"
	// PetDeskNeedStopped is a waiting Task whose latest Main Turn did not
	// finish cleanly, so it cannot continue without the Owner.
	PetDeskNeedStopped = "stopped"
	// PetDeskLaterIdea is a pending pet idea; nothing is blocked on it.
	PetDeskLaterIdea = "idea"
	// PetDeskLaterNextStep is a Task whose last round finished cleanly and
	// which only waits for the Owner's next message.
	PetDeskLaterNextStep = "next_step"

	PetDeskWaitReview    = "review"
	PetDeskWaitLine      = "line"
	PetDeskWaitExecuting = "executing"
	PetDeskWaitChannel   = "channel"
	// PetDeskWaitLoop is a "waiting" row the pet wrote in core/loops.md or
	// core/agenda.md. The files carry no time, so Since stays zero.
	PetDeskWaitLoop = "loop"
)

// PetDeskNeed is one thing on the Owner's side of the desk: an approval to
// resolve or a Task that cannot move until the Owner answers.
type PetDeskNeed struct {
	Kind         string        `json:"kind"`
	ApprovalID   string        `json:"approval_id,omitempty"`
	ApprovalKind string        `json:"approval_kind,omitempty"`
	Risk         string        `json:"risk,omitempty"`
	TaskID       string        `json:"task_id,omitempty"`
	TaskCode     string        `json:"task_code,omitempty"`
	Title        string        `json:"title"`
	Questions    []string      `json:"questions,omitempty"`
	StuckOn      string        `json:"stuck_on,omitempty"`
	Lines        []PetDeskLine `json:"lines,omitempty"`
	Detail       string        `json:"detail,omitempty"`
	Since        time.Time     `json:"since"`
}

// PetDeskWait is something that is moving without the Owner: a review in
// flight, an approved action being executed, or channel messages queued for
// delivery.
type PetDeskWait struct {
	Kind     string `json:"kind"`
	TaskID   string `json:"task_id,omitempty"`
	TaskCode string `json:"task_code,omitempty"`
	Title    string `json:"title"`
	On       string `json:"on,omitempty"`
	Count    int    `json:"count,omitempty"`
	// Detail is the next step of a loop wait.
	Detail string        `json:"detail,omitempty"`
	Lines  []PetDeskLine `json:"lines,omitempty"`
	Since  time.Time     `json:"since"`
}

type PetDeskLine struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Step    string `json:"step"`
	Blocker string `json:"blocker"`
	Who     string `json:"who,omitempty"`
}

type PetDeskRecentTask struct {
	TaskID     string            `json:"task_id"`
	TaskCode   string            `json:"task_code"`
	Title      string            `json:"title"`
	Status     domain.TaskStatus `json:"status"`
	Watched    bool              `json:"watched"`
	LastActive time.Time         `json:"last_active"`
}

// PetDeskRun is a Task that is being worked on right now.
type PetDeskRun struct {
	TaskID     string    `json:"task_id"`
	TaskCode   string    `json:"task_code,omitempty"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	TurnStatus string    `json:"turn_status,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Hardware   []string  `json:"hardware,omitempty"`
	Since      time.Time `json:"since"`
}

// PetDesk is the workbench landing view organised by what the Owner has to do
// rather than by data type. Everything in it is derived from state AHA
// already keeps: pet approvals, Task status, Task state cards, review links,
// channel outbox health, the pet's ledgers and the Owner's corrections.
type PetDesk struct {
	Stalled            []PetDeskStalledTask `json:"stalled"`
	StalledTotal       int                  `json:"stalled_total"`
	Recent             []PetDeskRecentTask  `json:"recent"`
	RecentTotal        int                  `json:"recent_total"`
	NeedsOwner         []PetDeskNeed        `json:"needs_owner"`
	NeedsOwnerTotal    int                  `json:"needs_owner_total"`
	Later              []PetDeskNeed        `json:"later"`
	LaterTotal         int                  `json:"later_total"`
	WaitingOthers      []PetDeskWait        `json:"waiting_others"`
	WaitingOthersTotal int                  `json:"waiting_others_total"`
	Running            []PetDeskRun         `json:"running"`
	RunningTotal       int                  `json:"running_total"`
	Corrections        []PetCorrection      `json:"corrections"`
	OpenLoops          []string             `json:"open_loops"`
	OpenLoopsTotal     int                  `json:"open_loops_total"`
	Agenda             []string             `json:"agenda"`
	AgendaTotal        int                  `json:"agenda_total"`
	// LastReply is the pet's latest finished reply; nil when it has none.
	LastReply *PetDeskLastReply `json:"last_reply,omitempty"`
}

// petDeskInput carries the reads the workbench already made, so building the
// desk does not repeat them.
type petDeskInput struct {
	tasks     []domain.Task
	watches   map[string]domain.PetTaskWatch
	approvals []domain.PetApproval
	channels  []PetChannelHealth
	now       time.Time
}

// petDesk classifies every open, non-ignored Task and every unresolved pet
// approval into the desk lists. The query count is independent of the number
// of waiting Tasks: state cards, latest Main Turns and review links are each
// read in batches. Only running Tasks, which are few, get a per-Task hardware
// lookup.
func (s *Service) petDesk(ctx context.Context, input petDeskInput) (PetDesk, error) {
	desk := PetDesk{
		Stalled: []PetDeskStalledTask{}, Recent: []PetDeskRecentTask{},
		NeedsOwner: []PetDeskNeed{}, Later: []PetDeskNeed{}, WaitingOthers: []PetDeskWait{},
		Running: []PetDeskRun{}, Corrections: []PetCorrection{}, OpenLoops: []string{}, Agenda: []string{},
	}
	pendingApprovals, err := s.store.ListPendingPetApprovals(ctx, 200)
	if err != nil {
		return PetDesk{}, err
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return PetDesk{}, err
	}
	ordinaryProjects := make(map[string]bool, len(projects))
	for _, project := range projects {
		if !domain.IsSystemManagedProject(project) {
			ordinaryProjects[project.ID] = true
		}
	}
	taskByID := make(map[string]domain.Task, len(input.tasks))
	var open []domain.Task
	var waitingIDs, turnIDs, recentIDs []string
	for _, task := range input.tasks {
		taskByID[task.ID] = task
		watch, hasWatch := input.watches[task.ID]
		if task.ID == petTaskID || petWorkStateWatchIgnored(watch) {
			continue
		}
		// Same visibility rule as the follow list: a Task in a system-managed
		// project appears only when the Owner or the pet follows it.
		if !hasWatch && !ordinaryProjects[task.ProjectID] {
			continue
		}
		if task.Status != domain.TaskDraft {
			recentIDs = append(recentIDs, task.ID)
		}
		switch task.Status {
		case domain.TaskWaitingUser, domain.TaskBlocked, domain.TaskFailed:
			waitingIDs = append(waitingIDs, task.ID)
		case domain.TaskActive, domain.TaskPreparing:
		default:
			// Drafts were never started; nothing is waiting on anyone yet.
			continue
		}
		open = append(open, task)
		turnIDs = append(turnIDs, task.ID)
	}
	cards, err := s.store.TaskStateCards(ctx, turnIDs)
	if err != nil {
		return PetDesk{}, err
	}
	turns, err := s.store.LatestMainTurnBriefs(ctx, turnIDs)
	if err != nil {
		return PetDesk{}, err
	}
	reviews, err := s.store.PetCodeReviewLinksBySource(ctx, waitingIDs)
	if err != nil {
		return PetDesk{}, err
	}
	pendingByTask := petPendingApprovalTaskMap(pendingApprovals)
	unresolved, err := s.store.TasksWithUnresolvedPetApproval(ctx)
	if err != nil {
		return PetDesk{}, err
	}
	finishes, err := s.store.LatestTaskTurnFinishes(ctx, recentIDs)
	if err != nil {
		return PetDesk{}, err
	}
	for _, id := range recentIDs {
		task := taskByID[id]
		last := task.UpdatedAt
		if finishes[id].After(last) {
			last = finishes[id]
		}
		if (task.Status == domain.TaskCompleted || task.Status == domain.TaskCancelled) && last.Before(input.now.Add(-petDeskRecentWindow)) {
			continue
		}
		_, watched := input.watches[id]
		desk.Recent = append(desk.Recent, PetDeskRecentTask{
			TaskID: id, TaskCode: task.Code, Title: task.Title, Status: task.Status, Watched: watched, LastActive: last,
		})
	}
	sort.SliceStable(desk.Recent, func(i, j int) bool { return desk.Recent[i].LastActive.After(desk.Recent[j].LastActive) })
	desk.RecentTotal = len(desk.Recent)
	desk.Recent = desk.Recent[:min(len(desk.Recent), petDeskRecentLimit)]

	for _, approval := range pendingApprovals {
		need := PetDeskNeed{
			Kind: PetDeskNeedApproval, ApprovalID: approval.ID, ApprovalKind: approval.Kind, Risk: approval.Risk,
			Title: approval.Title, Detail: truncateAssistantText(approval.Summary, petDeskDetailRunes), Since: approval.CreatedAt,
		}
		if task, ok := taskByID[petApprovalTargetTaskID(approval)]; ok {
			need.TaskID, need.TaskCode = task.ID, task.Code
		}
		if approval.Kind == "idea" {
			need.Kind = PetDeskLaterIdea
			desk.Later = append(desk.Later, need)
			continue
		}
		desk.NeedsOwner = append(desk.NeedsOwner, need)
	}
	for _, approval := range input.approvals {
		if approval.Status != "executing" {
			continue
		}
		wait := PetDeskWait{Kind: PetDeskWaitExecuting, Title: approval.Title, On: approval.Kind, Since: approval.ResolvedAt}
		if wait.Since.IsZero() {
			wait.Since = approval.CreatedAt
		}
		if task, ok := taskByID[petApprovalTargetTaskID(approval)]; ok {
			wait.TaskID, wait.TaskCode = task.ID, task.Code
		}
		desk.WaitingOthers = append(desk.WaitingOthers, wait)
	}

	for _, task := range open {
		card, turn := cards[task.ID], turns[task.ID]
		if task.Status == domain.TaskActive || task.Status == domain.TaskPreparing {
			run := PetDeskRun{
				TaskID: task.ID, TaskCode: task.Code, Title: task.Title, Status: string(task.Status),
				TurnStatus: turn.Status, Detail: truncateAssistantText(card.Status, petDeskDetailRunes), Since: task.UpdatedAt,
			}
			if !domain.TurnStatus(turn.Status).Terminal() {
				switch {
				case !turn.StartedAt.IsZero():
					run.Since = turn.StartedAt
				case !turn.QueuedAt.IsZero():
					run.Since = turn.QueuedAt
				}
			}
			desk.Running = append(desk.Running, run)
			continue
		}
		// A pending approval about this Task already represents it above.
		if pendingByTask[task.ID] != "" {
			continue
		}
		if review := petDeskActiveReview(reviews[task.ID]); review != "" {
			desk.WaitingOthers = append(desk.WaitingOthers, PetDeskWait{
				Kind: PetDeskWaitReview, TaskID: task.ID, TaskCode: task.Code, Title: task.Title, On: review, Since: task.UpdatedAt,
			})
			continue
		}
		need := PetDeskNeed{
			TaskID: task.ID, TaskCode: task.Code, Title: task.Title,
			Questions: petDeskQuestions(card.OpenQuestions), Since: task.UpdatedAt,
		}
		if task.Status == domain.TaskWaitingUser {
			wait := taskStuckOn(card.Lines)
			need.StuckOn = wait.on
			if idle, ok := taskStall(task, card, wait, turn, input.now, false, unresolved[task.ID], false); ok {
				desk.Stalled = append(desk.Stalled, idle)
				continue
			}
			switch wait.on {
			case taskStuckSelf, taskStuckNone:
				need.Kind = PetDeskLaterNextStep
				need.Detail = truncateAssistantText(petDeskFirst(card.NextSteps), petDeskDetailRunes)
				if wait.on == taskStuckSelf {
					need.Detail = wait.self.Step
				}
				desk.Later = append(desk.Later, need)
				continue
			case taskStuckOther:
				desk.WaitingOthers = append(desk.WaitingOthers, PetDeskWait{
					Kind: PetDeskWaitLine, TaskID: task.ID, TaskCode: task.Code, Title: task.Title,
					On: strings.Join(wait.who, "、"), Lines: petDeskLines(card.Lines, domain.TaskLineOwnerOther), Since: task.UpdatedAt,
				})
				continue
			case taskStuckOwner:
				need.Lines = petDeskLines(card.Lines, domain.TaskLineOwnerOwner)
			}
		}
		turnError := truncateAssistantText(turn.Error, petDeskDetailRunes)
		switch {
		case task.Status == domain.TaskBlocked:
			need.Kind, need.Detail = PetDeskNeedBlocked, turnError
		case task.Status == domain.TaskFailed:
			need.Kind, need.Detail = PetDeskNeedFailed, turnError
		case need.StuckOn == taskStuckOwner || len(need.Questions) > 0:
			need.Kind = PetDeskNeedQuestion
			if !card.StatusUpdatedAt.IsZero() {
				need.Since = card.StatusUpdatedAt
			}
		case turn.Status != "" && domain.TurnStatus(turn.Status) != domain.TurnSucceeded:
			need.Kind, need.Detail = PetDeskNeedStopped, turnError
		default:
			need.Kind = PetDeskLaterNextStep
			need.Detail = truncateAssistantText(petDeskFirst(card.NextSteps), petDeskDetailRunes)
			desk.Later = append(desk.Later, need)
			continue
		}
		if need.Detail == "" {
			need.Detail = truncateAssistantText(card.Status, petDeskDetailRunes)
		}
		desk.NeedsOwner = append(desk.NeedsOwner, need)
	}

	for _, channel := range input.channels {
		if channel.Pending <= 0 {
			continue
		}
		desk.WaitingOthers = append(desk.WaitingOthers, PetDeskWait{
			Kind: PetDeskWaitChannel, Title: channel.Name, On: channel.ID, Count: channel.Pending,
			Since: input.now.Add(-time.Duration(channel.OldestPendingMinutes) * time.Minute),
		})
	}

	pendingIDs := make([]string, 0, len(pendingApprovals))
	for _, approval := range pendingApprovals {
		pendingIDs = append(pendingIDs, approval.ID)
	}
	root := strings.TrimSpace(s.petWorkspaceRoot(ctx))
	desk.WaitingOthers = append(desk.WaitingOthers, petDeskLedgerWaits(
		petReadCoreFile(root, petCoreLoopsPath), petReadCoreFile(root, petCoreAgendaPath), pendingIDs,
	)...)

	// The longest-waiting item is the one most likely forgotten, so it leads.
	// Finished rounds are the opposite: the most recent is the freshest context.
	sort.SliceStable(desk.NeedsOwner, func(i, j int) bool { return desk.NeedsOwner[i].Since.Before(desk.NeedsOwner[j].Since) })
	// Loop waits carry no time. They follow the timed waits in file order, so a
	// long ledger cannot push a running review or a queued channel past the
	// list limit.
	sort.SliceStable(desk.WaitingOthers, func(i, j int) bool {
		left, right := desk.WaitingOthers[i].Since, desk.WaitingOthers[j].Since
		if left.IsZero() != right.IsZero() {
			return right.IsZero()
		}
		return left.Before(right)
	})
	sort.SliceStable(desk.Stalled, func(i, j int) bool { return desk.Stalled[i].Since.Before(desk.Stalled[j].Since) })
	desk.StalledTotal = len(desk.Stalled)
	desk.Stalled = desk.Stalled[:min(len(desk.Stalled), petDeskStalledLimit)]
	sort.SliceStable(desk.Later, func(i, j int) bool { return desk.Later[i].Since.After(desk.Later[j].Since) })
	sort.SliceStable(desk.Running, func(i, j int) bool { return desk.Running[i].Since.Before(desk.Running[j].Since) })
	desk.NeedsOwnerTotal, desk.LaterTotal = len(desk.NeedsOwner), len(desk.Later)
	desk.WaitingOthersTotal, desk.RunningTotal = len(desk.WaitingOthers), len(desk.Running)
	if len(desk.NeedsOwner) > petDeskNeedLimit {
		desk.NeedsOwner = desk.NeedsOwner[:petDeskNeedLimit]
	}
	if len(desk.Later) > petDeskLaterLimit {
		desk.Later = desk.Later[:petDeskLaterLimit]
	}
	if len(desk.WaitingOthers) > petDeskWaitingLimit {
		desk.WaitingOthers = desk.WaitingOthers[:petDeskWaitingLimit]
	}
	if len(desk.Running) > petDeskRunningLimit {
		desk.Running = desk.Running[:petDeskRunningLimit]
	}
	for index := range desk.Running {
		desk.Running[index].Hardware = s.petDeskHardware(ctx, desk.Running[index].TaskID)
	}

	if corrections := s.petOpenCorrections(ctx); len(corrections) > 0 {
		desk.Corrections = corrections
	}
	digest := s.petCoreDigest(ctx, false)
	if len(digest.OpenLoops) > 0 {
		desk.OpenLoops = digest.OpenLoops
	}
	if len(digest.Agenda) > 0 {
		desk.Agenda = digest.Agenda
	}
	desk.OpenLoopsTotal, desk.AgendaTotal = digest.OpenLoopsTotal, digest.AgendaTotal
	// A failed read leaves the line off; the rest of the desk still renders.
	if reply, replyErr := s.petDeskLastReply(ctx); replyErr == nil {
		desk.LastReply = reply
	}
	return desk, nil
}

// petDeskActiveReview returns the code of the newest linked review that is
// still running, or "" when the Task is not under review.
func petDeskActiveReview(links []store.PetCodeReviewLink) string {
	for index := len(links) - 1; index >= 0; index-- {
		link := links[index]
		if !petCodeReviewInFlight(link.Status) {
			continue
		}
		if code := strings.TrimSpace(link.Code); code != "" {
			return code
		}
		return link.ReviewTaskID
	}
	return ""
}

func petDeskLines(lines []domain.TaskLine, owner string) []PetDeskLine {
	var out []PetDeskLine
	for _, line := range lines {
		if line.State != domain.TaskLineClosed && line.Owner == owner {
			out = append(out, PetDeskLine{ID: line.ID, Title: line.Title, State: line.State, Step: line.Step, Blocker: line.Blocker, Who: line.Who})
		}
	}
	return out
}

func petDeskQuestions(items []string) []string {
	questions := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(item); text != "" {
			questions = append(questions, truncateAssistantText(text, petDeskDetailRunes))
		}
	}
	if len(questions) == 0 {
		return nil
	}
	return questions
}

func petDeskFirst(items []string) string {
	for _, item := range items {
		if text := strings.TrimSpace(item); text != "" {
			return text
		}
	}
	return ""
}

// petDeskHardware names the hardware resources bound to a running Task. A
// failed lookup leaves the row without hardware instead of failing the desk.
func (s *Service) petDeskHardware(ctx context.Context, taskID string) []string {
	bindings, err := s.store.TaskHardwareBindings(ctx, taskID)
	if err != nil || len(bindings) == 0 {
		return nil
	}
	names := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		name := binding.ResourceID
		if resource, resourceErr := s.store.HardwareResource(ctx, binding.ResourceID); resourceErr == nil {
			if description := strings.TrimSpace(resource.Description); description != "" {
				name = description
			}
		}
		names = append(names, name)
	}
	return names
}

// PetTaskStateCard is the Task's own "where it stands" note as the pet reads
// it: the short status, what comes next and what waits on the Owner.
type PetTaskStateCard struct {
	Status        string            `json:"status,omitempty"`
	NextSteps     []string          `json:"next_steps,omitempty"`
	OpenQuestions []string          `json:"open_questions,omitempty"`
	Lines         []domain.TaskLine `json:"lines,omitempty"`
	UpdatedAt     string            `json:"updated_at,omitempty"`
}

func petTaskStateCard(card store.TaskStateCard) *PetTaskStateCard {
	if strings.TrimSpace(card.Status) == "" && len(card.NextSteps) == 0 && len(card.OpenQuestions) == 0 && len(card.Lines) == 0 {
		return nil
	}
	view := &PetTaskStateCard{
		Status: truncateAssistantText(card.Status, 600), NextSteps: card.NextSteps, OpenQuestions: card.OpenQuestions, Lines: card.Lines,
	}
	if !card.StatusUpdatedAt.IsZero() {
		view.UpdatedAt = card.StatusUpdatedAt.Format(time.RFC3339)
	}
	return view
}
