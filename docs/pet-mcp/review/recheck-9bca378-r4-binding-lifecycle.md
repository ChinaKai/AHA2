# 复审 S4 / S5 / N4：绑定身份、连接保留与孤儿清理

统一固定源码：`9bca3785b3ef1d071faed06041aeb2e6de0139a7`，tree `5d2a491a18fe4df4c9c348c0e491aba87a8537ba`。问题 diff 边界为 `9d01cb63cca51bff23c40dad2c61d55497f2cd3a..9bca3785b3ef1d071faed06041aeb2e6de0139a7` 中下列完整文件；门禁 diff 保留 3 行上下文。所有源码摘录也从该固定提交读取，不来自正在编辑的 worktree。

全部校验先完成；无变化时不重写绑定行，也不断开合法连接。有效更新先成功写库，再只保留 ID 和资源身份都未变化的连接；真实 manager 的 DisconnectTaskExcept 同时清理没有数据库行的孤儿连接。Web 回归验证未变化连接仍连接、孤儿已去除、重绑后旧 ID 不可使用、非法更新不写库/不断连。测试孤儿是现有 manager 允许的只读共享 attachment，不代表新公布的 Agent 只读 connect 获准。宠物新资源的默认 ID 如撞到旧别名，会分配不冲突 ID，旧别名保留；空、重复、缺资源、超限分别有固定错误码。

固定提交的 20 个具名定向回归全部执行且退出 0（136.834s，显式核对每个顶层 Test 的 pass，missing=0）；`go vet ./internal/app ./internal/httpapi ./internal/hardware` 退出 0（1.176s）。完整命令和逐项覆盖在 `recheck-9bca378-validation.md`。固定提交的 app/httpapi 全包尚未完成，当前排队；旧 worktree 全包运行期间源码有改动，只作诊断，不是这个提交的通过证据。不能据此合并或声称完整 task-722 已完成。

完整 diff 文件边界：

- `internal/app/agent_pet_domains_test.go`
- `internal/app/pet_hardware_binding_alias_test.go`
- `internal/app/pet_task_hardware.go`
- `internal/app/task_hardware_bindings.go`
- `internal/hardware/manager.go`
- `internal/httpapi/agent_hardware_bindings_test.go`

## 完整调用链依赖源码

### `internal/app/task_hardware_bindings.go` · `ReplaceTaskHardwareResourceBindings`

```go
func (s *Service) ReplaceTaskHardwareResourceBindings(ctx context.Context, taskID string, bindings []domain.TaskHardwareBinding) error {
	if len(bindings) > MaxTaskHardwareResources {
		return &HardwareBindingError{Code: "hardware_binding_limit"}
	}
	if _, err := s.store.Task(ctx, taskID); err != nil {
		return err
	}
	existing, err := s.store.TaskHardwareBindings(ctx, taskID)
	if err != nil {
		return err
	}
	byID := map[string]domain.TaskHardwareBinding{}
	for _, item := range existing {
		byID[item.ID] = item
	}
	ids, resources := map[string]bool{}, map[string]bool{}
	now := s.now().UTC()
	for index := range bindings {
		item := &bindings[index]
		item.TaskID = taskID
		item.ResourceID = strings.TrimSpace(item.ResourceID)
		item.ID = hardwareconfig.NormalizeID(item.ID)
		if item.ID == "" {
			item.ID = hardwareconfig.NormalizeID(item.ResourceID)
		}
		if item.ID == "" || item.ResourceID == "" {
			return &HardwareBindingError{Code: "hardware_binding_empty"}
		}
		if ids[item.ID] || resources[item.ResourceID] {
			return &HardwareBindingError{Code: "hardware_binding_duplicate"}
		}
		if _, err := s.store.HardwareResource(ctx, item.ResourceID); err != nil {
			return &HardwareBindingError{Code: "hardware_resource_not_found"}
		}
		ids[item.ID], resources[item.ResourceID] = true, true
		item.CreatedAt, item.UpdatedAt = now, now
		if previous, ok := byID[item.ID]; ok && previous.ResourceID == item.ResourceID {
			item.CreatedAt = previous.CreatedAt
		}
	}
	groups, err := s.store.HardwareGroups(ctx, taskID)
	if err != nil {
		return err
	}
	keepIDs := []string{}
	keep := map[string]bool{}
	unchanged := len(existing) == len(bindings)
	for index, item := range bindings {
		if previous, ok := byID[item.ID]; ok && previous.ResourceID == item.ResourceID {
			keepIDs = append(keepIDs, item.ID)
			keep[item.ID] = true
		}
		if index >= len(existing) || existing[index].ID != item.ID || existing[index].ResourceID != item.ResourceID {
			unchanged = false
		}
	}
	if !unchanged {
		if err := s.store.ReplaceTaskHardwareBindings(ctx, taskID, bindings); err != nil {
			return err
		}
	}
	if runtime, ok := s.petHardware.(interface{ DisconnectTaskExcept(string, []string) }); ok {
		runtime.DisconnectTaskExcept(taskID, keepIDs)
	} else if s.petHardware != nil {
		for _, group := range groups {
			if keep[group.ID] {
				continue
			}
			for _, transport := range []string{domain.HardwareTransportSerial, domain.HardwareTransportNetwork} {
				if group.Supports(transport) {
					s.petHardware.Disconnect(taskID, group.ID, transport)
				}
			}
		}
	}
	return nil
}
```

