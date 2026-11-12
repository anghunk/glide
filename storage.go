package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// diskSettings 仅保存普通配置及凭据引用，密钥交由系统凭据库保管。
type diskSettings struct {
	Settings Settings                     `json:"settings"`
	Secrets  map[string]map[string]string `json:"secrets"`
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
	for i := range disk.Settings.Providers {
		p := &disk.Settings.Providers[i]
		for field, reference := range disk.Secrets[p.ID] {
			value, err := mygo.Secrets.Get(reference)
			if err != nil {
				return Settings{}, fmt.Errorf("读取 %s 的凭据失败：%w", p.Name, err)
			}
			switch field {
			case "apiKey":
				p.APIKey = string(value)
			case "secretKey":
				p.SecretKey = string(value)
			}
		}
	}
	return disk.Settings, nil
}

// writeSettings 先写入独立版本的凭据，再原子切换 JSON 引用；失败不会修改旧配置的密钥。
func writeSettings(dir string, settings Settings) error {
	path := filepath.Join(dir, "settings.json")
	var previous diskSettings
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &previous); err != nil {
			return fmt.Errorf("原设置无法读取，已取消保存：%w", err)
		}
	}
	var version [16]byte
	if _, err := rand.Read(version[:]); err != nil {
		return err
	}
	prefix := "provider." + hex.EncodeToString(version[:]) + "."
	disk := diskSettings{Settings: cloneSettings(settings), Secrets: make(map[string]map[string]string)}
	staged := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, reference := range staged {
				if err := mygo.Secrets.Delete(reference); err != nil {
					log.Printf("清理未提交凭据失败：%v", err)
				}
			}
		}
	}()
	for i := range disk.Settings.Providers {
		p := &disk.Settings.Providers[i]
		for field, value := range map[string]string{"apiKey": p.APIKey, "secretKey": p.SecretKey} {
			if value == "" {
				continue
			}
			reference := prefix + p.ID + "." + field
			if err := mygo.Secrets.Set(reference, []byte(value)); err != nil {
				return fmt.Errorf("保存 %s 的凭据失败：%w", p.Name, err)
			}
			staged = append(staged, reference)
			if disk.Secrets[p.ID] == nil {
				disk.Secrets[p.ID] = make(map[string]string)
			}
			disk.Secrets[p.ID][field] = reference
		}
		p.APIKey, p.SecretKey = "", ""
	}
	if err := writeJSON(path, disk); err != nil {
		return err
	}
	committed = true
	for _, fields := range previous.Secrets {
		for _, reference := range fields {
			if err := mygo.Secrets.Delete(reference); err != nil {
				log.Printf("清理旧版本凭据失败：%v", err)
			}
		}
	}
	return nil
}

// writeLanguageSettings 保留已提交的凭据引用，语言切换不重复访问系统密钥库。
func writeLanguageSettings(dir string, settings Settings) error {
	path := filepath.Join(dir, "settings.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return writeSettings(dir, settings)
	}
	if err != nil {
		return err
	}
	var disk diskSettings
	if err := json.Unmarshal(data, &disk); err != nil {
		return err
	}
	disk.Settings.SourceLang = settings.SourceLang
	disk.Settings.TargetLang = settings.TargetLang
	return writeJSON(path, disk)
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
