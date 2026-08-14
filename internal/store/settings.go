package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SetSetting upserts a plain or already-encrypted setting.
func (s *Store) SetSetting(ctx context.Context, key, value string, encrypted bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value, encrypted, updated_at) VALUES(?, ?, ?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, encrypted=excluded.encrypted, updated_at=excluded.updated_at`, key, value, encrypted, formatTime(time.Now()))
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// SetSettings atomically upserts a group of plain settings.
func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin settings transaction: %w", err)
	}
	defer tx.Rollback()
	updatedAt := formatTime(time.Now())
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value, encrypted, updated_at) VALUES(?, ?, 0, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, encrypted=0, updated_at=excluded.updated_at`, key, value, updatedAt); err != nil {
			return fmt.Errorf("set setting %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings transaction: %w", err)
	}
	return nil
}

// Setting returns a setting and whether its value is encrypted.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	var encrypted int
	err := s.db.QueryRowContext(ctx, `SELECT value, encrypted FROM settings WHERE key=?`, key).Scan(&value, &encrypted)
	if err != nil {
		return "", false, err
	}
	return value, encrypted != 0, nil
}

// DeleteSetting clears a setting explicitly.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
	return err
}

// SettingOrDefault reads a plain setting or returns fallback.
func (s *Store) SettingOrDefault(ctx context.Context, key, fallback string) (string, error) {
	value, _, err := s.Setting(ctx, key)
	if err == nil {
		return value, nil
	}
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	return "", err
}