### `internal/app/pet_task_hardware.go` · `PetUpdateTaskHardware`

```go
func (s *Service) PetUpdateTaskHardware(ctx context.Context, input PetUpdateTaskHardwareInput) (PetTaskHardwareView, error) {
	task, _, err := s.petManageableTask(ctx, input.TaskID)
	if err != nil {
		return PetTaskHardwareView{}, err
	}
	existing, err := s.store.TaskHardwareBindings(ctx, task.ID)
	if err != nil {
		return PetTaskHardwareView{}, err
	}
	byResource := map[string]string{}
	usedIDs := map[string]bool{}
	for _, item := range existing {
		byResource[item.ResourceID] = item.ID
		usedIDs[item.ID] = true
	}
	bindings := make([]domain.TaskHardwareBinding, 0, len(input.ResourceIDs))
	for _, raw := range input.ResourceIDs {
		resourceID := strings.TrimSpace(raw)
		id := byResource[resourceID]
		if id == "" {
			id = resourceID
			for usedIDs[id] {
				id = domain.NewID("hardware_binding")
			}
			usedIDs[id] = true
		}
		bindings = append(bindings, domain.TaskHardwareBinding{TaskID: task.ID, ID: id, ResourceID: resourceID})
	}
	if err := s.ReplaceTaskHardwareResourceBindings(ctx, task.ID, bindings); err != nil {
		return PetTaskHardwareView{}, err
	}

	_ = s.store.AppendPetEvent(ctx, domain.PetEvent{
		ID: domain.NewID("pet_event"), At: s.now().UTC(), Actor: "pet", Kind: "task_hardware_updated",
		Detail: task.ID, Status: "ok",
	})
	return s.PetTaskHardware(ctx, task.ID)
}
```

### `internal/httpapi/hardware_handlers.go` · `updateTaskHardwareBindings`

