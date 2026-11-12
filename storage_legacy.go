package main

import (
	"fmt"
	"log"

	"github.com/egoist/mygo"
)

// legacySecretStore 限定旧版迁移仅能读取和清理凭据，不能写入新凭据。
type legacySecretStore interface {
	Get(string) ([]byte, error)
	Delete(string) error
}

type mygoLegacySecretStore struct{}

// Get 仅在首次迁移旧版配置时读取系统凭据。
func (mygoLegacySecretStore) Get(reference string) ([]byte, error) {
	return mygo.Secrets.Get(reference)
}

// Delete 仅在完整配置落盘后清理已迁移的旧凭据。
func (mygoLegacySecretStore) Delete(reference string) error { return mygo.Secrets.Delete(reference) }

// migrateLegacySecrets 先恢复全部密钥，再原子落盘；失败时保留原文件和系统凭据。
// 清理失败不会阻止使用已迁移的配置，也不会重新写入系统凭据。
func migrateLegacySecrets(dir string, disk *diskSettings, store legacySecretStore) error {
	settings := cloneSettings(disk.Settings)
	references := map[string]bool{}
	for i := range settings.Providers {
		provider := &settings.Providers[i]
		for field, reference := range disk.Secrets[provider.ID] {
			var target *string
			switch field {
			case "apiKey":
				target = &provider.APIKey
			case "secretKey":
				target = &provider.SecretKey
			default:
				return fmt.Errorf("%s 存在无法迁移的凭据字段：%s", provider.Name, field)
			}
			if reference == "" {
				continue
			}
			if *target == "" {
				value, err := store.Get(reference)
				if err != nil {
					return fmt.Errorf("迁移 %s 的密钥失败，原配置已保留：%w", provider.Name, err)
				}
				*target = string(value)
			}
			references[reference] = true
		}
	}
	if err := writeSettings(dir, settings); err != nil {
		return fmt.Errorf("保存迁移后的配置失败：%w", err)
	}
	disk.Settings, disk.Secrets = settings, nil
	for reference := range references {
		if err := store.Delete(reference); err != nil {
			log.Print("清理旧版系统凭据失败；本地配置已迁移完成")
		}
	}
	return nil
}
