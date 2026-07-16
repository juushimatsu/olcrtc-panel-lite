package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/openlibrecommunity/olcrtc-panel-lite/internal/model"
)

// CreateSubscription inserts a subscription.
func (s *Store) CreateSubscription(ctx context.Context, item model.Subscription, mirrorKeyEncrypted string) (model.Subscription, error) {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `INSERT INTO subscriptions(slug, name, refresh_interval, color, icon, enabled, mirror_enabled, mirror_key_encrypted, mirror_public_url, mirror_status, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.Slug, item.Name, item.RefreshInterval, item.Color, item.Icon, item.Enabled, item.MirrorEnabled, mirrorKeyEncrypted, item.MirrorPublicURL, item.MirrorStatus, formatTime(now), formatTime(now))
	if err != nil {
		return model.Subscription{}, fmt.Errorf("create subscription: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Subscription{}, err
	}
	return s.SubscriptionByID(ctx, id)
}

// UpdateSubscription replaces mutable subscription metadata.
func (s *Store) UpdateSubscription(ctx context.Context, item model.Subscription) (model.Subscription, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET name=?, refresh_interval=?, color=?, icon=?, enabled=?, mirror_enabled=?, mirror_public_url=?, mirror_status=?, updated_at=? WHERE slug=?`,
		item.Name, item.RefreshInterval, item.Color, item.Icon, item.Enabled, item.MirrorEnabled, item.MirrorPublicURL, item.MirrorStatus, formatTime(time.Now()), item.Slug)
	if err != nil {
		return model.Subscription{}, fmt.Errorf("update subscription: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.Subscription{}, sql.ErrNoRows
	}
	return s.Subscription(ctx, item.Slug)
}

// Subscription returns one subscription and its ordered entries.
func (s *Store) Subscription(ctx context.Context, slug string) (model.Subscription, error) {
	item, err := scanSubscription(s.db.QueryRowContext(ctx, `SELECT id, slug, name, refresh_interval, color, icon, enabled, mirror_enabled, mirror_public_url, mirror_status, created_at, updated_at FROM subscriptions WHERE slug=?`, slug).Scan)
	if err != nil {
		return model.Subscription{}, err
	}
	item.Entries, err = s.SubscriptionEntries(ctx, item.ID)
	return item, err
}

// SubscriptionByID returns one subscription and entries.
func (s *Store) SubscriptionByID(ctx context.Context, id int64) (model.Subscription, error) {
	item, err := scanSubscription(s.db.QueryRowContext(ctx, `SELECT id, slug, name, refresh_interval, color, icon, enabled, mirror_enabled, mirror_public_url, mirror_status, created_at, updated_at FROM subscriptions WHERE id=?`, id).Scan)
	if err != nil {
		return model.Subscription{}, err
	}
	item.Entries, err = s.SubscriptionEntries(ctx, item.ID)
	return item, err
}

