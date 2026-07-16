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
