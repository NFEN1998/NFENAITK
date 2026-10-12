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
	err := s.queryRow(`SELECT `+s.dialect.settingValueColumn()+` FROM AppSettings WHERE `+
		s.dialect.settingKeyColumn()+` = $1`, key).Scan(&value)
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
	_, err := s.exec(`INSERT INTO AppSettings (`+s.dialect.settingKeyColumn()+`, `+
		s.dialect.settingValueColumn()+`, UpdateTime) VALUES ($1, $2, NOW())`+
		s.dialect.upsertSettingSuffix(), key, string(value))
	return err
}

// DeleteSetting removes a key if present.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.exec(`DELETE FROM AppSettings WHERE `+s.dialect.settingKeyColumn()+` = $1`, key)
	return err
}
