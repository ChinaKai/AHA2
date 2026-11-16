package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type DataCategory struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Bytes       int64  `json:"bytes"`
	SafeCleanup bool   `json:"safe_cleanup"`
	Note        string `json:"note,omitempty"`
}

type DataInventory struct {
	DataDir    string `json:"data_dir"`
	TotalBytes int64  `json:"total_bytes"`
	// BackupCount is the number of user-installed-* install backups. The page
	// disables cleanup when it is 1 or less, because version one always keeps
	// the newest backup.
	BackupCount int            `json:"backup_count"`
	Categories  []DataCategory `json:"categories"`
}

type DataCleanupResult struct {
	FreedBytes int64         `json:"freed_bytes"`
	Removed    int           `json:"removed"`
	Skipped    int           `json:"skipped"`
	Inventory  DataInventory `json:"inventory"`
}

// DataBackupItem is one user-installed-* install backup shown in the preview.
// Path is intentionally not serialized; the UI only needs the name, size and
// time.
type DataBackupItem struct {
	Name    string    `json:"name"`
	Bytes   int64     `json:"bytes"`
	ModTime time.Time `json:"mod_time"`
}

// DataBackupPreview is what "清理旧备份" would delete. The newest backup is
// always retained; Keep is fixed at 1 for the first version.
type DataBackupPreview struct {
	Keep       int              `json:"keep"`
	Retained   *DataBackupItem  `json:"retained,omitempty"`
	Candidates []DataBackupItem `json:"candidates"`
	FreedBytes int64            `json:"freed_bytes"`
	CanClean   bool             `json:"can_clean"`
}

// backupPreviewTimeout bounds the recursive size walk. The scan is normally
// instant, but a roaming profile, network data directory or antivirus scanner
// can stall it; the Web must get a retryable error instead of an endless
// "reading" dialog.
const backupPreviewTimeout = 10 * time.Second

func (s *Server) previewBackupCleanup(writer http.ResponseWriter, request *http.Request) {
	root := strings.TrimSpace(s.store.DataDir())
	if root == "" {
		writeError(writer, http.StatusServiceUnavailable, "data_dir_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), backupPreviewTimeout)
	defer cancel()
	preview, err := previewBackupCleanup(ctx, root)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		writeJSON(writer, status, map[string]any{"ok": false, "error": "data_cleanup_preview_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "preview": preview})
}

func (s *Server) dataInventory(writer http.ResponseWriter, request *http.Request) {
	root := strings.TrimSpace(s.store.DataDir())
	if root == "" {
		writeError(writer, http.StatusServiceUnavailable, "data_dir_unavailable")
		return
	}
	inventory, err := buildDataInventory(root)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"ok": false, "error": "data_inventory_failed", "message": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "inventory": inventory})
}

func (s *Server) cleanupData(writer http.ResponseWriter, request *http.Request) {
	root := strings.TrimSpace(s.store.DataDir())
	if root == "" {
		writeError(writer, http.StatusServiceUnavailable, "data_dir_unavailable")
		return
	}
	var payload struct {
		Category string `json:"category"`
		Confirm  bool   `json:"confirm"`
	}
	if err := decodeJSON(request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json")
		return
	}
	if !payload.Confirm {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "confirmation_required", "message": "清理需要明确确认"})
		return
	}
	result, err := cleanupDataDir(request.Context(), root, payload.Category)
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "data_cleanup_failed", "message": err.Error()})
		return
	}
	s.audit(request, "data.cleanup", "data_dir", payload.Category, map[string]any{
		"freed_bytes": result.FreedBytes, "removed": result.Removed,
	})
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "result": result})
}

func buildDataInventory(root string) (DataInventory, error) {
	root = filepath.Clean(root)
	known := map[string]DataCategory{
		"attachments":   {ID: "attachments", Label: "附件", Note: "消息附件和图片"},
		"backups":       {ID: "backups", Label: "安装备份", SafeCleanup: true, Note: "旧版本安装备份，固定保留最新 1 份后清理其余"},
		"channels":      {ID: "channels", Label: "渠道运行时", Note: "渠道插件与实例文件"},
		"plugins":       {ID: "plugins", Label: "插件", Note: "渠道插件二进制和清单"},
		"node":          {ID: "node", Label: "Node 运行时", Note: "内置 Node，不是可清理缓存"},
		"pet-workspace": {ID: "pet-workspace", Label: "宠物工作区", Note: "core、工作记录"},
		"knowledge":     {ID: "knowledge", Label: "知识库", Note: "共享知识文件"},
	}
	inventory := DataInventory{DataDir: root, Categories: make([]DataCategory, 0, len(known)+3)}
	inventory.BackupCount = countInstallBackups(root)
	for _, category := range []struct {
		dir   string
		label string
		note  string
	}{
		{dir: "attachments", label: "附件", note: "消息附件和图片"},
		{dir: "backups", label: "安装备份", note: "旧版本安装备份，固定保留最新 1 份后清理其余"},
		{dir: "channels", label: "渠道运行时", note: "渠道插件与实例文件"},
		{dir: "plugins", label: "插件", note: "渠道插件二进制和清单"},
		{dir: "node", label: "Node 运行时", note: "内置 Node，不是可清理缓存"},
		{dir: "pet-workspace", label: "宠物工作区", note: "core、工作记录"},
		{dir: "knowledge", label: "知识库", note: "共享知识文件"},
	} {
		size := directorySize(filepath.Join(root, category.dir))
		item := known[category.dir]
		item.ID, item.Label, item.Note, item.Bytes = category.dir, category.label, category.note, size
		inventory.Categories = append(inventory.Categories, item)
		inventory.TotalBytes += size
	}
	var databaseBytes, logBytes, otherBytes int64
	entries, err := os.ReadDir(root)
	if err != nil {
		return DataInventory{}, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if _, ok := known[name]; ok {
				continue
			}
			otherBytes += directorySize(filepath.Join(root, name))
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		switch {
		case name == "aha2.db" || strings.HasPrefix(name, "aha2.db-"):
			databaseBytes += info.Size()
		case strings.HasSuffix(strings.ToLower(name), ".log") || strings.HasSuffix(strings.ToLower(name), ".err") || strings.HasSuffix(strings.ToLower(name), ".tmp"):
			logBytes += info.Size()
		default:
			otherBytes += info.Size()
		}
	}
	inventory.Categories = append(inventory.Categories,
		DataCategory{ID: "database", Label: "数据库", Bytes: databaseBytes, Note: "主数据库、WAL 和共享内存文件"},
		DataCategory{ID: "logs", Label: "日志与临时文件", Bytes: logBytes, SafeCleanup: true, Note: "数据目录根下的 .log/.err/.tmp 文件"},
		DataCategory{ID: "other", Label: "其他文件", Bytes: otherBytes, Note: "无法归类或不应自动清理的文件"},
	)
	inventory.TotalBytes += databaseBytes + logBytes + otherBytes
	return inventory, nil
}

