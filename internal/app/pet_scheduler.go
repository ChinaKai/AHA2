package app

import (
	"context"
	"log"
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

func (s *Service) runPetAutonomyOnce(ctx context.Context) {
	assistant, err := s.store.EnsureOwnerAssistant(ctx, s.now().UTC())
	if err != nil || !assistant.PetEnabled {
		return
	}
	idle := assistant.PetIdleMinutes
	if idle < 1 {
		idle = 5
	}
	s.petBrainMu.Lock()
	if s.petBrainAt == nil {
		s.petBrainAt = map[string]time.Time{}
	}
	last := s.petBrainAt["server_autonomy_schedule"]
	if last.IsZero() {
		now := s.now().UTC()
		s.petBrainAt["server_autonomy_schedule"] = now
		s.petAutonomyNext = now.Add(time.Duration(idle) * time.Minute)
		s.petBrainMu.Unlock()
		return
	}
	if s.now().UTC().Sub(last) < time.Duration(idle)*time.Minute {
		s.petBrainMu.Unlock()
		return
	}
	now := s.now().UTC()
	s.petBrainAt["server_autonomy_schedule"] = now
	s.petAutonomyNext = now.Add(time.Duration(idle) * time.Minute)
	s.petBrainMu.Unlock()
	_, _ = s.AskOwnerPetBrain(ctx, PetBrainContext{
		DeviceID: "server_autonomy", IdleSeconds: idle * 60, Page: "后台（服务端调度）",
	})
	s.petBrainMu.Lock()
	s.petAutonomyLast = s.now().UTC()
	s.petBrainMu.Unlock()
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
	idle := assistant.PetIdleMinutes
	if idle < 1 {
		idle = 5
	}
	if next.IsZero() {
		next = s.now().UTC().Add(time.Duration(idle) * time.Minute)
	}
	return last, next, runs
}
