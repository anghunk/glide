package main

import (
	"strings"
	"unicode"
)

// smartTargetLanguage 为智能互译选择具体目标语言；显式源语言优先于本地文字判断。
// 自动模式识别简繁汉字；含假名或韩文的文本按非中文处理，纯数字等内容默认译为中文。
func smartTargetLanguage(text, source string) string {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "zh" || strings.HasPrefix(source, "zh-") || source == "chs" || source == "cht" {
		return "en"
	}
	if source != "" && source != "auto" {
		return "zh-CN"
	}
	hasHan := false
	for _, r := range text {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			return "zh-CN"
		}
		if unicode.Is(unicode.Han, r) {
			hasHan = true
		}
	}
	if hasHan {
		return "en"
	}
	return "zh-CN"
}
