package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
	"github.com/ChinaKai/AHA2/internal/store"
)

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
	if err != nil {
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
			time.AfterFunc(delay, func() { s.deliverPetTaskStatusMessage(message) })
			return
		}
	}
	go s.deliverPetTaskStatusMessage(message)
}

func (s *Service) deliverPetTaskStatusMessage(message string) {
	if s == nil || strings.TrimSpace(message) == "" {
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
		b.WriteString("\nUse aha_pet_work_state / aha_task_detail / aha_task_turns to inspect it and decide whether to continue.")
	} else {
		b.WriteString("\n请用 aha_pet_work_state / aha_task_detail / aha_task_turns 查看后决定是否继续处理。")
	}
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
	return s.store.DeletePetTaskWatch(ctx, task.ID)
}

func (s *Service) watchPetTask(ctx context.Context, taskID, reason string) {
	if s == nil || s.store == nil || strings.TrimSpace(taskID) == "" || taskID == petTaskID {
		return
	}
	_ = s.store.UpsertPetTaskWatch(ctx, taskID, reason, s.now().UTC())
}