```go
func (s *Server) updateTaskHardwareBindings(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if s.rejectRetiredChannelTaskWrite(writer, request, taskID) {
		return
	}
	if s.rejectCompletedTaskWrite(writer, request, taskID) {
		return
	}
	if _, err := s.store.Task(request.Context(), taskID); err != nil {
		writeError(writer, http.StatusNotFound, "task_not_found")
		return
	}
	var payload struct {
		Bindings []struct {
			ID         string `json:"id"`
			ResourceID string `json:"resource_id"`
			// Retained as an ignored compatibility field for pages loaded before
			// access became authoritative on the system hardware resource.
			Access string `json:"access"`
		} `json:"bindings"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if len(payload.Bindings) > app.MaxTaskHardwareResources {
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"ok": false, "error": "hardware_bindings_invalid", "message": "每个任务最多启用 16 个硬件资源",
		})
		return
	}
	existing, _ := s.store.TaskHardwareBindings(request.Context(), taskID)
	existingByID := map[string]domain.TaskHardwareBinding{}
	for _, binding := range existing {
		existingByID[binding.ID] = binding
	}
	now := time.Now().UTC()
	bindings := make([]domain.TaskHardwareBinding, 0, len(payload.Bindings))
	seenIDs, seenResources := map[string]bool{}, map[string]bool{}
	for _, raw := range payload.Bindings {
		resourceID := strings.TrimSpace(raw.ResourceID)
		if resourceID == "" || seenResources[resourceID] {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "hardware_binding_duplicate"})
			return
		}
		_, err := s.store.HardwareResource(request.Context(), resourceID)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "hardware_resource_not_found"})
			return
		}
		id := normalizeHardwareID(raw.ID)
		if id == "" {
			id = resourceID
		}
		if seenIDs[id] {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "hardware_binding_duplicate"})
			return
		}
		createdAt := now
		if previous, ok := existingByID[id]; ok && previous.ResourceID == resourceID {
			createdAt = previous.CreatedAt
		}
		seenIDs[id], seenResources[resourceID] = true, true
		bindings = append(bindings, domain.TaskHardwareBinding{
			TaskID: taskID, ID: id, ResourceID: resourceID,
			CreatedAt: createdAt, UpdatedAt: now,
		})
	}
	if err := s.app.ReplaceTaskHardwareResourceBindings(request.Context(), taskID, bindings); err != nil {
		var invalid *app.HardwareBindingError
		if errors.As(err, &invalid) {
			writeError(writer, http.StatusBadRequest, invalid.Code)
		} else {
			writeError(writer, http.StatusBadRequest, "update_hardware_bindings_failed")
		}
		return
	}

	groups, _ := s.store.HardwareGroups(request.Context(), taskID)
	resources, _ := s.store.HardwareResources(request.Context())
	s.audit(request, "task.hardware.bindings.update", "task", taskID, map[string]any{
		"bindings": len(bindings), "resource_ids": func() []string {
			ids := make([]string, 0, len(bindings))
			for _, binding := range bindings {
				ids = append(ids, binding.ResourceID)
			}
			return ids
		}(),
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "groups": groups, "resources": resources})
}
```

### `internal/store/hardware.go` · `TaskHardwareBindings`

```go
func (s *Store) TaskHardwareBindings(ctx context.Context, taskID string) ([]domain.TaskHardwareBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id,id,resource_id,position,created_at,updated_at
		FROM task_hardware_bindings WHERE task_id=? ORDER BY position,id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.TaskHardwareBinding, 0)
	for rows.Next() {
		var item domain.TaskHardwareBinding
		var createdAt, updatedAt string
		if err := rows.Scan(&item.TaskID, &item.ID, &item.ResourceID, &item.Position, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(createdAt)
		item.UpdatedAt = parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}
```

### `internal/store/hardware.go` · `writeTaskHardwareBindings`

```go
func writeTaskHardwareBindings(ctx context.Context, tx *sql.Tx, taskID string, bindings []domain.TaskHardwareBinding) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_hardware_bindings WHERE task_id=?`, taskID); err != nil {
		return err
	}
	now := time.Now().UTC()
	for index, binding := range bindings {
		if binding.ResourceID == "" {
			return fmt.Errorf("hardware resource id is required")
		}
		if binding.ID == "" {
			return fmt.Errorf("hardware binding id is required")
		}
		var resourceAccess string
		if err := tx.QueryRowContext(ctx, `SELECT access FROM hardware_resources WHERE id=?`, binding.ResourceID).Scan(&resourceAccess); err != nil {
			return fmt.Errorf("load hardware resource %s: %w", binding.ResourceID, err)
		}
		createdAt := binding.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		updatedAt := binding.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_hardware_bindings(
				task_id,id,resource_id,position,access,created_at,updated_at
			) VALUES(?,?,?,?,?,?,?)`,
			taskID, binding.ID, binding.ResourceID, index, resourceAccess,
			timeString(createdAt), timeString(updatedAt),
		); err != nil {
			return fmt.Errorf("insert hardware binding %s: %w", binding.ID, err)
		}
	}
	return nil
}
```

### `internal/store/hardware.go` · `ReplaceTaskHardwareBindings`

```go
func (s *Store) ReplaceTaskHardwareBindings(ctx context.Context, taskID string, bindings []domain.TaskHardwareBinding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeTaskHardwareBindings(ctx, tx, taskID, bindings); err != nil {
		return err
	}
	return tx.Commit()
}
```

### `internal/store/hardware.go` · `HardwareGroups`

```go
func (s *Store) HardwareGroups(ctx context.Context, taskID string) ([]domain.HardwareGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.task_id,b.id,b.resource_id,b.position,r.description,r.mode,
		       r.serial_device,r.serial_baudrate,r.network_host,r.network_port,
		       r.network_protocol,r.ssh_auth,r.username,r.credential_ref,
		       r.password_configured,r.access,b.created_at,b.updated_at
		FROM task_hardware_bindings b
		JOIN hardware_resources r ON r.id=b.resource_id
		WHERE b.task_id=?
		ORDER BY b.position,b.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.HardwareGroup, 0)
	for rows.Next() {
		var item domain.HardwareGroup
		var passwordConfigured int
		var createdAt, updatedAt string
		if err := rows.Scan(
			&item.TaskID, &item.ID, &item.ResourceID, &item.Position, &item.Description,
			&item.Mode, &item.Serial.Device, &item.Serial.Baudrate, &item.Network.Host,
			&item.Network.Port, &item.Network.Protocol, &item.Network.SSHAuth, &item.Username,
			&item.CredentialRef, &passwordConfigured, &item.Access, &createdAt, &updatedAt,
		); err != nil {
			return nil, err
		}
		item.PasswordConfigured = passwordConfigured != 0
		item.CreatedAt = parseTime(createdAt)
		item.UpdatedAt = parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}
