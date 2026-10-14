package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// legacyNames are the file names used before configuration moved into the
// database. They are looked up in the directory of the executable and in the
// config subdirectory.
var legacyNames = []string{"config.json", "model_config.json"}

// ImportLegacyFiles migrates any pre-existing config.json / model_config.json
// into the database on first run, then deletes the files. It is a no-op when
// the database already holds settings or when no legacy files are present.
//
// dirs lists the directories to scan, in order. Only files whose JSON matches
// the expected shape are imported.
func ImportLegacyFiles(repo SettingsRepo, dirs ...string) (imported []string, err error) {
	if _, found, err := repo.GetSetting("app_settings"); err != nil {
		return nil, err
	} else if found {
		// Configuration already in the database: never overwrite it.
		return nil, nil
	}

	settings, settingsFile := findLegacySettings(dirs)
	modelRaw, modelFile := findLegacyModelConfig(dirs)
	if settingsFile == "" && modelFile == "" {
		return nil, nil
	}

	if settingsFile != "" {
		// The old file shape is a superset of the new Settings struct, so a
		// direct unmarshal works. Connection strings are ignored here because
		// bootstrap already provided them.
		normalize(&settings)
		data, err := json.Marshal(settings)
		if err != nil {
			return imported, err
		}
		if err := repo.SetSetting("app_settings", data); err != nil {
			return imported, err
		}
		imported = append(imported, settingsFile)
		log.Printf("已从旧配置文件导入系统设置: %s", settingsFile)
		if err := os.Remove(settingsFile); err != nil {
			log.Printf("警告: 无法删除旧配置文件 %s: %v", settingsFile, err)
		} else {
			log.Printf("已删除旧配置文件: %s", settingsFile)
		}
	}

	if modelFile != "" {
		if err := repo.SetSetting("model_config", modelRaw); err != nil {
			return imported, err
		}
		imported = append(imported, modelFile)
		log.Printf("已从旧配置文件导入模型配置: %s", modelFile)
		if err := os.Remove(modelFile); err != nil {
			log.Printf("警告: 无法删除旧配置文件 %s: %v", modelFile, err)
		} else {
			log.Printf("已删除旧配置文件: %s", modelFile)
		}
	}
	return imported, nil
}

func findLegacySettings(dirs []string) (Settings, string) {
	for _, dir := range dirs {
		path := filepath.Join(dir, "config.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		settings := DefaultSettings()
		if err := json.Unmarshal(data, &settings); err != nil {
			continue
		}
		return settings, path
	}
	return Settings{}, ""
}

func findLegacyModelConfig(dirs []string) (json.RawMessage, string) {
	for _, dir := range dirs {
		path := filepath.Join(dir, "model_config.json")
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 || !json.Valid(data) {
			continue
		}
		return json.RawMessage(data), path
	}
	return nil, ""
}
