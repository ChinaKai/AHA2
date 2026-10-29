package app

import "testing"

func TestParsePetBrainValidatesActionsAndSay(t *testing.T) {
	result := parsePetBrain("```json\n{\"say\":\"我去看看\",\"actions\":[{\"type\":\"play\",\"name\":\"jump\"},{\"type\":\"play\",\"name\":\"turn\"},{\"type\":\"move_to\",\"zone\":\"left\"},{\"type\":\"open_task\",\"task\":\"task-032\"}]}\n```")
	if result.Say != "我去看看" {
		t.Fatalf("say=%q", result.Say)
	}
	if len(result.Actions) != 2 || result.Actions[0].Type != "play" || result.Actions[1].Type != "move_to" {
		t.Fatalf("actions=%#v", result.Actions)
	}
}

func TestParsePetBrainRejectsMalformedAndUnsafeActions(t *testing.T) {
	if result := parsePetBrain("not json"); result.Say != "" || len(result.Actions) != 0 {
		t.Fatalf("malformed=%#v", result)
	}
	result := parsePetBrain(`{"say":"x","actions":[{"type":"play","name":"explode"},{"type":"open_task","task":"../../etc"}]}`)
	if len(result.Actions) != 0 {
		t.Fatalf("unsafe=%#v", result.Actions)
	}
}
