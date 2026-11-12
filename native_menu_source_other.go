//go:build !windows

package main

// captureTraySelectionSource 在 macOS 上无需恢复前台：系统状态栏菜单不会主动激活 Glide。
func captureTraySelectionSource() func() error { return nil }
