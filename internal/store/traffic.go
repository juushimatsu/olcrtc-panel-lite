package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/model"
)

// ApplyTrafficEvent records a journal event exactly once and updates counters.
func (s *Store) ApplyTrafficEvent(ctx context.Context, cursor string, instanceID int64, sessionID, target string, upload, download int64, eventAt time.Time) (bool, error) {
	if cursor == "" || upload < 0 || download < 0 {
		return false, fmt.Errorf("invalid traffic event")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin traffic event: %w", err)
	}
	defer tx.Rollback()
	sum := sha256.Sum256([]byte(target))
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO traffic_events(journal_cursor, instance_id, session_id, target_hash, upload_bytes, download_bytes, event_at) VALUES(?, ?, ?, ?, ?, ?, ?)`, cursor, instanceID, sessionID, hex.EncodeToString(sum[:]), upload, download, formatTime(eventAt))
	if err != nil {
		return false, fmt.Errorf("insert traffic event: %w", err)
	}
	inserted, _ := result.RowsAffected()
	if inserted == 0 {
		return false, nil
	}
	now := formatTime(time.Now())
	_, err = tx.ExecContext(ctx, `UPDATE traffic_counters SET upload_bytes=upload_bytes+?, download_bytes=download_bytes+?, total_bytes=total_bytes+?, last_event_at=?, updated_at=? WHERE instance_id=?`, upload, download, upload+download, formatTime(eventAt), now, instanceID)
	if err != nil {
		return false, fmt.Errorf("update traffic counter: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit traffic event: %w", err)
	}
	return true, nil
}

// ResetTraffic zeros an instance counter while retaining the event ledger.
func (s *Store) ResetTraffic(ctx context.Context, instanceID int64, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE traffic_counters SET upload_bytes=0, download_bytes=0, total_bytes=0, period_started_at=?, last_event_at=NULL, updated_at=? WHERE instance_id=?`, formatTime(at), formatTime(at), instanceID)
	if err != nil {
		return fmt.Errorf("reset traffic: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TrafficCounter returns exact accounting state.
func (s *Store) TrafficCounter(ctx context.Context, instanceID int64) (model.TrafficCounter, error) {
	var item model.TrafficCounter
	var period string
	var last sql.NullString
	var updated string
	err := s.db.QueryRowContext(ctx, `SELECT instance_id, upload_bytes, download_bytes, total_bytes, period_started_at, last_event_at, updated_at FROM traffic_counters WHERE instance_id=?`, instanceID).Scan(&item.InstanceID, &item.UploadBytes, &item.DownloadBytes, &item.TotalBytes, &period, &last, &updated)
	if err != nil {
		return model.TrafficCounter{}, err
	}
	item.PeriodStartedAt, err = parseTime(period)
	if err != nil {
		return model.TrafficCounter{}, err
	}
	item.LastEventAt, err = parseOptionalTime(last)
	if err != nil {
		return model.TrafficCounter{}, err
	}
	item.UpdatedAt, err = parseTime(updated)
	return item, err
}

// LastTrafficCursor returns the newest committed cursor and event time.
func (s *Store) LastTrafficCursor(ctx context.Context) (string, time.Time, error) {
	var cursor string
	var eventAt string
	err := s.db.QueryRowContext(ctx, `SELECT journal_cursor, event_at FROM traffic_events ORDER BY event_at DESC, rowid DESC LIMIT 1`).Scan(&cursor, &eventAt)
	if err != nil {
		return "", time.Time{}, err
	}
	parsed, err := parseTime(eventAt)
	if err != nil {
		return "", time.Time{}, err
	}
	return cursor, parsed, nil
}
