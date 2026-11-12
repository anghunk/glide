<p align="center">
  <img src="assets/brand/logo-readme.svg" alt="Glide Logo" width="144" />
</p>
<h1 align="center">Glide</h1>
<p align="center">轻巧的原生桌面划词翻译工具，支持 macOS 与 Windows。</p>

## 功能

- **快捷翻译**：常驻系统托盘，支持划词、手动输入与剪贴板翻译。
- **多渠道对照**：并行请求多个翻译服务，结果独立展示，支持调整渠道顺序。
- **紧凑浮窗**：支持编辑重译、固定窗口、折叠结果与复制译文。
- **本地管理**：支持清空、单条删除历史，可保留最近 10 / 50 / 100 条；配置与密钥保存在本机。
- **原生体验**：基于 Go / MyGo 绘制界面，无需 WebView，正式版支持签名验证更新。

## 开始使用

从 [Releases](https://github.com/anghunk/glide/releases) 下载对应平台的安装包，启动后通过系统托盘进入“偏好设置”。默认启用必应内置翻译，其他服务可在“翻译服务”中配置。

选中文字后按 `CmdOrCtrl+Shift+T` 呼出翻译浮窗（macOS 为 `⌘⇧T`，Windows 为 `Ctrl+Shift+T`），快捷键可在设置中修改。自动划词默认关闭，可按需开启。

macOS 需在“系统设置 → 隐私与安全性 → 辅助功能”中授权 Glide，授权后重启应用。部分应用无法读取选区时，可使用托盘中的“翻译剪贴板”或“输入翻译”。当前不支持 OCR。

### macOS 提示“Apple 无法验证 Glide.app”

确认应用来自本项目的 [Releases](https://github.com/anghunk/glide/releases) 且你信任此来源后，可按以下步骤打开：

1. 尝试打开 Glide，出现提示后关闭提示框。
2. 打开“系统设置 → 隐私与安全性”，向下找到“安全性”区域。
3. 在 Glide 被阻止的提示旁点击“仍要打开”，按系统提示验证身份并确认打开。

若未看到“仍要打开”，再次尝试打开 Glide 后返回设置查看。详细说明见 [Apple 官方指南](https://support.apple.com/zh-cn/102445)。

## 翻译服务

| 服务 | 配置 |
| --- | --- |
| 谷歌内置、必应内置 | 无需密钥，可自定义服务地址 |
| Google Cloud | API Key |
| 微软 Azure | 订阅密钥、资源区域（如需） |
| 腾讯翻译 | SecretId、SecretKey、区域 |
| 百度翻译 | APP ID、密钥 |
| AI 翻译 | 上游格式、API 地址、API Key、模型（支持获取列表或自定义） |
| Ollama | 服务地址、已安装的模型 |

AI 翻译支持 [OpenAI Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)、[OpenAI Responses](https://developers.openai.com/api/reference/resources/responses/methods/create)、[Anthropic Messages](https://platform.claude.com/docs/en/api/messages/create) 和 [Gemini](https://ai.google.dev/api/generate-content)。填写地址与密钥后，点击“获取模型”即可下拉选择，也可选择“自定义模型…”手动输入。地址可填写基础地址或完整的生成接口地址；切换格式时请确认地址与上游匹配。

Ollama 默认连接 `http://127.0.0.1:11434`，需自行启动服务并安装模型。AI 与 Ollama 均支持添加多个实例。

内置网页接口可能受网络、限流或接口调整影响；各渠道有独立的文本长度限制，超限时会提示分段。需要密钥的渠道与 Ollama 尚未完成真实账户或模型联调。

## 隐私与数据

配置和密钥（包括 API Key、SecretKey）保存在本机 `settings.json`，密钥以明文保存，不再写入钥匙串或 Windows 凭据管理器；macOS 文件权限为 `0600`。翻译历史单独保存在 `history.json`。数据目录：

- macOS：`~/Library/Application Support/Glide/`
- Windows：`%AppData%\Glide\`

原文会发送至当前启用的翻译渠道；仅启用本机 Ollama 时，翻译请求留在本机。应用不监听剪贴板，仅在主动使用剪贴板翻译时读取。旧版钥匙串凭据在首次启动时自动迁移，读取旧密钥时可能需要一次系统授权；迁移成功后清理对应旧凭据。

历史可在“翻译历史”中清空或单条删除，保留条数可在“通用”中设置。

## 开发与构建

渠道图标来自 [LobeHub Icons](https://icons.lobehub.com/)，通用图标来自 [Lucide](https://lucide.dev/)，许可见 [assets/providers](assets/providers/) 与 [assets/lucide](assets/lucide/)。

版本变更见 [更新日志](CHANGELOG.md)，版本发布、自动更新与签名配置见 [发布说明](docs/releasing.md)。