```

### `internal/hardware/manager.go` · `Disconnect`

```go
func (manager *Manager) Disconnect(taskID, hardwareID, transport string) {
	key := attachmentKey(taskID, hardwareID, transport)
	manager.mu.Lock()
	session := manager.attachments[key]
	if session != nil {
		manager.detachLocked(key, session)
	}
	manager.mu.Unlock()
}
```

### `internal/hardware/manager.go` · `DisconnectTask`

```go
func (manager *Manager) DisconnectTask(taskID string) {
	manager.DisconnectTaskExcept(taskID, nil)
}
```

### `internal/hardware/manager.go` · `DisconnectTaskExcept`

```go
func (manager *Manager) DisconnectTaskExcept(taskID string, keepIDs []string) {
	keep := map[string]bool{}
	for _, id := range keepIDs {
		keep[id] = true
	}
	manager.mu.Lock()
	for key, session := range manager.attachments {
		if strings.HasPrefix(key, taskID+"\x00") {
			parts := strings.Split(key, "\x00")
			if len(parts) == 3 && keep[parts[1]] {
				continue
			}
			manager.detachLocked(key, session)
		}
	}
	manager.mu.Unlock()
}
```

### `internal/hardware/manager.go` · `detachLocked`

```go
func (manager *Manager) detachLocked(key string, session *terminalSession) {
	delete(manager.attachments, key)
	session.mu.Lock()
	delete(session.attachments, key)
	remaining := len(session.attachments)
	session.mu.Unlock()
	if remaining == 0 {
		delete(manager.sessions, session.targetKey)
		session.close("hardware terminal disconnected")
	}
}
```

### `internal/app/service.go` · `SetPetHardwareDisconnector`

```go
func (s *Service) SetPetHardwareDisconnector(disconnector PetHardwareDisconnector) {
	s.petHardware = disconnector
}
```

### Server 初始化实际 manager 接线

```go
	}
	if server.app != nil && server.hardware != nil {
		server.app.SetPetHardwareDisconnector(server.hardware)
	}
```

## 本问题边界的完整 diff

```diff
diff --git a/internal/app/agent_pet_domains_test.go b/internal/app/agent_pet_domains_test.go
index 15a9aeb..c48a1bc 100644
--- a/internal/app/agent_pet_domains_test.go
+++ b/internal/app/agent_pet_domains_test.go
@@ -100,12 +100,15 @@ func TestPetHardwareBindingReplacementPreservesIdentityAndRejectsWithoutMutation
 			t.Fatal("unchanged resource lost binding identity")
 		}
 	}
-	if len(recorder.calls) != 16 {
-		t.Fatalf("old connections were not closed: %v", recorder.calls)
+	if len(recorder.calls) != 0 {
+		t.Fatalf("unchanged connections were closed: %v", recorder.calls)
 	}
 	if _, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: []string{resources[16]}}); err != nil {
 		t.Fatal(err)
 	}
