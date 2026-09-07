package tray

import (
	"reflect"
	"testing"
)

func TestMachineRetriesAreFiniteAndManualStartResetsThem(t *testing.T) {
	machine := NewMachine(2)
	assertTransition(t, &machine, EventUserStart, StateStarting, []Effect{EffectStartProcess})
	assertTransition(t, &machine, EventStartSucceeded, StateRunning, nil)
	assertTransition(t, &machine, EventProcessExited, StateBackoff, []Effect{EffectScheduleRetry})
	assertTransition(t, &machine, EventRetryDue, StateStarting, []Effect{EffectStartProcess})
	assertTransition(t, &machine, EventStartFailed, StateBackoff, []Effect{EffectScheduleRetry})
	assertTransition(t, &machine, EventRetryDue, StateStarting, []Effect{EffectStartProcess})
	assertTransition(t, &machine, EventStartFailed, StateFailed, nil)
	if machine.RetryCount != 2 {
		t.Fatalf("retry count=%d", machine.RetryCount)
	}
	assertTransition(t, &machine, EventUserStart, StateStarting, []Effect{EffectStartProcess})
	if machine.RetryCount != 0 {
		t.Fatalf("manual start did not reset retries: %d", machine.RetryCount)
	}
}

func TestMachineStopRestartAndExit(t *testing.T) {
	machine := NewMachine(3)
	assertTransition(t, &machine, EventUserStart, StateStarting, []Effect{EffectStartProcess})
	assertTransition(t, &machine, EventStartSucceeded, StateRunning, nil)
	assertTransition(t, &machine, EventUserStop, StateStopping, []Effect{EffectStopProcess})
	assertTransition(t, &machine, EventProcessExited, StateStopped, nil)

	assertTransition(t, &machine, EventUserStart, StateStarting, []Effect{EffectStartProcess})
	assertTransition(t, &machine, EventStartSucceeded, StateRunning, nil)
	assertTransition(t, &machine, EventUserRestart, StateStopping, []Effect{EffectStopProcess})
	assertTransition(t, &machine, EventProcessExited, StateStarting, []Effect{EffectStartProcess})
	assertTransition(t, &machine, EventStartSucceeded, StateRunning, nil)
	assertTransition(t, &machine, EventUserExit, StateStopping, []Effect{EffectStopProcess})
	assertTransition(t, &machine, EventProcessExited, StateExited, []Effect{EffectExit})
}

func TestMachineCanStopOrExitWhileStarting(t *testing.T) {
	machine := NewMachine(1)
	assertTransition(t, &machine, EventUserStart, StateStarting, []Effect{EffectStartProcess})
	assertTransition(t, &machine, EventUserStop, StateStopping, nil)
	assertTransition(t, &machine, EventStartSucceeded, StateStopping, []Effect{EffectStopProcess})
	assertTransition(t, &machine, EventProcessExited, StateStopped, nil)
	assertTransition(t, &machine, EventUserExit, StateExited, []Effect{EffectExit})
}

func assertTransition(t *testing.T, machine *Machine, event Event, wantState State, wantEffects []Effect) {
	t.Helper()
	gotEffects := machine.Apply(event)
	if machine.State != wantState || !reflect.DeepEqual(gotEffects, wantEffects) {
		t.Fatalf("event=%d state=%s effects=%v; want state=%s effects=%v", event, machine.State, gotEffects, wantState, wantEffects)
	}
}
