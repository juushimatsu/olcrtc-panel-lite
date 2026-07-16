package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
)

// AdminCount returns the number of configured administrators.
func (s *Store) AdminCount(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return count, nil
}

// CreateAdmin creates the single supported administrator.
func (s *Store) CreateAdmin(ctx context.Context, username, passwordHash string) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO admin_users(id, username, password_hash, created_at, updated_at) VALUES(1, ?, ?, ?, ?)`, username, passwordHash, now, now)
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	return nil
}

// AdminByUsername finds the administrator without revealing lookup details.
func (s *Store) AdminByUsername(ctx context.Context, username string) (model.Admin, error) {
	return s.scanAdmin(s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at, updated_at FROM admin_users WHERE username = ?`, username))
}

// Admin returns the single administrator.
func (s *Store) Admin(ctx context.Context) (model.Admin, error) {
	return s.scanAdmin(s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at, updated_at FROM admin_users WHERE id = 1`))
}

func (s *Store) scanAdmin(row *sql.Row) (model.Admin, error) {
	var item model.Admin
	var created string
	var updated string
	if err := row.Scan(&item.ID, &item.Username, &item.PasswordHash, &created, &updated); err != nil {
		return model.Admin{}, err
	}
	var err error
	item.CreatedAt, err = parseTime(created)
	if err != nil {
		return model.Admin{}, fmt.Errorf("parse admin created time: %w", err)
	}
	item.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return model.Admin{}, fmt.Errorf("parse admin updated time: %w", err)
	}
	return item, nil
}

// UpdateAdminCredentials changes username and password hash atomically.
func (s *Store) UpdateAdminCredentials(ctx context.Context, username, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credential update: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET username = ?, password_hash = ?, updated_at = ? WHERE id = 1`, username, passwordHash, formatTime(time.Now())); err != nil {
		return fmt.Errorf("update credentials: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit credentials: %w", err)
	}
	return nil
}

// CreateSession persists only hashed bearer and CSRF tokens.
func (s *Store) CreateSession(ctx context.Context, item model.Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id_hash, admin_id, csrf_hash, created_at, expires_at, last_seen_at, ip, user_agent) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		item.IDHash, item.AdminID, item.CSRFHash, formatTime(item.CreatedAt), formatTime(item.ExpiresAt), formatTime(item.LastSeenAt), item.IP, item.UserAgent)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Session returns a non-expired session by token hash.
func (s *Store) Session(ctx context.Context, idHash string) (model.Session, error) {
	var item model.Session
	var created string
	var expires string
	var seen string
	err := s.db.QueryRowContext(ctx, `SELECT id_hash, admin_id, csrf_hash, created_at, expires_at, last_seen_at, ip, user_agent FROM sessions WHERE id_hash = ? AND expires_at > ?`, idHash, formatTime(time.Now())).Scan(
		&item.IDHash, &item.AdminID, &item.CSRFHash, &created, &expires, &seen, &item.IP, &item.UserAgent)
	if err != nil {
		return model.Session{}, err
	}
	item.CreatedAt, err = parseTime(created)
	if err != nil {
		return model.Session{}, err
	}
	item.ExpiresAt, err = parseTime(expires)
	if err != nil {
		return model.Session{}, err
	}
	item.LastSeenAt, err = parseTime(seen)
	return item, err
}

// TouchSession updates activity and optionally extends expiration.
func (s *Store) TouchSession(ctx context.Context, idHash string, seen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id_hash = ?`, formatTime(seen), formatTime(expires), idHash)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// DeleteSession revokes one session immediately.
func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// DeleteSessions revokes all sessions.
func (s *Store) DeleteSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

// CleanupSessions removes expired sessions.
func (s *Store) CleanupSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, formatTime(time.Now()))
	return err
}

// AddAudit appends a redacted audit event.
func (s *Store) AddAudit(ctx context.Context, item model.AuditEvent) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log(action, object_type, object_id, result, actor_ip, details_redacted, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		item.Action, item.ObjectType, item.ObjectID, item.Result, item.ActorIP, item.DetailsRedacted, formatTime(item.CreatedAt))
	return err
}

// Audit returns the newest events with a hard upper bound.
func (s *Store) Audit(ctx context.Context, limit int) ([]model.AuditEvent, error) {
	if limit < 1 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, action, object_type, object_id, result, actor_ip, details_redacted, created_at FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()
	items := make([]model.AuditEvent, 0, limit)
	for rows.Next() {
		var item model.AuditEvent
		var created string
		if err := rows.Scan(&item.ID, &item.Action, &item.ObjectType, &item.ObjectID, &item.Result, &item.ActorIP, &item.DetailsRedacted, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
