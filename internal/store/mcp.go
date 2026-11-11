package store

import (
	"context"
	"strings"
	"time"

	"github.com/ChinaKai/AHA2/internal/domain"
)

func (s *Store) ListMCPServers(ctx context.Context) ([]domain.MCPServer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,transport,url,command,args_json,env_json,headers_json,tools_json,disabled_tools_json,enabled,builtin,health,last_error,last_checked_at,created_at,updated_at FROM mcp_servers ORDER BY builtin DESC,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.MCPServer{}
	for rows.Next() {
		var item domain.MCPServer
		var command, argsJSON, envJSON, headers, tools, disabledTools, health, lastError, lastCheckedAt, createdAt, updatedAt string
		var enabled, builtin int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Transport, &item.URL, &command, &argsJSON, &envJSON, &headers, &tools, &disabledTools, &enabled, &builtin, &health, &lastError, &lastCheckedAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		item.Command = command
		item.Args = decodeJSON(argsJSON, []string{})
		item.Env = decodeJSON(envJSON, map[string]string{})
		item.Headers = decodeJSON(headers, map[string]string{})
		item.Tools = decodeJSON(tools, []domain.MCPTool{})
		item.DisabledTools = decodeJSON(disabledTools, []string{})
		item.Enabled = enabled != 0
		item.Builtin = builtin != 0
		item.Health = health
		item.LastError = lastError
		item.LastCheckedAt = parseTime(lastCheckedAt)
		item.CreatedAt, item.UpdatedAt = parseTime(createdAt), parseTime(updatedAt)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpsertMCPServer(ctx context.Context, item domain.MCPServer) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mcp_servers(id,name,description,transport,url,command,args_json,env_json,headers_json,tools_json,disabled_tools_json,enabled,builtin,health,last_error,last_checked_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,
			transport=excluded.transport,url=excluded.url,command=excluded.command,
			args_json=excluded.args_json,env_json=excluded.env_json,headers_json=excluded.headers_json,tools_json=excluded.tools_json,disabled_tools_json=excluded.disabled_tools_json,
			enabled=excluded.enabled,health=excluded.health,last_error=excluded.last_error,
			last_checked_at=excluded.last_checked_at,updated_at=excluded.updated_at`,
		item.ID, item.Name, item.Description, item.Transport, item.URL, item.Command, encodeJSON(item.Args), encodeJSON(item.Env), encodeJSON(item.Headers), encodeJSON(item.Tools), encodeJSON(item.DisabledTools),
		boolInt(item.Enabled), boolInt(item.Builtin), item.Health, item.LastError,
		timeString(item.LastCheckedAt), timeString(item.CreatedAt), timeString(item.UpdatedAt))
	return err
}

// UpdateMCPServerProbe stores only the result of a tools/list probe. Keeping
// the probe columns separate from the config columns means a runtime refresh
// cannot overwrite an Owner edit (URL, env, headers, enabled) that raced it.
func (s *Store) UpdateMCPServerProbe(ctx context.Context, id string, tools []domain.MCPTool, health, lastError string, checkedAt, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE mcp_servers SET tools_json=?, health=?, last_error=?, last_checked_at=?, updated_at=?
		WHERE id=?`,
		encodeJSON(tools), health, lastError, timeString(checkedAt), timeString(updatedAt), id)
	return err
}

func (s *Store) DeleteMCPServer(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id=? AND builtin=0`, strings.TrimSpace(id))
	return err
}

func (s *Store) UpdateTaskMCPServers(ctx context.Context, taskID string, ids []string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET mcp_server_ids_json=? WHERE id=?`, encodeJSON(ids), taskID)
	return err
}
