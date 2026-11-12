package main

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

// panelNamedKeys 统一浮窗录入和解析支持的特殊按键名称。
var panelNamedKeys = map[ui.Key]string{
	ui.KeyEnter: "Enter", ui.KeyEscape: "Escape", ui.KeySpace: "Space",
	ui.KeyTab: "Tab", ui.KeyBackspace: "Backspace", ui.KeyDelete: "Delete",
	ui.KeyInsert: "Insert", ui.KeyHome: "Home", ui.KeyEnd: "End",
	ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
	ui.KeyLeft: "Left", ui.KeyRight: "Right", ui.KeyUp: "Up", ui.KeyDown: "Down",
}

// panelShortcutKeyName 返回可持久化的按键名称；不支持的按键返回空字符串。
func panelShortcutKeyName(key ui.Key) string {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return string(rune('A') + rune(key-ui.KeyA))
	case key >= ui.Key0 && key <= ui.Key9:
		return string(rune('0') + rune(key-ui.Key0))
	case key >= ui.KeyF1 && key <= ui.KeyF12:
		return fmt.Sprintf("F%d", key-ui.KeyF1+1)
	default:
		return panelNamedKeys[key]
	}
}

// panelShortcutAllowed 避免把普通文字或空格录成操作键，保留输入框的正常输入。
func panelShortcutAllowed(mods ui.Modifiers, key ui.Key) bool {
	textKey := (key >= ui.KeyA && key <= ui.KeyZ) || (key >= ui.Key0 && key <= ui.Key9) || key == ui.KeySpace
	return !textKey || mods&(ui.Ctrl|ui.Super|ui.Alt) != 0
}

// parsePanelShortcut 解析浮窗组合键；空值表示禁用，未知或重复修饰键视为无效。
func parsePanelShortcut(shortcut string) (ui.Modifiers, ui.Key, bool) {
	if shortcut == "" || len(shortcut) > 100 {
		return 0, ui.KeyUnknown, false
	}
	parts := strings.Split(shortcut, "+")
	var mods ui.Modifiers
	for _, part := range parts[:len(parts)-1] {
		var mod ui.Modifiers
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "cmdorctrl":
			mod = ui.Cmd
		case "cmd", "command", "super", "win":
			mod = ui.Super
		case "ctrl", "control":
			mod = ui.Ctrl
		case "alt", "option":
			mod = ui.Alt
		case "shift":
			mod = ui.Shift
		default:
			return 0, ui.KeyUnknown, false
		}
		if mods&mod != 0 {
			return 0, ui.KeyUnknown, false
		}
		mods |= mod
	}
	name := strings.TrimSpace(parts[len(parts)-1])
	for _, span := range [][2]ui.Key{{ui.KeyA, ui.KeyZ}, {ui.Key0, ui.Key9}, {ui.KeyF1, ui.KeyF12}} {
		for key := span[0]; key <= span[1]; key++ {
			if strings.EqualFold(name, panelShortcutKeyName(key)) {
				return mods, key, panelShortcutAllowed(mods, key)
			}
		}
	}
	for key, label := range panelNamedKeys {
		if strings.EqualFold(name, label) {
			return mods, key, panelShortcutAllowed(mods, key)
		}
	}
	return 0, ui.KeyUnknown, false
}

// panelShortcutMatches 在输入框处理按键前匹配设置，避免编辑器吞掉 Enter 等操作键。
func panelShortcutMatches(shortcut string, mods ui.Modifiers, key ui.Key) bool {
	wantMods, wantKey, ok := parsePanelShortcut(shortcut)
	return ok && mods == wantMods && key == wantKey
}

// panelShortcutTooltip 仅为已启用的快捷键追加当前平台的组合提示。
func panelShortcutTooltip(label, shortcut string) string {
	if shortcut == "" {
		return label
	}
	modifier := "Ctrl"
	if runtime.GOOS == "darwin" {
		modifier = "⌘"
	}
	return label + "（" + strings.ReplaceAll(shortcut, "CmdOrCtrl", modifier) + "）"
}
