package app

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

// PetTaskWatchView is the Owner-facing row for the Pet Workbench follows tab.
// Unwatched rows are ordinary non-terminal Tasks the Owner can start following.
type PetTaskWatchView struct {
	TaskID    string            `json:"task_id"`
	Code      string            `json:"code"`
	Title     string            `json:"title"`
	Status    domain.TaskStatus `json:"status"`
	Watched   bool              `json:"watched"`
	Reason    string            `json:"reason"`
	Ignored   bool              `json:"ignored"`
	IgnoredAt time.Time         `json:"ignored_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func petStatusNotifies(status domain.TaskStatus) bool {
	switch status {
	case domain.TaskCompleted, domain.TaskFailed, domain.TaskBlocked, domain.TaskWaitingUser, domain.TaskCancelled:
		return true
	default:
		return false
	}
}

// StartPetTaskNotifications wires the store status listener. It is called by
// the server process, not by every test fixture, so tests opt in explicitly.
func (s *Service) StartPetTaskNotifications() {
	if s == nil || s.store == nil {
		return
	}
	s.store.SetTaskStatusListener(s.handlePetTaskStatusChange)
}

func (s *Service) StopPetTaskNotifications() {
	if s == nil || s.store == nil {
		return
	}
	s.store.SetTaskStatusListener(nil)
}

func (s *Service) handlePetTaskStatusChange(ctx context.Context, change store.TaskStatusChange) {
	if s == nil || change.TaskID == "" || change.TaskID == petTaskID || !petStatusNotifies(change.To) {
		return
	}
	watch, err := s.store.PetTaskWatch(ctx, change.TaskID)
	if err != nil || watch.Ignored {
		return
	}
	task, err := s.store.Task(ctx, change.TaskID)
	if err != nil {
		return
	}
	turnID := ""
	if turn, turnErr := s.store.LatestTurnForAgent(ctx, task.ID, "main"); turnErr == nil {
		turnID = turn.ID
	}
	if watch.LastNotifiedStatus == string(change.To) && watch.LastNotifiedTurnID == turnID {
		return
	}
	if err := s.store.MarkPetTaskWatchNotified(ctx, task.ID, string(change.To), turnID, s.now().UTC()); err != nil {
		return
	}
	message := s.petTaskStatusMessage(ctx, task, change.To)
	assistant, err := s.petAssistant(ctx)
	if err != nil || !assistant.PetEnabled {
		return
	}
	now := s.now().UTC()
	if petQuietNow(assistant, now) && (change.To == domain.TaskCompleted || change.To == domain.TaskCancelled) {
		delay := petQuietRemaining(assistant, now)
		if delay > 0 {
			time.AfterFunc(delay, func() { s.deliverPetTaskStatusMessage(task.ID, message) })
			return
		}
	}
	go s.deliverPetTaskStatusMessage(task.ID, message)
}

func (s *Service) deliverPetTaskStatusMessage(taskID, message string) {
	if s == nil || strings.TrimSpace(message) == "" {
		return
	}
	// The watch may have been ignored or removed while a quiet-hours callback
	// was waiting. Re-check before starting a new pet turn.
	if watch, err := s.store.PetTaskWatch(context.Background(), taskID); err != nil || watch.Ignored {
		return
	}
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return
	}
	if _, err := s.SubmitMessage(context.Background(), petTaskID, message); err != nil {
		log.Printf("notify pet task about watched task status: %v", err)
	}
}

func (s *Service) petTaskStatusMessage(ctx context.Context, task domain.Task, status domain.TaskStatus) string {
	english := s.petUsesEnglish(ctx)
	var b strings.Builder
	if english {
		fmt.Fprintf(&b, "[Task update] The followed Task %s「%s」changed status to %s.", task.Code, task.Title, status)
	} else {
		fmt.Fprintf(&b, "[任务动态] 你关注的 Task %s「%s」状态变为 %s。", task.Code, task.Title, status)
	}
	if turn, err := s.store.LatestTurnForAgent(ctx, task.ID, "main"); err == nil {
		if english {
			fmt.Fprintf(&b, "\nLatest Turn: %s", turn.Status)
			if errText := strings.TrimSpace(turn.Error); errText != "" {
				fmt.Fprintf(&b, "\nError: %s", truncateAssistantText(errText, 1200))
			}
			if result := strings.TrimSpace(turn.Result); result != "" {
				fmt.Fprintf(&b, "\nResult: %s", truncateAssistantText(result, 1200))
			}
		} else {
			fmt.Fprintf(&b, "\n最后 Turn：%s", turn.Status)
			if errText := strings.TrimSpace(turn.Error); errText != "" {
				fmt.Fprintf(&b, "\n错误：%s", truncateAssistantText(errText, 1200))
			}
			if result := strings.TrimSpace(turn.Result); result != "" {
				fmt.Fprintf(&b, "\n结果：%s", truncateAssistantText(result, 1200))
			}
		}
	}
	if english {
		b.WriteString("\nUse aha_task_detail/aha_task_turns for details.")
	} else {
		b.WriteString("\n可用 aha_task_detail / aha_task_turns 查看细节。")
	}
	b.WriteString(petWakeActionContract(english))
	return b.String()
}

func petQuietRemaining(assistant domain.OwnerAssistant, now time.Time) time.Duration {
	if !assistant.PetQuietEnabled || assistant.PetQuietStart == assistant.PetQuietEnd {
		return 0
	}
	start, end := assistant.PetQuietStart, assistant.PetQuietEnd
	hour := now.Hour()
	inQuiet := false
	if start < end {
		inQuiet = hour >= start && hour < end
	} else {
		inQuiet = hour >= start || hour < end
	}
	if !inQuiet {
		return 0
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), end, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(now)
}

func (s *Service) PetWatchTask(ctx context.Context, taskID string) (domain.PetTaskWatch, error) {
	task, err := s.petViewableTask(ctx, taskID)
	if err != nil {
		return domain.PetTaskWatch{}, err
	}
	if task.ID == petTaskID {
		return domain.PetTaskWatch{}, fmt.Errorf("不能关注宠物自己的 Task")
	}
	if watch, watchErr := s.store.PetTaskWatch(ctx, task.ID); watchErr == nil && watch.Ignored {
		return domain.PetTaskWatch{}, fmt.Errorf("该任务已被主人忽略，不能重新关注")
	}
	now := s.now().UTC()
	if err := s.store.UpsertPetTaskWatch(ctx, task.ID, "manual", now); err != nil {
		return domain.PetTaskWatch{}, err
	}
	return s.store.PetTaskWatch(ctx, task.ID)
}

func (s *Service) PetUnwatchTask(ctx context.Context, taskID string) error {
	task, err := s.petViewableTask(ctx, taskID)
	if err != nil {
		return err
	}
	if watch, watchErr := s.store.PetTaskWatch(ctx, task.ID); watchErr == nil && watch.Ignored {
		return fmt.Errorf("该任务已被主人忽略，请由主人在宠物工作台取消关注")
	}
	return s.store.DeletePetTaskWatch(ctx, task.ID)
}

// PetTaskWatchList returns the Owner workbench follows tab: every non-terminal
// ordinary Task, plus any watched Task (including terminal or ignored ones) so
// the Owner can manage existing follows.
func (s *Service) PetTaskWatchList(ctx context.Context) ([]PetTaskWatchView, error) {
	watches, err := s.store.PetTaskWatches(ctx)
	if err != nil {
		return nil, err
	}
	tasks, err := s.store.ListTasks(ctx, "")
	if err != nil {
		return nil, err
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	ordinaryProjects := make(map[string]bool, len(projects))
	for _, project := range projects {
		if !domain.IsSystemManagedProject(project) {
			ordinaryProjects[project.ID] = true
		}
	}
	watchByTask := make(map[string]domain.PetTaskWatch, len(watches))
	for _, watch := range watches {
		watchByTask[watch.TaskID] = watch
	}
	views := make([]PetTaskWatchView, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == petTaskID {
			continue
		}
		watch, watched := watchByTask[task.ID]
		if !watched && (task.Status.Terminal() || !ordinaryProjects[task.ProjectID]) {
			continue
		}
		updatedAt := task.UpdatedAt
		if watched && watch.UpdatedAt.After(updatedAt) {
			updatedAt = watch.UpdatedAt
		}
		views = append(views, PetTaskWatchView{
			TaskID: task.ID, Code: task.Code, Title: task.Title, Status: task.Status,
			Watched: watched, Reason: watch.Reason, Ignored: watch.Ignored,
			IgnoredAt: watch.IgnoredAt, CreatedAt: task.CreatedAt, UpdatedAt: updatedAt,
		})
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Ignored != views[j].Ignored {
			return !views[i].Ignored
		}
		return views[i].UpdatedAt.After(views[j].UpdatedAt)
	})
	return views, nil
}

// FollowPetTask is the Owner-facing follow action for an ordinary non-terminal
// Task that the pet is not already watching.
func (s *Service) FollowPetTask(ctx context.Context, taskID string) (domain.PetTaskWatch, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return domain.PetTaskWatch{}, fmt.Errorf("task_id 不能为空")
	}
	task, err := s.store.Task(ctx, taskID)
	if err != nil {
		return domain.PetTaskWatch{}, fmt.Errorf("Task 不存在")
	}
	if task.ID == petTaskID {
		return domain.PetTaskWatch{}, fmt.Errorf("不能关注宠物自己的 Task")
	}
	project, err := s.store.Project(ctx, task.ProjectID)
	if err != nil {
		return domain.PetTaskWatch{}, fmt.Errorf("Task 所属项目不存在")
	}
	if domain.IsSystemManagedProject(project) {
		return domain.PetTaskWatch{}, fmt.Errorf("系统托管任务不能关注")
	}
	if task.Status.Terminal() {
		return domain.PetTaskWatch{}, fmt.Errorf("只能关注未结束的任务")
	}
	if watch, watchErr := s.store.PetTaskWatch(ctx, task.ID); watchErr == nil {
		if watch.Ignored {
			return domain.PetTaskWatch{}, fmt.Errorf("该任务已被忽略，请恢复关注")
		}
		return watch, nil
	}
	if err := s.store.UpsertPetTaskWatch(ctx, task.ID, "manual", s.now().UTC()); err != nil {
		return domain.PetTaskWatch{}, err
	}
	return s.store.PetTaskWatch(ctx, task.ID)
}

// RemovePetTaskWatch is the Owner-facing unfollow action. Unlike the pet tool,
// it may remove an ignored watch as well.
func (s *Service) RemovePetTaskWatch(ctx context.Context, taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return fmt.Errorf("task_id 不能为空")
	}
	if _, err := s.store.PetTaskWatch(ctx, taskID); err != nil {
		return fmt.Errorf("该任务未被关注")
	}
	return s.store.DeletePetTaskWatch(ctx, taskID)
}

// SetPetTaskWatchIgnored mutes or restores one watch from the Owner workbench.
func (s *Service) SetPetTaskWatchIgnored(ctx context.Context, taskID string, ignored bool) (domain.PetTaskWatch, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return domain.PetTaskWatch{}, fmt.Errorf("task_id 不能为空")
	}
	if _, err := s.store.PetTaskWatch(ctx, taskID); err != nil {
		return domain.PetTaskWatch{}, fmt.Errorf("该任务未被关注")
	}
	if err := s.store.SetPetTaskWatchIgnored(ctx, taskID, ignored, s.now().UTC()); err != nil {
		return domain.PetTaskWatch{}, err
	}
	return s.store.PetTaskWatch(ctx, taskID)
}

func (s *Service) watchPetTask(ctx context.Context, taskID, reason string) {
	if s == nil || s.store == nil || strings.TrimSpace(taskID) == "" || taskID == petTaskID {
		return
	}
	if watch, err := s.store.PetTaskWatch(ctx, taskID); err == nil && watch.Ignored {
		return
	}
	_ = s.store.UpsertPetTaskWatch(ctx, taskID, reason, s.now().UTC())
}
