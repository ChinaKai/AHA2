package app

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

// StartPetAutonomy runs the pet's autonomous loop on the server, so closing the
// browser no longer stops it. The pet still emits actions over SSE, which every
// connected web client applies.
func (s *Service) StartPetAutonomy() {
	if s == nil || s.runContext == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.runContext.Done():
				return
			case <-ticker.C:
				func() {
					defer func() {
						if recovered := recover(); recovered != nil {
							log.Printf("pet autonomy panicked: %v", recovered)
						}
					}()
					s.runPetAutonomyOnce(s.runContext)
				}()
			}
		}
	}()
}

// petAutonomyMinimumReview is the floor for the time-based wake-up. Task status
// notifications already cover events; the floor only prevents an accidental
// sub-five-minute loop from spamming repeated inspection turns.
const petAutonomyMinimumReview = 5 * time.Minute

// petAutonomyInterval is the scheduler's polling cadence. It may be shorter
// than 60/pet_hourly_limit when the owner configures a 15-minute idle trigger
// with an hourly limit of 1. That configuration is intentional in production:
// the budget is enforced per submitted run, so polls rejected by the hourly
// limit do not consume it and the first poll after the rolling window slides
// runs again.
func petAutonomyInterval(assistant domain.OwnerAssistant) time.Duration {
	interval := time.Duration(assistant.PetIdleMinutes) * time.Minute
	if interval < petAutonomyMinimumReview {
		return petAutonomyMinimumReview
	}
	return interval
}

func (s *Service) runPetAutonomyOnce(ctx context.Context) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil || !assistant.PetEnabled {
		return
	}
	interval := petAutonomyInterval(assistant)
	s.petBrainMu.Lock()
	if s.petBrainAt == nil {
		s.petBrainAt = map[string]time.Time{}
	}
	last := s.petBrainAt["server_autonomy_schedule"]
	if last.IsZero() {
		now := s.now().UTC()
		s.petBrainAt["server_autonomy_schedule"] = now
		s.petAutonomyNext = now.Add(interval)
		s.petBrainMu.Unlock()
		return
	}
	if s.now().UTC().Sub(last) < interval {
		s.petBrainMu.Unlock()
		return
	}
	now := s.now().UTC()
	s.petBrainAt["server_autonomy_schedule"] = now
	s.petAutonomyNext = now.Add(interval)
	s.petBrainMu.Unlock()
	_, err = s.AskOwnerPetBrain(ctx, PetBrainContext{
		DeviceID: "server_autonomy", IdleSeconds: int(interval.Minutes()) * 60, Page: "后台（服务端调度）",
	})
	if err != nil && !petAutonomySkip(err) {
		s.recordPetAutonomyError(ctx, err)
	}
	s.petBrainMu.Lock()
	s.petAutonomyLast = s.now().UTC()
	s.petBrainMu.Unlock()
}

// IsPetBrainSkip reports expected "nothing to do" outcomes so the HTTP layer
// does not label a cooldown, quiet period or active turn as a rate-limit
// failure.
func IsPetBrainSkip(err error) bool { return petAutonomySkip(err) }

// petAutonomySkip distinguishes the normal reasons a scheduled thought does
// nothing from a real failure. Only the latter is written to pet activity.
func petAutonomySkip(err error) bool {
	if err == nil {
		return true
	}
	message := err.Error()
	for _, expected := range []string{
		"宠物刚刚动过", "宠物安静时段", "宠物这个小时已经够活跃了",
		"宠物 Task 正在执行",
	} {
		if strings.Contains(message, expected) {
			return true
		}
	}
	return false
}

func (s *Service) recordPetAutonomyError(ctx context.Context, err error) {
	detail, _ := json.Marshal(map[string]any{"message": err.Error()})
	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "aha",
		Kind: "autonomy_error", Detail: string(detail), Status: "failed",
	})
}

func (s *Service) petAutonomyStatus(assistant domain.OwnerAssistant) (time.Time, time.Time, int) {
	if !assistant.PetEnabled {
		return time.Time{}, time.Time{}, 0
	}
	s.petBrainMu.Lock()
	last := s.petAutonomyLast
	next := s.petAutonomyNext
	runs := len(s.petBrainRuns["server_autonomy"])
	s.petBrainMu.Unlock()
	if next.IsZero() {
		next = s.now().UTC().Add(petAutonomyInterval(assistant))
	}
	return last, next, runs
}
