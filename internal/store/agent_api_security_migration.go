package store

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// agentAPIAddressAllowed is the single source of truth for the Agent API address
// boundary: HTTPS anywhere, or plaintext HTTP only on loopback.
//
// It lives in the store because the migration must apply the same rule to rows
// written by an older build that AHA applies to a new value typed today. Two
// copies of this predicate would eventually disagree, and the disagreement would
// be a security hole in exactly one of them.
func agentAPIAddressAllowed(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return true
	case "http":
		host := strings.TrimSpace(parsed.Hostname())
		if strings.EqualFold(host, "localhost") {
			return true
		}
		address := net.ParseIP(host)
		return address != nil && address.IsLoopback()
	default:
		return false
	}
}

// migrateAgentAPISecurityConvergence disables every stored Agent API address
// that the current boundary does not allow.
//
// The alternative — failing at the next Turn — would turn a security tightening
// into a broken task with no explanation at the point of breakage. Instead the
// disallowed value is dropped, the automatic selection path takes over, and a
// note is stored so the change is visible rather than silent. Safe legacy values
// survive untouched: a manual HTTPS address remains a hidden override.
func (s *Store) migrateAgentAPISecurityConvergence(ctx context.Context) error {
	now := timeString(time.Now().UTC())

	var overrideURL string
	err := s.db.QueryRowContext(ctx, `SELECT url FROM agent_api_settings WHERE id=1`).Scan(&overrideURL)
	switch {
	case err == sql.ErrNoRows:
		// Nothing was ever stored; nothing to converge.
	case err != nil:
		return fmt.Errorf("load agent api override: %w", err)
	default:
		if overrideURL != "" && !agentAPIAddressAllowed(overrideURL) {
			note := fmt.Sprintf("旧版 Agent API 地址 %q 不是 https:// 或 loopback，已禁用；AHA 将自动选择连接方式。", overrideURL)
			if _, err := s.db.ExecContext(ctx, `
				UPDATE agent_api_settings SET url='',allow_insecure=0,migration_note=?,updated_at=? WHERE id=1`,
				note, now); err != nil {
				return fmt.Errorf("disable legacy agent api override: %w", err)
			}
		} else if _, err := s.db.ExecContext(ctx, `UPDATE agent_api_settings SET allow_insecure=0 WHERE id=1`); err != nil {
			return fmt.Errorf("disable legacy agent api insecure flag: %w", err)
		}
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id,agent_api_url FROM workspaces
		WHERE agent_api_url<>'' AND read_only=0`)
	if err != nil {
		return fmt.Errorf("load legacy workspace agent api addresses: %w", err)
	}
	type legacyWorkspace struct {
		id, address string
	}
	var legacy []legacyWorkspace
	for rows.Next() {
		var item legacyWorkspace
		if err := rows.Scan(&item.id, &item.address); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy workspace agent api address: %w", err)
		}
		legacy = append(legacy, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy workspace agent api addresses: %w", err)
	}

	for _, item := range legacy {
		if agentAPIAddressAllowed(item.address) {
			// Still allowed. A manual row keeps working as the hidden override; an
			// automatic row keeps ignoring its unused value, as it already did.
			continue
		}
		note := fmt.Sprintf("旧版局域网明文 Agent API 地址 %q 已禁用；AHA 将自动选择连接方式。", item.address)
		if _, err := s.db.ExecContext(ctx, `
			UPDATE workspaces
			SET agent_api_mode='auto',agent_api_url='',agent_api_migration_note=?,
				agent_api_resolved_url='',agent_api_status='unknown',agent_api_error='',
				agent_api_last_checked_at='',updated_at=?
			WHERE id=?`,
			note, now, item.id); err != nil {
			return fmt.Errorf("disable legacy workspace %s override: %w", item.id, err)
		}
	}

	// "global" duplicated what automatic selection already does. Earlier builds
	// folded stored rows, but channel-created workspaces could still write it.
	if _, err := s.db.ExecContext(ctx, `UPDATE workspaces SET agent_api_mode='auto' WHERE agent_api_mode='global'`); err != nil {
		return fmt.Errorf("fold global agent api mode: %w", err)
	}
	return nil
}
