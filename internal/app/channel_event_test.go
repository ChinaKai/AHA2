package app

import "testing"

func TestChannelSemanticEventOnlyProjectsFinalAgentReply(t *testing.T) {
	t.Parallel()
	if _, _, _, _, ok := channelSemanticEvent("task", "agent_message", map[string]any{"text": "draft", "agent_id": "main"}); ok {
		t.Fatal("streaming agent message would create a duplicate channel reply")
	}
	eventClass, eventType, payload, _, ok := channelSemanticEvent("task", "agent_reply", map[string]any{"text": "final", "agent_id": "main"})
	if !ok || eventClass != "message" || eventType != "agent_reply" || payload["text"] != "final" {
		t.Fatalf("final reply projection=%q/%q %#v ok=%v", eventClass, eventType, payload, ok)
	}
}
