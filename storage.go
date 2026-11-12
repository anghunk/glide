package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// diskSettings 保存完整本地配置；Secrets 仅用于识别并迁移旧版凭据引用。
type diskSettings struct {
	Version  int                          `json:"version,omitempty"`
	Settings Settings                     `json:"settings"`
	Secrets  map[string]map[string]string `json:"secrets,omitempty"`
}

// readSettings 恢复设置；首次启动使用默认渠道，损坏的配置返回错误以免覆盖。
func readSettings(dir string) (Settings, error) {
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return defaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	disk := diskSettings{Settings: defaultSettings()}
	if err := json.Unmarshal(data, &disk); err != nil {
		return Settings{}, fmt.Errorf("读取设置失败：%w", err)
	}
	// 将旧版自动识别搭配默认中文的配置升级为智能互译，保留其他手动目标语言。
	if disk.Version < 2 && disk.Settings.SourceLang == "auto" && disk.Settings.TargetLang == "zh-CN" {
		disk.Settings.TargetLang = "smart"
	}
	if disk.Settings.HistoryLimit != 0 && disk.Settings.HistoryLimit != 10 && disk.Settings.HistoryLimit != 50 && disk.Settings.HistoryLimit != 100 {
		disk.Settings.HistoryLimit = 50
	}
	switch disk.Settings.ThemeMode {
	case "system", "light", "dark":
	default:
		disk.Settings.ThemeMode = "system"
	}
	switch disk.Settings.UILanguage {
	case "", "zh-Hans", "zh-Hant", "en":
	default:
		disk.Settings.UILanguage = ""
	}
	if len(disk.Secrets) > 0 {
		if err := migrateLegacySecrets(dir, &disk, mygoLegacySecretStore{}); err != nil {
			return Settings{}, err
		}
	}

	return disk.Settings, nil
}

// writeSettings 原子保存完整配置及密钥，保留对损坏原文件的覆盖保护。
func writeSettings(dir string, settings Settings) error {
	path := filepath.Join(dir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		var previous diskSettings
		if err := json.Unmarshal(data, &previous); err != nil {
			return fmt.Errorf("原设置无法读取，已取消保存：%w", err)
		}
	}
	return writeJSON(path, diskSettings{Version: 2, Settings: cloneSettings(settings)})
}

// writeJSON 通过同目录临时文件替换数据，避免中断时留下半份 JSON。
func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".glide-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
