package domain

import "testing"

func TestKnowledgeEnabledFollowsTaskPolicy(t *testing.T) {
	for _, testCase := range []struct {
		policy string
		want   bool
	}{
		{"disabled", false},
		{"enabled", true},
		{"inherit", true},
		{"", true},
		{"legacy-unknown", true},
	} {
		if got := KnowledgeEnabled(Task{KnowledgePolicy: testCase.policy}); got != testCase.want {
			t.Fatalf("policy %q: got %v want %v", testCase.policy, got, testCase.want)
		}
	}
}
