# Glide

基于 MyGo v0.8.3 原生 UI 的桌面划词翻译应用，首版面向 macOS 和 Windows。界面使用 Go 绘制，不使用 WebView。

应用平时常驻系统托盘；选中文字后按快捷键呼出紧凑翻译浮窗。浮窗纵向展示原文、语言选择及各渠道译文，可编辑重译、固定、折叠结果和复制。独立设置窗口采用 PaneNest 风格，提供翻译服务、通用偏好、快捷键、历史、隐私和关于页面。

设置窗口默认为 840×620，侧栏导航左对齐，不显示独立的系统标题栏；可拖动品牌行或分类标题移动窗口。服务列表支持拖拽调整顺序，保存后按此顺序展示译文。在 macOS 上，设置窗口打开时显示 Dock 图标，关闭时隐藏，翻译浮窗不改变 Dock 状态。

托盘菜单按翻译操作、设置与历史、帮助与关于分组，显示当前实际生效的取词快捷键；“输入翻译”可直接打开浮窗手动输入。

## 运行

需要 Go 1.27.3 或更高版本。Go 开启自动工具链时会按 `go.mod` 下载相应版本。

```bash
go tool mygo dev
```

`go tool mygo dev` 构建并运行原生桌面应用，Go 文件修改后自动重启。启动后从系统托盘选择“偏好设置”配置服务；不需要 Node.js、Bun 或前端构建工具。

默认启用必应内置翻译。其他渠道在“偏好设置 → 翻译服务”中配置并启用，可添加同类多个 AI 或 Ollama 实例。

## 翻译渠道

| 渠道 | 接入方式 | 配置 |
| --- | --- | --- |
| 谷歌内置 | 无需密钥的非官方网页接口 | 可自定义服务地址，需要网络可达 |
| 必应内置 | 无需密钥的非官方网页接口 | 可自定义必应网站地址 |
| Google Cloud | 官方 Translation Basic v2 | API Key |
| 微软 Azure | 官方 Translator v3 | 订阅密钥、资源区域（如需） |
| 腾讯翻译 | 腾讯云 TMT TextTranslate，TC3 签名 | SecretId、SecretKey、区域 |
| 百度翻译 | 官方通用文本翻译 API | APP ID、密钥 |
| AI 翻译 | OpenAI Chat Completions 兼容协议 | 服务地址、模型、API Key（按服务要求） |
| Ollama | 原生 `/api/chat` | 服务地址、已安装的模型名称 |

AI 服务地址可以填写基础地址（例如 `https://服务域名/v1`），也可填写完整的 `/chat/completions` 地址。Ollama 默认连接 `http://127.0.0.1:11434`，请先启动 Ollama 并安装模型；应用不会自动下载模型。

内置网页接口不是厂商承诺稳定的开放 API，可能因网络、限流、验证码或接口调整不可用。遇到验证时应用会显示错误，不绕过验证。各渠道独立返回结果，某个渠道失败不会阻止其他渠道完成。

AI / Ollama 当前使用整段返回；多个渠道并行请求，每个渠道完成后立即显示，不必等最慢的渠道。单次总上限为 10,000 字符；谷歌内置最多 5,000 字符，必应内置最多 1,000 字符，腾讯云需少于 6,000 字符，百度最多 6,000 字节，超限会提示分段。

