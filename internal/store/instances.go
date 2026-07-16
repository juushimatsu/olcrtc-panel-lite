package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
)

const instanceColumns = `i.id, i.name, i.provider, i.transport, i.room_id, i.room_channel,
i.auth_token_encrypted, i.dns, i.outbound_proxy_encrypted, i.transport_options_json,
i.liveness_json, i.max_session_duration, i.traffic_options_json, i.debug, i.reset_policy,
i.quota_bytes, i.expires_at, i.created_at, i.updated_at,
COALESCE(t.upload_bytes, 0), COALESCE(t.download_bytes, 0), COALESCE(t.total_bytes, 0), t.last_event_at`

// CreateInstance inserts metadata and initializes its traffic counter.
func (s *Store) CreateInstance(ctx context.Context, item model.Instance) (model.Instance, error) {
	options, _ := json.Marshal(item.Options)
	liveness, _ := json.Marshal(item.Liveness)
	traffic, _ := json.Marshal(item.Traffic)
	now := time.Now()
	var expires any
	if item.ExpiresAt != nil {
		expires = formatTime(*item.ExpiresAt)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Instance{}, fmt.Errorf("begin instance create: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO instances(name, provider, transport, room_id, room_channel, auth_token_encrypted, dns, outbound_proxy_encrypted, transport_options_json, liveness_json, max_session_duration, traffic_options_json, debug, reset_policy, quota_bytes, expires_at, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.Name, item.Provider, item.Transport, item.RoomID, item.RoomChannel, item.AuthToken, item.DNS, item.OutboundProxy, string(options), string(liveness), item.MaxSessionDuration, string(traffic), item.Debug, item.ResetPolicy, item.QuotaBytes, expires, formatTime(now), formatTime(now))
	if err != nil {
		return model.Instance{}, fmt.Errorf("insert instance: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Instance{}, fmt.Errorf("instance ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO traffic_counters(instance_id, period_started_at, updated_at) VALUES(?, ?, ?)`, id, formatTime(now), formatTime(now)); err != nil {
		return model.Instance{}, fmt.Errorf("initialize traffic: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Instance{}, fmt.Errorf("commit instance: %w", err)
	}
	return s.Instance(ctx, id)
}

// UpdateInstance replaces editable metadata. Secret fields must already contain ciphertext.
func (s *Store) UpdateInstance(ctx context.Context, item model.Instance) (model.Instance, error) {
	options, _ := json.Marshal(item.Options)
	liveness, _ := json.Marshal(item.Liveness)
	traffic, _ := json.Marshal(item.Traffic)
	var expires any
	if item.ExpiresAt != nil {
		expires = formatTime(*item.ExpiresAt)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE instances SET name=?, provider=?, transport=?, room_id=?, room_channel=?, auth_token_encrypted=?, dns=?, outbound_proxy_encrypted=?, transport_options_json=?, liveness_json=?, max_session_duration=?, traffic_options_json=?, debug=?, reset_policy=?, quota_bytes=?, expires_at=?, updated_at=? WHERE id=?`,
		item.Name, item.Provider, item.Transport, item.RoomID, item.RoomChannel, item.AuthToken, item.DNS, item.OutboundProxy, string(options), string(liveness), item.MaxSessionDuration, string(traffic), item.Debug, item.ResetPolicy, item.QuotaBytes, expires, formatTime(time.Now()), item.ID)
	if err != nil {
		return model.Instance{}, fmt.Errorf("update instance: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.Instance{}, sql.ErrNoRows
	}
	return s.Instance(ctx, item.ID)
}

// Instance returns one instance with exact traffic counters.
func (s *Store) Instance(ctx context.Context, id int64) (model.Instance, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+` FROM instances i LEFT JOIN traffic_counters t ON t.instance_id=i.id WHERE i.id=?`, id)
	return scanInstance(row.Scan)
}

// Instances returns all instances ordered by their non-reused numeric ID.
func (s *Store) Instances(ctx context.Context) ([]model.Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+instanceColumns+` FROM instances i LEFT JOIN traffic_counters t ON t.instance_id=i.id ORDER BY i.id`)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	defer rows.Close()
	items := make([]model.Instance, 0)
	for rows.Next() {
		item, err := scanInstance(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type scanner func(...any) error

func scanInstance(scan scanner) (model.Instance, error) {
	var item model.Instance
	var options string
	var liveness string
	var traffic string
	var debug int
	var expires sql.NullString
	var created string
	var updated string
	var lastTraffic sql.NullString
	err := scan(&item.ID, &item.Name, &item.Provider, &item.Transport, &item.RoomID, &item.RoomChannel,
		&item.AuthToken, &item.DNS, &item.OutboundProxy, &options, &liveness, &item.MaxSessionDuration,
		&traffic, &debug, &item.ResetPolicy, &item.QuotaBytes, &expires, &created, &updated,
		&item.UploadBytes, &item.DownloadBytes, &item.TotalBytes, &lastTraffic)
	if err != nil {
		return model.Instance{}, err
	}
	item.Debug = debug != 0
	if err := json.Unmarshal([]byte(options), &item.Options); err != nil {
		return model.Instance{}, fmt.Errorf("decode transport options: %w", err)
	}
	if err := json.Unmarshal([]byte(liveness), &item.Liveness); err != nil {
		return model.Instance{}, fmt.Errorf("decode liveness: %w", err)
	}
	if err := json.Unmarshal([]byte(traffic), &item.Traffic); err != nil {
		return model.Instance{}, fmt.Errorf("decode traffic options: %w", err)
	}
	item.ExpiresAt, err = parseOptionalTime(expires)
	if err != nil {
		return model.Instance{}, err
	}
	item.CreatedAt, err = parseTime(created)
	if err != nil {
		return model.Instance{}, err
	}
	item.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return model.Instance{}, err
	}
	item.LastTrafficAt, err = parseOptionalTime(lastTraffic)
	return item, err
}

// DeleteInstance removes an instance and linked subscription entries.
func (s *Store) DeleteInstance(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM instances WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// InstanceCount returns the current instance count.
func (s *Store) InstanceCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances`).Scan(&count)
	return count, err
}
