package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// Settings keys stored in the AppSettings table.
const (
	SettingKeyAppSettings = "app_settings"
	SettingKeyModelConfig = "model_config"
)

// GetSetting returns the raw JSON value for a key. The boolean reports whether
// the row existed.
func (s *Store) GetSetting(key string) (json.RawMessage, bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT Value FROM AppSettings WHERE Key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(value), true, nil
}

// SetSetting upserts a raw JSON value for a key.
func (s *Store) SetSetting(key string, value json.RawMessage) error {
	_, err := s.db.Exec(`INSERT INTO AppSettings (Key, Value, UpdateTime) VALUES ($1, $2, NOW())
		ON CONFLICT (Key) DO UPDATE SET Value = EXCLUDED.Value, UpdateTime = NOW()`, key, string(value))
	return err
}

// DeleteSetting removes a key if present.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM AppSettings WHERE Key = $1`, key)
	return err
}