func directorySize(root string) int64 {
	total, _ := directorySizeContext(context.Background(), root)
	return total
}

// directorySizeContext is directorySize with cancellation. WalkDir only stops
// when the callback returns an error, so the context is checked on every
// entry; a timed-out preview then fails instead of blocking the HTTP handler.
func directorySizeContext(ctx context.Context, root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			if info, infoErr := entry.Info(); infoErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// backupCleanupKeep is fixed for the first version: the newest install backup
// is always kept.
const backupCleanupKeep = 1

// countInstallBackups counts user-installed-* directories without walking
// them; the inventory already sizes the whole backups directory in one pass.
func countInstallBackups(root string) int {
	entries, err := os.ReadDir(filepath.Join(filepath.Clean(root), "backups"))
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "user-installed-") {
			count++
		}
	}
	return count
}

// listInstallBackups returns user-installed-* backup directories, newest first.
func listInstallBackups(ctx context.Context, root string) ([]DataBackupItem, error) {
	backupRoot := filepath.Join(filepath.Clean(root), "backups")
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type backupItem struct {
		DataBackupItem
		path string
	}
	items := make([]backupItem, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "user-installed-") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		path := filepath.Join(backupRoot, entry.Name())
		size, sizeErr := directorySizeContext(ctx, path)
		if sizeErr != nil {
			return nil, sizeErr
		}
		items = append(items, backupItem{
			DataBackupItem: DataBackupItem{Name: entry.Name(), Bytes: size, ModTime: info.ModTime()},
			path:           path,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModTime.After(items[j].ModTime) })
	result := make([]DataBackupItem, len(items))
	for index, item := range items {
		result[index] = item.DataBackupItem
	}
	return result, nil
}

func previewBackupCleanup(ctx context.Context, root string) (DataBackupPreview, error) {
	items, err := listInstallBackups(ctx, root)
	if err != nil {
		return DataBackupPreview{}, err
	}
	preview := DataBackupPreview{Keep: backupCleanupKeep, Candidates: []DataBackupItem{}}
	if len(items) == 0 {
		return preview, nil
	}
	retained := items[0]
	preview.Retained = &retained
	for _, item := range items[backupCleanupKeep:] {
		preview.Candidates = append(preview.Candidates, item)
		preview.FreedBytes += item.Bytes
	}
	preview.CanClean = len(preview.Candidates) > 0
	return preview, nil
}

func cleanupDataDir(ctx context.Context, root, category string) (DataCleanupResult, error) {
	root = filepath.Clean(root)
	category = strings.ToLower(strings.TrimSpace(category))
	result := DataCleanupResult{}
	switch category {
	case "backups":
		items, err := listInstallBackups(ctx, root)
		if err != nil {
			return result, err
		}
		for index, item := range items {
			if index < backupCleanupKeep {
				continue
			}
			if err := os.RemoveAll(filepath.Join(root, "backups", item.Name)); err != nil {
				// A backup can be held open by the installer or a scanner. Keep
				// going so one locked directory does not block the rest.
				result.Skipped++
				continue
			}
			result.FreedBytes += item.Bytes
			result.Removed++
		}
	case "logs":
		entries, err := os.ReadDir(root)
		if err != nil {
			return result, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			lower := strings.ToLower(entry.Name())
			if !strings.HasSuffix(lower, ".log") && !strings.HasSuffix(lower, ".err") && !strings.HasSuffix(lower, ".tmp") {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
				// The service log can still be open on Windows.
				result.Skipped++
				continue
			}
			result.FreedBytes += info.Size()
			result.Removed++
		}
	default:
		return result, fmt.Errorf("不支持的清理类型")
	}
	inventory, err := buildDataInventory(root)
	if err != nil {
		return result, err
	}
	result.Inventory = inventory
	return result, nil
}