+	if len(recorder.calls) != 16 {
+		t.Fatalf("removed connections not closed: %v", recorder.calls)
+	}
 	if _, err := f.database.HardwareGroup(f.ctx, task.ID, resources[0]); err == nil {
 		t.Fatal("removed resource still resolves")
 	}
diff --git a/internal/app/pet_hardware_binding_alias_test.go b/internal/app/pet_hardware_binding_alias_test.go
new file mode 100644
index 0000000..38d8b4c
--- /dev/null
+++ b/internal/app/pet_hardware_binding_alias_test.go
@@ -0,0 +1,27 @@
+package app
+
+import (
+	"github.com/ChinaKai/AHA2/internal/domain"
+	"testing"
+)
+
+func TestPetHardwareNewResourceAvoidsExistingBindingAlias(t *testing.T) {
+	f := newPetTaskFixture(t)
+	enablePetHardwareTestSettings(t, f, true)
+	task := f.petTask(t)
+	for _, id := range []string{"resource-a", "resource-b"} {
+		if err := f.database.UpsertHardwareResource(f.ctx, domain.HardwareResource{ID: id, Description: id, Mode: domain.HardwareModeSerial, Serial: domain.HardwareSerialConfig{Device: "FIXTURE", Baudrate: 9600}, Access: domain.HardwareAccessReadWrite, CreatedAt: f.createdAt, UpdatedAt: f.createdAt}); err != nil {
+			t.Fatal(err)
+		}
+	}
+	if err := f.database.ReplaceTaskHardwareBindings(f.ctx, task.ID, []domain.TaskHardwareBinding{{TaskID: task.ID, ID: "resource-b", ResourceID: "resource-a"}}); err != nil {
+		t.Fatal(err)
+	}
+	view, err := f.service.PetUpdateTaskHardware(f.ctx, PetUpdateTaskHardwareInput{TaskID: task.ID, ResourceIDs: []string{"resource-a", "resource-b"}})
+	if err != nil || len(view.Bindings) != 2 {
+		t.Fatalf("valid resources rejected: %v", err)
+	}
+	if view.Bindings[0].ID != "resource-b" || view.Bindings[0].ResourceID != "resource-a" || view.Bindings[1].ID == "resource-b" || view.Bindings[1].ResourceID != "resource-b" {
+		t.Fatal("existing alias was replaced or collided with new resource")
+	}
+}
diff --git a/internal/app/pet_task_hardware.go b/internal/app/pet_task_hardware.go
index 66c7f0e..be86782 100644
--- a/internal/app/pet_task_hardware.go
+++ b/internal/app/pet_task_hardware.go
@@ -137,13 +137,23 @@ func (s *Service) PetUpdateTaskHardware(ctx context.Context, input PetUpdateTask
 		return PetTaskHardwareView{}, err
 	}
 	byResource := map[string]string{}
