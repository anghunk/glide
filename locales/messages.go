package locales

import "encoding/json"

// sourceKeys 从源语言文件建立文案索引，界面保留可读原文，语言目录使用稳定键名。
var sourceKeys = func() map[string]string {
	data, err := files.ReadFile("zh-Hans.json")
	if err != nil {
		panic(err)
	}
	var messages map[string]string
	if err := json.Unmarshal(data, &messages); err != nil {
		panic(err)
	}
	keys := make(map[string]string, len(messages))
	for key, message := range messages {
		keys[message] = key
	}
	return keys
}()

// Text 返回原文在指定语言中的文案；用户输入及未知文案保持原值。
func Text(locale, source string) string {
	if key, ok := sourceKeys[source]; ok {
		return Catalog.Format(locale, key, nil)
	}
	return source
}
