package execution

import "testing"

func TestParseCheckpoint(t *testing.T) {
	t.Parallel()
	reply, patch, candidates := parseCheckpoint(`完成。
<aha2_checkpoint>
{"facts":["verified"],"progress":["implemented"],"knowledge_candidates":[{"scope":"project","type":"practice","title":"Rule","body":"Use transactions.","confidence":0.9}]}
</aha2_checkpoint>`)
	if reply != "完成。" || len(patch.Facts) != 1 || len(candidates) != 1 {
		t.Fatalf("unexpected checkpoint parse: %q %#v %#v", reply, patch, candidates)
	}
}
