package app

import (
	"testing"

	"github.com/ChinaKai/AHA2/internal/gateway"
)

func TestParseTextToolCalls(t *testing.T) {
	text := `好。<tool_call>pet_walk_path{"points":[[0.4,0.4],[0.5,0.5]]}</tool_call_end><tool_call>pet_mood{"mood":"happy"}</tool_call_end>`
	calls, cleaned := parseTextToolCalls(text)
	if len(calls) != 2 || calls[0].Name != "pet_walk_path" || cleaned != "好。" {
		t.Fatalf("calls=%#v cleaned=%q", calls, cleaned)
	}
	var point PetPoint
	if err := point.UnmarshalJSON([]byte(`[0.5,0.25]`)); err != nil || point.X != 0.5 || point.Y != 0.25 {
		t.Fatalf("point=%#v err=%v", point, err)
	}
}

func TestPetActionsFromTextPathArgs(t *testing.T) {
	actions := petActionsFromToolCalls([]gateway.ChatToolCall{{
		Name: "pet_walk_path", Arguments: `{"points":[[0.4,0.4],[0.5,0.5]]}`,
	}}, 2)
	if len(actions) != 1 || actions[0].Type != "walk_path" || len(actions[0].Points) != 2 || actions[0].Points[0].X != 0.4 {
		t.Fatalf("actions=%#v", actions)
	}
}
