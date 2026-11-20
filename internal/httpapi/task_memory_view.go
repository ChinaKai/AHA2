package httpapi

import "github.com/ChinaKai/AHA2/internal/domain"

// compactTaskMemory returns the Task state card as browsers and Agents see it.
// Extra holds internal bookkeeping (knowledge references) and is left out.
func compactTaskMemory(memory domain.TaskMemory) domain.TaskMemory {
	memory.Extra = nil
	return memory
}
