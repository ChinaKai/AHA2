package execution

import "testing"

func TestParseCheckpoint(t *testing.T) {
	t.Parallel()
	reply, patch, candidates, actions, mainFollowup := parseCheckpoint(`完成。
<aha2_checkpoint>
{"facts":["verified"],"progress":["implemented"],"knowledge_candidates":[{"scope":"project","type":"practice","title":"Rule","body":"Use transactions.","confidence":0.9}]}
</aha2_checkpoint>`)
	if reply != "完成。" || len(patch.Facts) != 1 || len(candidates) != 1 || len(actions) != 0 {
		t.Fatalf("unexpected checkpoint parse: %q %#v %#v %#v", reply, patch, candidates, actions)
	}
	if mainFollowup != "" {
		t.Fatalf("unexpected main followup: %q", mainFollowup)
	}
}

func TestParseCheckpointAgentActions(t *testing.T) {
	t.Parallel()
	_, _, _, actions, mainFollowup := parseCheckpoint(`plan
<aha2_checkpoint>
{"main_followup":"Continue main-owned work.","agent_actions":[{"agent_id":"sub-001","title":"Store","assignment":"Implement the store layer.","required":true}]}
</aha2_checkpoint>`)
	if len(actions) != 1 || actions[0].AgentID != "sub-001" || actions[0].Assignment != "Implement the store layer." || !actions[0].Required {
		t.Fatalf("unexpected agent actions: %#v", actions)
	}
	if mainFollowup != "Continue main-owned work." {
		t.Fatalf("unexpected main followup: %q", mainFollowup)
	}
}

func TestParseCheckpointKeepsAgentActionsWhenMemoryUsesObjects(t *testing.T) {
	t.Parallel()
	reply, patch, _, actions, mainFollowup := parseCheckpoint(`已提交。
<aha2_checkpoint>
{"decisions":[{"decision":"use AHA orchestration"}],"progress":[{"item":"planned"}],"main_followup":"continue main work","agent_actions":[{"agent_id":"sub-001","assignment":"reply ok","required":true}]}
</aha2_checkpoint>`)
	if reply != "已提交。" {
		t.Fatalf("checkpoint leaked into reply: %q", reply)
	}
	if len(patch.Decisions) != 1 || patch.Decisions[0] != "use AHA orchestration" {
		t.Fatalf("object memory was not normalized: %#v", patch.Decisions)
	}
	if len(actions) != 1 || actions[0].AgentID != "sub-001" {
		t.Fatalf("agent actions were lost: %#v", actions)
	}
	if mainFollowup != "continue main work" {
		t.Fatalf("main followup was lost: %q", mainFollowup)
	}
}
