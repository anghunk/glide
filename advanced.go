package main

import (
	"encoding/json"
	"fmt"
	"glide/internal/translation"
	"io"
	"os"
)

// WebDAVConfig 只保存在本机；备份文件不包含同步服务器凭据。
type WebDAVConfig struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

const maxBackupBytes = 4 << 20

// settingsBackup 使用版本号识别配置文件，历史与 WebDAV 凭据不参与传输。
type settingsBackup struct {
	Format   string    `json:"format"`
	Version  int       `json:"version"`
	Settings *Settings `json:"settings"`
}

// encodeBackup 导出服务、密钥和偏好，排除本机同步配置。
func encodeBackup(settings Settings) ([]byte, error) {
	settings = cloneSettings(settings)
	settings.WebDAV = WebDAVConfig{}
	if settings.Providers == nil {
		settings.Providers = []translation.Config{}
	}
	return json.MarshalIndent(settingsBackup{Format: "glide-settings", Version: 1, Settings: &settings}, "", "  ")
}

// decodeBackup 在覆盖前检查文件格式；允许读取本机 settings.json，但拒绝空对象和未知版本。
func decodeBackup(data []byte, local WebDAVConfig) (Settings, error) {
	if len(data) > maxBackupBytes {
		return Settings{}, fmt.Errorf("配置文件不能超过 4 MB")
	}
	backup := settingsBackup{Settings: &Settings{PanelTranslateShortcut: "CmdOrCtrl+Enter", PanelCloseShortcut: "Escape"}}
	if err := json.Unmarshal(data, &backup); err != nil {
		return Settings{}, fmt.Errorf("配置文件不是有效 JSON")
	}
	if backup.Settings == nil || backup.Settings.Providers == nil || backup.Settings.TargetLang == "" {
		return Settings{}, fmt.Errorf("文件缺少有效的 Glide 设置")
	}
	if backup.Format != "" && (backup.Format != "glide-settings" || backup.Version != 1) {
		return Settings{}, fmt.Errorf("不支持的配置格式或版本")
	}
	backup.Settings.WebDAV = local
	return *backup.Settings, nil
}

// readBackup 限制文件大小，防止异常配置耗尽内存。
func readBackup(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBackupBytes+1))
	if err == nil && len(data) > maxBackupBytes {
		err = fmt.Errorf("配置文件不能超过 4 MB")
	}
	return data, err
}