+	usedIDs := map[string]bool{}
 	for _, item := range existing {
 		byResource[item.ResourceID] = item.ID
+		usedIDs[item.ID] = true
 	}
 	bindings := make([]domain.TaskHardwareBinding, 0, len(input.ResourceIDs))
 	for _, raw := range input.ResourceIDs {
 		resourceID := strings.TrimSpace(raw)
-		bindings = append(bindings, domain.TaskHardwareBinding{TaskID: task.ID, ID: byResource[resourceID], ResourceID: resourceID})
+		id := byResource[resourceID]
+		if id == "" {
+			id = resourceID
+			for usedIDs[id] {
+				id = domain.NewID("hardware_binding")
+			}
+			usedIDs[id] = true
+		}
+		bindings = append(bindings, domain.TaskHardwareBinding{TaskID: task.ID, ID: id, ResourceID: resourceID})
 	}
 	if err := s.ReplaceTaskHardwareResourceBindings(ctx, task.ID, bindings); err != nil {
 		return PetTaskHardwareView{}, err
diff --git a/internal/app/task_hardware_bindings.go b/internal/app/task_hardware_bindings.go
index c2c4477..2362288 100644
--- a/internal/app/task_hardware_bindings.go
+++ b/internal/app/task_hardware_bindings.go
@@ -2,7 +2,6 @@ package app
 
 import (
 	"context"
-	"fmt"
 	"strings"
 
 	"github.com/ChinaKai/AHA2/internal/domain"
@@ -11,12 +10,16 @@ import (
 
 const MaxTaskHardwareResources = 16
 
+type HardwareBindingError struct{ Code string }
+
+func (e *HardwareBindingError) Error() string { return e.Code }
+
 // ReplaceTaskHardwareResourceBindings is shared by Web and pet management.
 // Caller-specific owner/Task permissions are checked before entering here.
 // Validation completes before any live connection is closed or row is changed.
 func (s *Service) ReplaceTaskHardwareResourceBindings(ctx context.Context, taskID string, bindings []domain.TaskHardwareBinding) error {
 	if len(bindings) > MaxTaskHardwareResources {
-		return fmt.Errorf("每个任务最多启用 %d 个硬件资源", MaxTaskHardwareResources)
+		return &HardwareBindingError{Code: "hardware_binding_limit"}
 	}
 	if _, err := s.store.Task(ctx, taskID); err != nil {
 		return err
@@ -39,11 +42,14 @@ func (s *Service) ReplaceTaskHardwareResourceBindings(ctx context.Context, taskI
 		if item.ID == "" {
 			item.ID = hardwareconfig.NormalizeID(item.ResourceID)
 		}
-		if item.ID == "" || item.ResourceID == "" || ids[item.ID] || resources[item.ResourceID] {
-			return fmt.Errorf("硬件绑定为空或重复")
+		if item.ID == "" || item.ResourceID == "" {
+			return &HardwareBindingError{Code: "hardware_binding_empty"}
+		}
+		if ids[item.ID] || resources[item.ResourceID] {
+			return &HardwareBindingError{Code: "hardware_binding_duplicate"}
 		}
 		if _, err := s.store.HardwareResource(ctx, item.ResourceID); err != nil {
-			return fmt.Errorf("硬件资源不存在")
+			return &HardwareBindingError{Code: "hardware_resource_not_found"}
 		}
 		ids[item.ID], resources[item.ResourceID] = true, true
 		item.CreatedAt, item.UpdatedAt = now, now
@@ -55,8 +61,30 @@ func (s *Service) ReplaceTaskHardwareResourceBindings(ctx context.Context, taskI
 	if err != nil {
 		return err
 	}
-	if s.petHardware != nil {
+	keepIDs := []string{}
+	keep := map[string]bool{}
+	unchanged := len(existing) == len(bindings)
+	for index, item := range bindings {
+		if previous, ok := byID[item.ID]; ok && previous.ResourceID == item.ResourceID {
+			keepIDs = append(keepIDs, item.ID)
+			keep[item.ID] = true
+		}
+		if index >= len(existing) || existing[index].ID != item.ID || existing[index].ResourceID != item.ResourceID {
+			unchanged = false
+		}
+	}
+	if !unchanged {
+		if err := s.store.ReplaceTaskHardwareBindings(ctx, taskID, bindings); err != nil {
+			return err
+		}
+	}
+	if runtime, ok := s.petHardware.(interface{ DisconnectTaskExcept(string, []string) }); ok {
+		runtime.DisconnectTaskExcept(taskID, keepIDs)
+	} else if s.petHardware != nil {
 		for _, group := range groups {
+			if keep[group.ID] {
+				continue
+			}
 			for _, transport := range []string{domain.HardwareTransportSerial, domain.HardwareTransportNetwork} {
 				if group.Supports(transport) {
 					s.petHardware.Disconnect(taskID, group.ID, transport)
@@ -64,5 +92,5 @@ func (s *Service) ReplaceTaskHardwareResourceBindings(ctx context.Context, taskI
 			}
 		}
 	}
-	return s.store.ReplaceTaskHardwareBindings(ctx, taskID, bindings)
+	return nil
 }
diff --git a/internal/hardware/manager.go b/internal/hardware/manager.go
index a1079f2..c992d78 100644
--- a/internal/hardware/manager.go
+++ b/internal/hardware/manager.go
@@ -478,9 +478,24 @@ func (manager *Manager) Disconnect(taskID, hardwareID, transport string) {
 }
 
 func (manager *Manager) DisconnectTask(taskID string) {
+	manager.DisconnectTaskExcept(taskID, nil)
+}
+
+// DisconnectTaskExcept preserves unchanged bindings while also removing live
+// attachments whose binding row has already disappeared. Callers pass only IDs
+// whose resource identity is unchanged after a successful binding update.
+func (manager *Manager) DisconnectTaskExcept(taskID string, keepIDs []string) {
+	keep := map[string]bool{}
+	for _, id := range keepIDs {
+		keep[id] = true
+	}
 	manager.mu.Lock()
 	for key, session := range manager.attachments {
 		if strings.HasPrefix(key, taskID+"\x00") {
+			parts := strings.Split(key, "\x00")
+			if len(parts) == 3 && keep[parts[1]] {
+				continue
+			}
 			manager.detachLocked(key, session)
 		}
 	}
diff --git a/internal/httpapi/agent_hardware_bindings_test.go b/internal/httpapi/agent_hardware_bindings_test.go
new file mode 100644
index 0000000..bf7befd
--- /dev/null
+++ b/internal/httpapi/agent_hardware_bindings_test.go
@@ -0,0 +1,64 @@
+package httpapi
+
+import (
+	"github.com/ChinaKai/AHA2/internal/hardware"
+	"net/http"
+	"net/http/cookiejar"
+	"reflect"
+	"testing"
+)
+
+func TestWebBindingReplacementPreservesConnectionsAndCleansOrphans(t *testing.T) {
+	f := newHardwareAuthorizationFixture(t, false)
+	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "board"}); result.IsError || result.Status != 200 {
+		t.Fatal("fixture connect failed")
+	}
+	_ = f.connection(t)
+	group, err := f.h.database.HardwareGroup(f.h.ctx, f.task.ID, "board")
+	if err != nil {
+		t.Fatal(err)
+	}
+	if _, err := f.manager.Connect(f.h.ctx, hardware.ConnectRequest{TaskID: f.task.ID, HardwareID: "orphan", Transport: "network", Group: group, Password: f.secret, ReadOnly: true}); err != nil {
+		t.Fatal("orphan fixture failed")
+	}
+	before, err := f.h.database.TaskHardwareBindings(f.h.ctx, f.task.ID)
+	if err != nil {
+		t.Fatal(err)
+	}
+	jar, _ := cookiejar.New(nil)
+	client := &http.Client{Jar: jar}
+	csrf := registerOwner(t, client, f.server.URL)
+	url := f.server.URL + "/api/v1/tasks/" + f.task.ID + "/hardware-bindings"
+	response := requestJSON(t, client, http.MethodPut, url, map[string]any{"bindings": []map[string]any{{"id": "board", "resource_id": f.board.ID}, {"id": "device", "resource_id": f.adb.ID}}}, csrf)
+	if response.StatusCode != 200 {
+		t.Fatalf("Web unchanged binding status=%d", response.StatusCode)
+	}
+	response.Body.Close()
+	after, _ := f.h.database.TaskHardwareBindings(f.h.ctx, f.task.ID)
+	if !reflect.DeepEqual(before, after) || !f.manager.Status(f.task.ID, "board", "network", false).Connected || f.manager.Status(f.task.ID, "orphan", "network", false).Connected || f.accepts.Load() != 1 {
+		t.Fatal("unchanged binding dropped a connection or retained an orphan")
+	}
+	response = requestJSON(t, client, http.MethodPut, url, map[string]any{"bindings": []map[string]any{{"id": "newalias", "resource_id": f.board.ID}}}, csrf)
+	if response.StatusCode != 200 {
+		t.Fatal("Web rebind failed")
+	}
+	response.Body.Close()
+	if f.manager.Status(f.task.ID, "board", "network", false).Connected {
+		t.Fatal("removed binding retained connection")
+	}
+	if f.raw(t, http.MethodPost, "/board/send", map[string]any{"data": "OLD-ID"}) != 404 {
+		t.Fatal("removed binding remained addressable")
+	}
+	if result := f.mcp(t, "hardware_connect", map[string]any{"hardware_id": "newalias"}); result.IsError || result.Status != 200 {
+		t.Fatal("new binding not usable")
+	}
+	_ = f.connection(t)
+	response = requestJSON(t, client, http.MethodPut, url, map[string]any{"bindings": []map[string]any{{"id": "newalias", "resource_id": "missing"}}}, csrf)
+	if response.StatusCode != 400 {
+		t.Fatal("invalid Web binding accepted")
+	}
+	response.Body.Close()
+	if !f.manager.Status(f.task.ID, "newalias", "network", false).Connected {
+		t.Fatal("invalid Web update disconnected current device")
+	}
+}
```