// Subscriptions returns all subscriptions and their entries.
func (s *Store) Subscriptions(ctx context.Context) ([]model.Subscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, slug, name, refresh_interval, color, icon, enabled, mirror_enabled, mirror_public_url, mirror_status, created_at, updated_at FROM subscriptions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	items := make([]model.Subscription, 0)
	for rows.Next() {
		item, err := scanSubscription(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Entries, err = s.SubscriptionEntries(ctx, items[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func scanSubscription(scan scanner) (model.Subscription, error) {
	var item model.Subscription
	var enabled int
	var mirrorEnabled int
	var created string
	var updated string
	err := scan(&item.ID, &item.Slug, &item.Name, &item.RefreshInterval, &item.Color, &item.Icon, &enabled, &mirrorEnabled, &item.MirrorPublicURL, &item.MirrorStatus, &created, &updated)
	if err != nil {
		return model.Subscription{}, err
	}
	item.Enabled = enabled != 0
	item.MirrorEnabled = mirrorEnabled != 0
	item.CreatedAt, err = parseTime(created)
	if err != nil {
		return model.Subscription{}, err
	}
	item.UpdatedAt, err = parseTime(updated)
	return item, err
}

// DeleteSubscription deletes local subscription state.
func (s *Store) DeleteSubscription(ctx context.Context, slug string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE slug=?`, slug)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AddSubscriptionEntry inserts a linked or manual entry.
func (s *Store) AddSubscriptionEntry(ctx context.Context, item model.SubscriptionEntry) (model.SubscriptionEntry, error) {
	now := time.Now()
	var expires any
	if item.ExpiresAt != nil {
		expires = formatTime(*item.ExpiresAt)
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO subscription_entries(subscription_id, source_instance_id, raw_uri, exclave_compatible, name, color, icon, ip, comment, manual_used, manual_available, expires_at, enabled, sort_order, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.SubscriptionID, item.SourceInstanceID, nullableString(item.RawURI), item.ExclaveCompatible, item.Name, item.Color, item.Icon, item.IP, item.Comment, item.ManualUsed, item.ManualAvailable, expires, item.Enabled, item.SortOrder, formatTime(now), formatTime(now))
	if err != nil {
		return model.SubscriptionEntry{}, fmt.Errorf("add subscription entry: %w", err)
	}
	id, _ := result.LastInsertId()
	return s.SubscriptionEntry(ctx, id)
}

// UpdateSubscriptionEntry updates entry metadata and source.
func (s *Store) UpdateSubscriptionEntry(ctx context.Context, item model.SubscriptionEntry) (model.SubscriptionEntry, error) {
	var expires any
	if item.ExpiresAt != nil {
		expires = formatTime(*item.ExpiresAt)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE subscription_entries SET source_instance_id=?, raw_uri=?, exclave_compatible=?, name=?, color=?, icon=?, ip=?, comment=?, manual_used=?, manual_available=?, expires_at=?, enabled=?, sort_order=?, updated_at=? WHERE id=?`,
		item.SourceInstanceID, nullableString(item.RawURI), item.ExclaveCompatible, item.Name, item.Color, item.Icon, item.IP, item.Comment, item.ManualUsed, item.ManualAvailable, expires, item.Enabled, item.SortOrder, formatTime(time.Now()), item.ID)
	if err != nil {
		return model.SubscriptionEntry{}, fmt.Errorf("update subscription entry: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return model.SubscriptionEntry{}, sql.ErrNoRows
	}
	return s.SubscriptionEntry(ctx, item.ID)
}

// SubscriptionEntry returns one entry.
func (s *Store) SubscriptionEntry(ctx context.Context, id int64) (model.SubscriptionEntry, error) {
	return scanEntry(s.db.QueryRowContext(ctx, entrySelect+` WHERE id=?`, id).Scan)
}

const entrySelect = `SELECT id, subscription_id, source_instance_id, raw_uri, exclave_compatible, name, color, icon, ip, comment, manual_used, manual_available, expires_at, enabled, sort_order, created_at, updated_at FROM subscription_entries`

// SubscriptionEntries returns entries in publish order.
func (s *Store) SubscriptionEntries(ctx context.Context, subscriptionID int64) ([]model.SubscriptionEntry, error) {
	rows, err := s.db.QueryContext(ctx, entrySelect+` WHERE subscription_id=? ORDER BY sort_order, id`, subscriptionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.SubscriptionEntry, 0)
	for rows.Next() {
		item, err := scanEntry(rows.Scan)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanEntry(scan scanner) (model.SubscriptionEntry, error) {
	var item model.SubscriptionEntry
	var source sql.NullInt64
	var raw sql.NullString
	var compatible int
	var used sql.NullInt64
	var available sql.NullInt64
	var expires sql.NullString
	var enabled int
	var created string
	var updated string
	err := scan(&item.ID, &item.SubscriptionID, &source, &raw, &compatible, &item.Name, &item.Color, &item.Icon, &item.IP, &item.Comment, &used, &available, &expires, &enabled, &item.SortOrder, &created, &updated)
	if err != nil {
		return model.SubscriptionEntry{}, err
	}
	if source.Valid {
		v := source.Int64
		item.SourceInstanceID = &v
	}
	if raw.Valid {
		item.RawURI = raw.String
	}
	item.ExclaveCompatible = compatible != 0
	if used.Valid {
		v := used.Int64
		item.ManualUsed = &v
	}
	if available.Valid {
		v := available.Int64
		item.ManualAvailable = &v
	}
	item.ExpiresAt, err = parseOptionalTime(expires)
	if err != nil {
		return model.SubscriptionEntry{}, err
	}
	item.Enabled = enabled != 0
	item.CreatedAt, err = parseTime(created)
	if err != nil {
		return model.SubscriptionEntry{}, err
	}
	item.UpdatedAt, err = parseTime(updated)
	return item, err
}

// DeleteSubscriptionEntry removes one entry from a subscription.
func (s *Store) DeleteSubscriptionEntry(ctx context.Context, subscriptionID, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM subscription_entries WHERE subscription_id=? AND id=?`, subscriptionID, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ReorderSubscriptionEntries applies a complete ordered entry ID list.
func (s *Store) ReorderSubscriptionEntries(ctx context.Context, subscriptionID int64, ids []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for order, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE subscription_entries SET sort_order=?, updated_at=? WHERE subscription_id=? AND id=?`, order, formatTime(time.Now()), subscriptionID, id)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return sql.ErrNoRows
		}
	}
	return tx.Commit()
}

// SubscriptionMirrorKey returns encrypted key storage.
func (s *Store) SubscriptionMirrorKey(ctx context.Context, id int64) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT mirror_key_encrypted FROM subscriptions WHERE id=?`, id).Scan(&value)
	return value, err
}

// SetSubscriptionMirror updates mirror state after a sync attempt.
func (s *Store) SetSubscriptionMirror(ctx context.Context, id int64, publicURL, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET mirror_public_url=?, mirror_status=?, updated_at=? WHERE id=?`, publicURL, status, formatTime(time.Now()), id)
	return err
}

// SubscriptionSlugsForInstance returns subscriptions affected by a linked URI change.
func (s *Store) SubscriptionSlugsForInstance(ctx context.Context, instanceID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT s.slug FROM subscriptions s JOIN subscription_entries e ON e.subscription_id=s.id WHERE e.source_instance_id=?`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	slugs := make([]string, 0)
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		slugs = append(slugs, slug)
	}
	return slugs, rows.Err()
}

// TouchSubscriptions updates the standard #update value after content changes.
func (s *Store) TouchSubscriptions(ctx context.Context, slugs []string) error {
	for _, slug := range slugs {
		if _, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET updated_at=? WHERE slug=?`, formatTime(time.Now()), slug); err != nil {
			return err
		}
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
