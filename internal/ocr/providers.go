package ocr

import (
	"context"
	"fmt"
	"strings"
)

// Config 描述一个独立的识别服务；同一时间最多启用一个服务。
type Config struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Definition 描述可添加的识别服务类型，仅包含已实现的服务。
type Definition struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// provider 将类型定义与识别实现绑定；新增引擎时在此接入，截图流程无需改动。
type provider struct {
	definition Definition
	recognize  func(context.Context, Config, string) (string, error)
}

var providers = []provider{
	{Definition{"system", "系统 OCR"}, func(ctx context.Context, _ Config, path string) (string, error) {
		return recognizeNative(ctx, path)
	}},
}

// Definitions 返回已接入的服务目录，调用方可以独立修改返回的切片。
func Definitions() []Definition {
	definitions := make([]Definition, 0, len(providers))
	for _, p := range providers {
		definitions = append(definitions, p.definition)
	}
	return definitions
}

// DefaultConfigs 为旧配置及首次启动保留默认的系统识别行为。
func DefaultConfigs() []Config {
	return []Config{{ID: "system", Kind: "system", Name: "系统 OCR", Enabled: true}}
}

// ValidateConfigs 校验服务类型、标识及单服务启用约束，允许清空或停用全部服务。
func ValidateConfigs(configs []Config) error {
	if len(configs) > 24 {
		return fmt.Errorf("最多配置 24 个 OCR 服务")
	}
	kinds := map[string]bool{}
	for _, p := range providers {
		kinds[p.definition.Kind] = true
	}
	ids := map[string]bool{}
	enabled := 0
	for _, config := range configs {
		if strings.TrimSpace(config.ID) == "" || len(config.ID) > 80 || ids[config.ID] || !kinds[config.Kind] {
			return fmt.Errorf("OCR 服务标识重复或类型无效")
		}
		ids[config.ID] = true
		if config.Enabled {
			enabled++
		}
	}
	if enabled > 1 {
		return fmt.Errorf("每次只能启用一个 OCR 服务")
	}
	return nil
}

// ActiveConfig 返回当前启用的识别服务；没有启用服务时提示配置，避免隐式回退。
func ActiveConfig(configs []Config) (Config, error) {
	for _, config := range configs {
		if config.Enabled {
			return config, nil
		}
	}
	return Config{}, fmt.Errorf("请在“OCR 服务”中添加并启用服务")
}

// recognizeProvider 将图片交给指定类型的引擎；不支持的类型不会回退到系统识别。
func recognizeProvider(ctx context.Context, config Config, path string) (string, error) {
	for _, p := range providers {
		if config.Kind == p.definition.Kind {
			return p.recognize(ctx, config, path)
		}
	}
	return "", fmt.Errorf("不支持的 OCR 服务类型：%s", config.Kind)
}
