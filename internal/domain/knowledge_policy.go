package domain

import "strings"

// KnowledgeEnabled reports whether a Task's knowledge blocks are enabled.
// "disabled" is the only value that turns knowledge off; enabled, inherit,
// empty, and unknown legacy values all run as enabled so a hand-written policy
// can never silently hide knowledge.
func KnowledgeEnabled(task Task) bool {
	switch strings.TrimSpace(task.KnowledgePolicy) {
	case "disabled":
		return false
	default:
		return true
	}
}
