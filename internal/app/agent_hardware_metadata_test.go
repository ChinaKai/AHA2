package app

import (
	"github.com/ChinaKai/AHA2/internal/domain"
	"testing"
)

func TestHardwarePolicyReadOnlyTaskMetadataKeepsReadsAndDeniesWrites(t *testing.T) {
	ctx, _, service, task, _ := newStreamCompatFixture(t)
	seedHardwareMCPGroup(t, ctx, service.store, task)
	call := AgentCallContext{Task: task, Project: domain.Project{ID: task.ProjectID}, Turn: domain.Turn{AgentID: "main", RuntimeConfigSnapshotID: task.RuntimeConfigSnapshotID}}
	if !service.AgentHardwareMCPWriteAllowed(ctx, call) {
		t.Fatal("fixture writable Main was denied")
	}
	call.Task.ReadOnly = true
	if !service.AgentHardwareMCPAllowed(ctx, call) || service.AgentHardwareMCPWriteAllowed(ctx, call) {
		t.Fatal("read-only Task metadata did not preserve read/write separation")
	}
	_, allowed := service.mcpRuntimeForTurn(ctx, call.Task, " main ", "http://127.0.0.1:8766", "")
	if len(mcpDomainTools(allowed, hardwareMCPName)) != 2 {
		t.Fatal("read-only Task metadata received device I/O tools")
	}
}