腾讯渠道实现的是经典 TMT `TextTranslate` 协议，账户需具备相应调用权限；当前官方产品文档入口已有调整，可结合[官方历史 SDK](https://github.com/TencentCloud/tencentcloud-sdk-go/blob/v1.0.1000/tencentcloud/tmt/v20180321/models.go)核对接口。已用自拟短句验证必应内置真实返回；谷歌内置在本机网络下超时。需要账户密钥的渠道与 Ollama 尚未完成真实账户或模型联调。

## 跨应用取词

- 默认快捷键为 `CmdOrCtrl+Shift+T`（macOS 为 ⌘⇧T，Windows 为 Ctrl+Shift+T），可在设置中修改。
- macOS 使用 Accessibility 读取选区，需要在“系统设置 → 隐私与安全性 → 辅助功能”授权正在运行的 Glide 应用。开发版与打包版身份不同，可能需要分别授权。
- Windows 使用 UI Automation 读取选区，不申请全局辅助功能授权。高权限应用、自绘控件和未暴露选区的应用可能无法读取。
- 自动划词默认关闭；开启后监听拖选、双击的鼠标事件，合并事件后读取选区，浮窗展示时不主动抢走原应用焦点。
- 不监听剪贴板、不模拟复制按键；“翻译剪贴板”只在主动点击时读取。图片文字需先借助其他工具转为文本，当前版本未接入 OCR。

授权后重新启动 Glide，或在设置中刷新权限并保存自动划词设置。快捷键冲突会在设置中报告，不能读取选区时可复制文字后使用剪贴板翻译。

## 数据

API Key 和 SecretKey 保存在 macOS Keychain / Windows Credential Manager；配置文件只保存对应的凭据引用，APP ID、SecretId、模型和服务地址等普通配置保存在本机。设置与最近 100 条翻译历史位于应用数据目录：

- macOS：`~/Library/Application Support/Glide/`
- Windows：`%AppData%\Glide\`

历史包含原文及译文。原文会并行发送至当前启用的翻译渠道；仅启用使用本机地址的 Ollama 时，翻译请求留在本机。通过“偏好设置 → 隐私 → 清空历史”可删除记录。

## 构建与验证

```bash
# Go 静态检查
go vet ./...

# 当前平台应用包
go tool mygo build

# 在 macOS 上构建通用应用包（Apple Silicon / Intel）及 Windows x64 免安装程序
go tool mygo build -platform darwin/universal,windows/amd64 -skip-installer

# 在 macOS 上将 Windows 程序及图标打包为便携 ZIP
zip -j build/glide-0.1.0-windows-amd64.zip build/windows-amd64/Glide.exe build/windows-amd64/icon.png
```

对应产物为 `build/darwin-universal/Glide.app`、`build/darwin-universal/glide-0.1.0.dmg`、`build/windows-amd64/Glide.exe` 和 `build/glide-0.1.0-windows-amd64.zip`。Windows ZIP 解压后运行 `Glide.exe`，配置与历史仍保存在上述应用数据目录。只需 Apple Silicon 包时可将 `darwin/universal` 改为 `darwin/arm64`。

macOS 构建与磁盘镜像签名需要在 macOS 执行，未配置签名身份时使用临时签名；公开分发需配置自己的签名、公证和发布信息。两端均使用 MyGo 原生渲染，Windows 不依赖 WebView2。交叉编译通过不代表完成 Windows 真机兼容验证。`-skip-installer` 跳过 Windows 安装器及 NSIS；需要安装器时移除此参数并准备 NSIS，MyGo 可在 Windows 构建机上自动下载缺失的 NSIS。

## 代码结构

```text
main.go / desktop.go       应用生命周期、窗口、托盘及触发方式
native_settings.go        PaneNest 风格设置窗口
native_style.go           原生界面的语义配色、控件与间距
native_events.go          合并设置与翻译结果的原生窗口重绘通知
native_panel.go           紧凑翻译浮窗
service.go / storage.go    并发翻译、配置与历史
internal/translation/     翻译协议、签名、语言代码和错误处理
internal/selection/       macOS / Windows 原生选区及鼠标事件
```

界面与业务逻辑直接通过 Go 调用，不生成 JavaScript 绑定。

## 品牌与图标资源

`logo.png` 是品牌原稿。修改后在项目根目录运行 `go run ./cmd/brand-assets`，生成界面、Windows、macOS ICNS 与托盘资源；生成器不会覆盖原稿。界面及 Windows 使用圆角图，macOS ICNS 包含 32–1024px 的普通与 Retina 槽位，并为旧版 Finder / Dock 保留透明安全边距。macOS 托盘使用 40×36px 单色模板，由 MyGo 显示为 20×18pt 并适配明暗菜单栏；Windows 托盘直接使用同一张圆角 logo。

渠道图标使用 [LobeHub Icons](https://icons.lobehub.com/) 的本地 SVG，通用配置图标使用 [Lucide](https://lucide.dev/)。图标与许可均嵌入应用，运行时不从 CDN 加载。来源、固定版本及许可分别见 `assets/providers/` 与 `assets/lucide/`。

## 发布与自动更新

公开更新源为 [anghunk/glide](https://github.com/anghunk/glide)。修改 `mygo.json` 的版本后推送到默认分支，GitHub Actions 会判断是否为新版本，构建 macOS 通用版、Windows x64 / ARM64 安装包与签名更新文件，全部验证成功后自动发布 Release。同一版本不会重复发布，预发布不会替换稳定版的 latest。

正式包使用 MyGo 官方原生更新插件，启动十秒后检查，此后每六小时检查；也可通过托盘或“关于 → 软件更新”手动检查。下载和安装前验证 Ed25519 签名，默认不自动安装，重启始终由用户点击。开发版不检查更新。

密钥配置、发布产物及故障处理见 [发布说明](docs/releasing.md)。更新私钥保存在仓库外，并通过 Actions Secret `MYGO_UPDATER_PRIVATE_KEY` 提供给构建器；需要重新同步时运行 `python3 scripts/setup-release.py`。

渠道文档：[Google Cloud](https://docs.cloud.google.com/translate/docs/reference/rest/v2/translate)、[Azure Translator](https://learn.microsoft.com/azure/ai-services/translator/text-translation/reference/v3/translate)、[腾讯云 TC3 签名](https://cloud.tencent.com/document/api/213/30654)、[百度翻译开放平台](https://api.fanyi.baidu.com/product/11)、[OpenAI Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)、[Ollama Chat](https://docs.ollama.com/api/chat)。内置渠道对应[谷歌翻译](https://translate.google.com/)与[必应翻译](https://www.bing.com/translator)网页，不属于官方开放 API。
