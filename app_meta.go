package main

import (
	_ "embed"
	"encoding/json"
)

//go:embed mygo.json
var appConfigJSON []byte

// appMetadata 与打包器共用版本来源，升级只需修改 mygo.json。
var appMetadata = func() struct{ Name, Version string } {
	var metadata struct{ Name, Version string }
	if err := json.Unmarshal(appConfigJSON, &metadata); err != nil || metadata.Version == "" {
		panic("mygo.json 缺少有效的应用版本")
	}
	return metadata
}()
