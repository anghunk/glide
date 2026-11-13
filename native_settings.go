package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"glide/internal/translation"
)

// settingsUI 的可编辑状态仅由主线程访问；磁盘与系统操作在后台执行。
type settingsUI struct {
	openAtLogin                          bool
	loginBusy                            bool
	generalTab                           int
	iconPickerOpen                       bool
	iconPickerProviderID                 string
	iconPickerCategory                   string
	visibleSecrets                       map[string]bool
	home                                 *homeUI
	advancedBusy                         bool
	remoteBackups                        []webDAVBackup
	remoteBackupsLoaded                  bool
	remoteBackupsSource                  WebDAVConfig
	aiTests                              map[string]*aiTestState
	modelLists                           map[string]*modelListState
	autoSaving                           bool
	saveTimer                            *time.Timer
	saveRevision                         uint64
	observedSettings                     Settings
	confirmingDelete                     bool
	recordingShortcut                    string
	lastHotkeyError                      string
	app                                  *GlideService
	state                                AppState
	draft                                Settings
	category, query, providerID          string
	saving, loadingHistory, clearConfirm bool
	historyQuery                         string
	messages                             map[string]settingsNotice
	pendingSaveScope                     string
	backupDrawerOpen                     bool
	history                              []TranslationResponse
	expanded                             map[string]bool
	providerList                         ui.ListState
	ocrProviderID                        string
	ocrProviderList                      ui.ListState
}

// newSettingsUI 创建原生设置视图，不会启动翻译请求或自动申请权限。
func newSettingsUI(app *GlideService) *settingsUI {
	s := &settingsUI{app: app, category: "home", expanded: map[string]bool{}, visibleSecrets: map[string]bool{}, messages: map[string]settingsNotice{}}
	s.adopt(app.GetState())
	s.openAtLogin = mygo.App.OpenAtLogin()
	s.home = newHomeUI(app)
	s.observedSettings = cloneSettings(s.draft)
	s.providerList.Key = func(row int) any { return s.draft.Providers[row].ID }
	s.providerList.Label = func(row int) string { return providerDisplayName(s.draft.Providers[row].Name) }
	s.providerList.Reorder = s.reorderProviders
	s.ocrProviderList.Key = func(row int) any { return s.draft.OCRProviders[row].ID }
	s.ocrProviderList.Label = func(row int) string { return ocrDisplayName(s.draft.OCRProviders[row].Name) }
	return s
}

// adopt 将保存后的快照作为新基线，并保持当前服务的选中状态。
func (s *settingsUI) adopt(state AppState) {
	s.state = state
	s.draft = cloneSettings(state.Settings)
	s.selectOCRProvider()
	found := false
	for _, p := range s.draft.Providers {
		if p.ID == s.providerID {
			found = true
			break
		}
	}
	if !found {
		s.providerID = ""
		if len(s.draft.Providers) > 0 {
			s.providerID = s.draft.Providers[0].ID
		}
	}
}

// dirty 比较编辑草稿与上次加载的设置，不将权限状态变化误视为未保存编辑。
func (s *settingsUI) dirty() bool { return !reflect.DeepEqual(s.draft, s.state.Settings) }

// reload 在主线程刷新外部变更；有未保存编辑时只更新权限和运行状态。
func (s *settingsUI) reload() {
	latest := s.app.GetState()
	if !s.saving && !s.autoSaving && !s.dirty() {
		s.adopt(latest)
	} else {
		s.state.Platform, s.state.HotkeyError = latest.Platform, latest.HotkeyError
	}
}

// runTask 自动保存时允许继续编辑，完成后保留保存期间的新修改。
func (s *settingsUI) runTask(task func() (AppState, error)) {
	if s.saving || s.autoSaving {
		return
	}
	submitted := cloneSettings(s.draft)
	s.autoSaving = true
	scope := s.pendingSaveScope
	if scope == "" {
		scope = s.saveScope()
	}
	s.pendingSaveScope = ""
	s.showNotice(scope, "", nil)
	go func() {
		state, err := task()
		s.app.mainWindow.Update(func() {
			s.autoSaving = false
			if err != nil {
				s.showNotice(scope, "", fmt.Errorf(tr("自动保存失败：%w"), err))
				if !reflect.DeepEqual(s.draft, submitted) && s.saveTimer == nil {
					s.save()
				}
				return
			}
			if reflect.DeepEqual(s.draft, submitted) {
				s.adopt(state)
			} else {
				s.draft = mergeSettingsDraft(submitted, s.draft, state.Settings)
				s.state = state
			}
			if s.dirty() && s.saveTimer == nil {
				s.save()
			}
		})
	}()
}

// scheduleSave 合并连续文本输入；开关、选择和增删操作立即保存。
// 定时回调回到主线程读取最新草稿，避免后台访问界面状态。
func (s *settingsUI) scheduleSave() {
	if s.saving {
		return
	}
	if reflect.DeepEqual(s.observedSettings, s.draft) {
		return
	}
	previous := s.observedSettings
	s.pendingSaveScope = s.saveScope()
	s.observedSettings = cloneSettings(s.draft)
	s.saveRevision++
	revision := s.saveRevision
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	if !s.dirty() {
		return
	}
	// 服务文本编辑短暂合并，避免每输入一个字符都写入配置文件。
	textEdit := previous.ProxyAddress != s.draft.ProxyAddress
	if len(previous.Providers) == len(s.draft.Providers) {
		for i, after := range s.draft.Providers {
			before := previous.Providers[i]
			if before.ID != after.ID {
				textEdit = false
				break
			}
			before.Enabled = after.Enabled
			if !reflect.DeepEqual(before, after) {
				textEdit = true
			}
		}
	}
	if !textEdit && len(previous.OCRProviders) == len(s.draft.OCRProviders) {
		for i, after := range s.draft.OCRProviders {
			before := previous.OCRProviders[i]
			if before.ID != after.ID {
				textEdit = false
				break
			}
			before.Enabled = after.Enabled
			if !reflect.DeepEqual(before, after) {
				textEdit = true
			}
		}
	}
	if !textEdit {
		s.save()
		return
	}
	s.saveTimer = time.AfterFunc(250*time.Millisecond, func() {
		s.app.mainWindow.Update(func() {
			if revision != s.saveRevision {
				return
			}
			s.saveTimer = nil
			s.save()
		})
	})
}

// save 仅用本次实际编辑的值覆盖最新设置，避免语言浮窗的并行修改被旧草稿覆盖。
func (s *settingsUI) save() {
	if s.saving {
		s.observedSettings = cloneSettings(s.state.Settings)
		return
	}
	if s.autoSaving || !s.dirty() {
		return
	}
	draft, baseline := cloneSettings(s.draft), cloneSettings(s.state.Settings)
	s.runTask(func() (AppState, error) {
		latest := s.app.GetState().Settings
		draft = mergeSettingsDraft(baseline, draft, latest)
		return s.app.SaveSettings(draft)
	})
}

// refreshPermission 重新检查授权并启动已经保存的监听偏好，保留尚未保存的服务表单。
func (s *settingsUI) refreshPermission() {
	if s.saving {
		return
	}
	s.saving = true
	scope := "general/系统权限"
	s.showNotice(scope, "", nil)
	window := s.app.mainWindow
	go func() {
		latest := s.app.GetState()
		_, err := s.app.SaveSettings(latest.Settings)
		latest = s.app.GetState()
		window.Update(func() {
			s.saving = false
			s.state.Platform, s.state.HotkeyError = latest.Platform, latest.HotkeyError
			if err != nil {
				s.showNotice(scope, "", err)
			} else if latest.Platform.PermissionGranted {
				s.showNotice(scope, "取词权限已刷新", nil)
			} else {
				s.showNotice(scope, "尚未取得授权，请在系统设置中允许 Glide 后重试", nil)
			}
		})
	}()
}

// externalAction 异步打开系统设置或服务文档，错误保持在设置页中可见。
func (s *settingsUI) externalAction(action func() error) {
	window := s.app.mainWindow
	scope := s.operationScope()
	if s.category == "about" {
		scope = "about/应用信息"
	}
	go func() {
		if err := action(); err != nil {
			window.Update(func() { s.showNotice(scope, "", err) })
		}
	}()
}

// loadHistory 按需读取本地记录，并在主线程发布查询结果。
func (s *settingsUI) loadHistory() {
	if s.loadingHistory {
		return
	}
	s.loadingHistory = true
	window := s.app.mainWindow
	go func() {
		items := s.app.GetHistory()
		window.Update(func() { s.history, s.loadingHistory = items, false })
	}()
}

// clearHistory 通过第二次明确点击确认清空，后台删除完成后刷新本地列表。
func (s *settingsUI) clearHistory() {
	if !s.clearConfirm {
		s.clearConfirm = true
		return
	}
	if s.saving {
		return
	}
	s.saving = true
	scope := s.operationScope()
	s.showNotice(scope, "", nil)
	window := s.app.mainWindow
	go func() {
		err := s.app.ClearHistory()
		window.Update(func() {
			s.saving, s.clearConfirm = false, false
			if err != nil {
				s.showNotice(scope, "", err)
			} else {
				s.history = nil
				s.showNotice(scope, "翻译历史已清空", nil)
			}
		})
	}()
}

// addProvider 复用该渠道的默认连接字段，为多个 AI 地址和本地模型创建独立实例。
func (s *settingsUI) addProvider(definition translation.Definition) {
	if s.saving {
		return
	}
	if len(s.draft.Providers) >= 24 {
		s.showNotice("services/服务配置", "", errors.New(tr("最多配置 24 个翻译服务")))
		return
	}
	var p translation.Config
	for _, initial := range translation.DefaultConfigs() {
		if initial.Kind == definition.Kind {
			p = initial
			break
		}
	}
	p.ID, p.Kind, p.Name, p.Enabled = fmt.Sprintf("%s-%d", definition.Kind, time.Now().UnixNano()), definition.Kind, definition.Name, false
	count := 0
	for _, old := range s.draft.Providers {
		if old.Kind == p.Kind {
			count++
		}
	}
	if count > 0 {
		p.Name = fmt.Sprintf("%s %d", p.Name, count+1)
	}
	s.draft.Providers = append(s.draft.Providers, p)
	s.providerID, s.query = p.ID, ""
}

// duplicateProvider 按 ID 复制完整服务配置，使用独立 ID 和不重名的名称并选中新副本。
// 达到服务数量上限或源服务已移除时，不修改草稿。
func (s *settingsUI) duplicateProvider(id string) {
	if s.saving {
		return
	}
	if len(s.draft.Providers) >= 24 {
		s.showNotice("services/服务配置", "", errors.New(tr("最多配置 24 个翻译服务")))
		return
	}
	for _, provider := range s.draft.Providers {
		if provider.ID != id {
			continue
		}
		copy := provider
		copy.ID = fmt.Sprintf("%s-%d", provider.Kind, time.Now().UnixNano())
		baseName := providerDisplayName(provider.Name) + tr(" 副本")
		names := make(map[string]bool, len(s.draft.Providers))
		for _, item := range s.draft.Providers {
			names[item.Name] = true
		}
		copy.Name = baseName
		for n := 2; names[copy.Name]; n++ {
			copy.Name = fmt.Sprintf("%s %d", baseName, n)
		}
		s.draft.Providers = append(s.draft.Providers, copy)
		s.providerID, s.query = copy.ID, ""
		return
	}
}

// removeProvider 确认后移除选中的服务实例，自动保存并生效。
func (s *settingsUI) removeProvider() {
	if s.saving {
		return
	}
	for _, provider := range s.draft.Providers {
		if provider.ID != s.providerID {
			continue
		}
		id := provider.ID
		s.confirmDeletion("删除服务", tr("确定删除“")+provider.Name+"”？", func() {
			if s.saving {
				return
			}
			for i, item := range s.draft.Providers {
				if item.ID != id {
					continue
				}
				s.draft.Providers = append(s.draft.Providers[:i], s.draft.Providers[i+1:]...)
				s.providerID = ""
				if len(s.draft.Providers) > 0 {
					s.providerID = s.draft.Providers[min(i, len(s.draft.Providers)-1)].ID
				}
				return
			}
		})
		return
	}
}

// reorderProviders 将拖动行插到原列表的 to 行之前，顺序自动应用于翻译浮窗。
// to 等于服务数量时表示末尾；移动只改变顺序，选中服务及未保存的凭据保持原值。
func (s *settingsUI) reorderProviders(rows []int, to int) {
	if s.saving || len(rows) == 0 || to < 0 || to > len(s.draft.Providers) {
		return
	}
	selected := make(map[int]bool, len(rows))
	for _, row := range rows {
		if row < 0 || row >= len(s.draft.Providers) {
			return
		}
		selected[row] = true
	}
	moved := make([]translation.Config, 0, len(selected))
	remaining := make([]translation.Config, 0, len(s.draft.Providers)-len(selected))
	insertion := 0
	for row, provider := range s.draft.Providers {
		if selected[row] {
			moved = append(moved, provider)
		} else {
			remaining = append(remaining, provider)
			if row < to {
				insertion++
			}
		}
	}
	ordered := make([]translation.Config, 0, len(s.draft.Providers))
	ordered = append(ordered, remaining[:insertion]...)
	ordered = append(ordered, moved...)
	ordered = append(ordered, remaining[insertion:]...)
	if !reflect.DeepEqual(s.draft.Providers, ordered) {
		s.draft.Providers = ordered
	}
}

// view 构建 PaneNest 风格的侧栏与圆角主面板，所有业务操作均来自真实服务。
func (s *settingsUI) view(c *ui.Context) {
	s.reload()
	p := pnApplyTheme(c, s.draft.ThemeMode)
	ui.Row(c).Fill().Padding(8).Gap(8).TextColor(p.Text).Children(func() {
		s.sidebar(c, p)
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(p.Surface).Radius(14).Border(.5, p.Divider).Clip().Children(func() {
			ui.Row(c).Height(40).Shrink(0).Padding(0, 9, 0, 18).Gap(8).BorderWidth(0, 0, .5, 0).BorderColor(p.Divider).Children(func() {
				ui.Row(c).Grow(1).FillHeight().Gap(8).DragWindow().Children(func() {
					ui.Text(c, tr(s.categoryTitle())).FontSize(15).FontWeight(650).Grow(1)
				})
				if s.state.Platform.OS == "windows" {
					minimize := ui.ButtonBase(c.Key("minimize-settings")).Size(25, 25).Radius(7).Center().Label(tr("最小化设置")).Tooltip(tr("最小化"))
					if minimize.Hovered() {
						minimize.Background(p.Hover)
					}
					minimize.Children(func() { pnIcon(c, "minus", 15, p.Muted) }).OnClick(s.app.mainWindow.Minimize)
				}
				if s.state.Platform.OS != "darwin" {
					close := ui.ButtonBase(c.Key("close-settings")).Size(25, 25).Radius(7).Center().Label(tr("关闭设置")).Tooltip(tr("关闭设置"))
					if close.Hovered() {
						close.Background(p.Hover)
					}
					close.Children(func() { pnIcon(c, "close", 15, p.Muted) }).OnClick(s.app.HideMain)
				}
			})
			ui.Column(c.Key("settings-page-" + s.category)).Grow(1).MinHeight(0).FillWidth().Disabled(s.saving).
				Transition(settingsPageTransition()).Children(func() {
				switch s.category {
				case "home":
					s.home.view(c, p, s.draft)
				case "advanced":
					s.advanced(c, p)
				case "proxy":
					s.proxy(c, p)
				case "services":
					s.services(c, p)
				case "ocr":
					s.ocrServices(c, p)
				case "shortcut":
					s.shortcut(c, p)
				case "history":
					s.historyView(c, p)
				case "about":
					s.about(c, p)
				default:
					s.general(c, p)
				}
			})
		})
	})
	s.providerIconDialog(c, p)
	s.backupDrawer(c, p)
	s.scheduleSave()
}

type settingsCategory struct{ ID, Label, Icon, Keywords string }

var settingsCategories = []settingsCategory{{"home", "首页", "home", "翻译 对比 输入"}, {"services", "翻译服务", "globe", "渠道 谷歌 Google Bing 必应 腾讯 百度 DeepL DeepLX AI Ollama 模型 密钥"}, {"ocr", "OCR 服务", "screenshot", "OCR 截图 图片 文字识别 系统 本地 离线 Vision Windows 腾讯 阿里 火山 百度 华为 讯飞 网易 有道 AI 视觉 模型 OpenAI Anthropic Gemini Tencent Aliyun Volcengine Baidu Huawei Xfyun Youdao"}, {"general", "设置", "settings", "自动 划词 权限 原文 语言 浮窗"}, {"shortcut", "快捷键", "keyboard", "热键 组合 呼出"}, {"history", "翻译历史", "history", "记录 原文 译文"}, {"advanced", "备份同步", "refresh", "导入 导出 WebDAV 同步 备份"}, {"proxy", "系统代理", "globe", "网络 HTTP HTTPS SOCKS proxy"}, {"about", "关于", "info", "版本 Glide 隐私 本地 数据 密钥 清空"}}

// categoryTitle 返回当前分类标题。
func (s *settingsUI) categoryTitle() string {
	for _, item := range settingsCategories {
		if item.ID == s.category {
			return item.Label
		}
	}
	return "设置"
}

// categoryMatches 搜索设置分类及已配置的服务名称，不生成无效入口。
func (s *settingsUI) categoryMatches(item settingsCategory) bool {
	query := strings.ToLower(strings.TrimSpace(s.query))
	if _, matched := matchingGeneralTab(query); item.ID == "general" && matched {
		return true
	}
	if query == "" || strings.Contains(strings.ToLower(item.Label+" "+item.Keywords+" "+tr(item.Label)+" "+tr(item.Keywords)), query) {
		return true
	}
	if item.ID == "services" {
		for _, p := range s.draft.Providers {
			if strings.Contains(strings.ToLower(p.Name+" "+p.Kind+" "+p.Model), query) {
				return true
			}
		}
	}
	if item.ID == "ocr" {
		for _, p := range s.draft.OCRProviders {
			if strings.Contains(strings.ToLower(p.Name+" "+p.Kind+" "+p.Model), query) {
				return true
			}
		}
	}
	return false
}

// sidebar 提供侧栏搜索和功能分类。
func (s *settingsUI) sidebar(c *ui.Context, p pnPalette) {
	ui.Column(c).Width(180).Shrink(0).FillHeight().Padding(8, 3).Gap(7).Children(func() {
		if s.state.Platform.OS == "darwin" {
			// 原生按钮位于侧栏顶部，只预留按钮所需空间，避免标题栏高度撑大品牌区。
			ui.Box(c).Height(20).Shrink(0).DragWindow()
		}
		ui.Row(c).Height(40).Shrink(0).Gap(9).Padding(0, 7).DragWindow().Children(func() {
			pnBrandLogo(c, 28)
			ui.Text(c, "Glide").FontSize(16)
		})
		if ui.SearchField(c.Key("settings-search"), &s.query).Placeholder(tr("搜索设置")).Height(29).FontSize(12.5).Changed() {
			for _, item := range settingsCategories {
				if s.categoryMatches(item) {
					s.category = item.ID
					if item.ID == "general" {
						if tab, matched := matchingGeneralTab(s.query); matched {
							s.generalTab = tab
						}
					}
					if item.ID == "services" && strings.TrimSpace(s.query) != "" {
						query := strings.ToLower(strings.TrimSpace(s.query))
						for _, provider := range s.draft.Providers {
							if strings.Contains(strings.ToLower(provider.Name+" "+provider.Kind+" "+provider.Model), query) {
								s.providerID = provider.ID
								break
							}
						}
					}
					if item.ID == "history" {
						s.loadHistory()
					}
					break
				}
			}
		}
		ui.Spacer(c).Height(5).Grow(0)
		matched := 0
		for _, item := range settingsCategories {
			if !s.categoryMatches(item) {
				continue
			}
			matched++
			item := item
			// 各状态保留相同边框宽度，避免切换选中项时图标和文字位移。
			b := ui.ButtonBase(c.Key(item.ID)).FillWidth().Height(32).Padding(0, 10).Gap(10).Justify(ui.Start).AlignItems(ui.Center).Radius(8).Border(.5, ui.RGBA(0, 0, 0, 0)).Disabled(s.saving).
				Transition(ui.ElementTransition{Colors: true, Shadows: true, Duration: 140 * time.Millisecond})
			if item.ID == s.category {
				b.Background(p.Selected).Shadow(0, .5, 2, 0, ui.RGBA(0, 0, 0, .05)).BorderColor(p.Divider)
			} else if b.Hovered() {
				b.Background(p.Hover)
			}
			b.Children(func() {
				color := p.Muted
				if item.ID == s.category {
					color = p.Accent
				}
				pnIcon(c, item.Icon, 16, color).Shrink(0)
				ui.Text(c, tr(item.Label)).FontSize(13).TextAlign(ui.Start)
			})
			b.OnClick(func() {
				s.category, s.clearConfirm = item.ID, false
				if item.ID == "general" {
					if tab, matched := matchingGeneralTab(s.query); matched {
						s.generalTab = tab
					}
				}
				if item.ID == "history" {
					s.loadHistory()
				}
			})
		}
		if matched == 0 {
			ui.Text(c, tr("没有匹配的设置")).FontSize(12).TextColor(p.Muted).Padding(10, 8)
		}
		ui.Spacer(c)
	})
}

// group 为同一主题设置提供一致的圆角分组和细分隔线。
func (s *settingsUI) group(c *ui.Context, p pnPalette, title string, content func()) {
	ui.Column(c).Shrink(0).Gap(7).Children(func() {
		ui.Text(c, tr(title)).FontSize(12).FontWeight(600).TextColor(p.Muted).Padding(0, 4)
		s.card(c, p, title, content)
	})
}

// card 展示无外部标题的设置卡片；合并分组时保留各设置原有的错误提示位置。
func (s *settingsUI) card(c *ui.Context, p pnPalette, section string, content func(), additionalSections ...string) {
	ui.Column(c).Shrink(0).Gap(7).Children(func() {
		ui.Column(c).Background(p.Group).Radius(12).Padding(3, 13).Dividers(.5, p.Divider).Children(content)
		s.inlineNotice(c, p, section)
		for _, section := range additionalSections {
			s.inlineNotice(c, p, section)
		}
	})
}

// row 将设置名称与说明放在左侧，右侧控件保持稳定尺寸。
func (s *settingsUI) row(c *ui.Context, p pnPalette, title, detail string, control func()) {
	ui.Row(c).Padding(9, 0).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, tr(title)).FontSize(13.5)
			if detail != "" {
				ui.Text(c, tr(detail)).FontSize(12).TextColor(p.Muted).LineHeight(1.4)
			}
		})
		control()
	})
}

// generalSettingsTabs 集中维护页签及搜索词，新增设置时可扩展对应分组。
var generalSettingsTabs = []struct {
	Label       string
	SearchTerms []string
}{
	{"外观", []string{"主题风格", "明亮", "黑夜", "跟随系统", "界面语言", "托盘", "左键点击托盘显示设置面板", "外观", "启动", "开机自启", "登录", "自启动"}},
	{"翻译偏好", []string{"取词方式", "自动划词翻译", "默认语言", "源语言", "目标语言", "智能互译", "翻译历史", "保留条数", "不保留"}},
	{"翻译浮窗", []string{"打开翻译浮窗", "浮窗随鼠标显示", "显示原文输入框", "显示语言栏"}},
	{"系统权限", []string{"跨应用取词已获授权", "需要允许跨应用取词", "打开辅助功能设置", "刷新权限"}},
}

// matchingGeneralTab 返回搜索命中的页签；空查询或无匹配时保留当前选择。
func matchingGeneralTab(query string) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0, false
	}
	for i, tab := range generalSettingsTabs {
		terms := append([]string{tab.Label}, tab.SearchTerms...)
		for _, term := range terms {
			if strings.Contains(strings.ToLower(term+" "+tr(term)), query) {
				return i, true
			}
		}
	}
	return 0, false
}

// general 固定显示页签栏，各页签独立滚动并保留当前选择。
func (s *settingsUI) general(c *ui.Context, p pnPalette) {
	labels := make([]string, len(generalSettingsTabs))
	for i, tab := range generalSettingsTabs {
		labels[i] = tr(tab.Label)
	}
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Row(c).Shrink(0).FillWidth().Padding(8, 18, 0).BorderWidth(0, 0, .5, 0).BorderColor(p.Divider).Children(func() {
			ui.Tabs(c.Key("general-tabs"), &s.generalTab, labels...).FontSize(12.5)
		})
		ui.Scroll(c.Key(fmt.Sprintf("general-scroll-%d", s.generalTab))).Grow(1).MinHeight(0).FillWidth().Padding(18).Gap(17).
			Transition(settingsPageTransition()).Children(func() {
			switch s.generalTab {
			case 1:
				s.generalTranslation(c, p)
			case 2:
				s.generalPanel(c, p)
			case 3:
				s.generalPermissions(c, p)
			default:
				s.generalBasics(c, p)
			}
		})
	})
}

// settingsPageTransition 为分类、页签和服务表单提供短促的淡入位移，系统减少动态效果时自动简化。
func settingsPageTransition() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: 180 * time.Millisecond, Ease: ui.EaseOut, Colors: true,
		Enter: &ui.Motion{Y: 6}, Exit: &ui.Motion{Y: -3}, ExitDuration: 100 * time.Millisecond,
	}
}

// themeSegments 在等宽选项下绘制独立滑块，快速切换时从当前动画位置继续移动。
// 选项沿用原生分段控件的键盘与无障碍语义，首次显示直接定位到已保存的主题。
func (s *settingsUI) themeSegments(c *ui.Context, p pnPalette, selected *int, labels []string) ui.Element {
	segmentWidth := float32(76)
	if strings.HasPrefix(mygo.App.Locale(), "en") {
		segmentWidth = 94
	}
	parts := ui.SegmentedBase(c, selected, len(labels))
	track := parts.Track.Height(32).Padding(3).Radius(9).Background(p.Group).Border(.5, p.FieldBorder).Label(tr("主题风格"))
	position := track.AnimateTo("selection", float32(*selected), ui.Animation{Duration: 220 * time.Millisecond, Ease: ui.EaseOut})
	track.Children(func() {
		ui.Box(c.Key("thumb")).Absolute().Left(3).Top(3).Size(segmentWidth, 26).Radius(6).
			Background(p.Selected).Border(.5, p.FieldBorder).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, .1)).TranslatePercent(position*100, 0)
		for i, label := range labels {
			// 绝对定位的滑块默认绘制在普通内容之上，选项需提高层级以免文字被遮挡。
			segment := parts.Segment(i).Width(segmentWidth).Height(26).Radius(6).FontSize(12.5).TextColor(p.Muted).ZIndex(1)
			if i == *selected {
				segment.TextColor(p.Accent)
			} else if segment.Hovered() {
				segment.Background(p.Hover)
			}
			segment.Children(func() { ui.Text(c, label).SingleLine() })
		}
	})
	return track
}

// generalBasics 将主题与语言、启动与托盘分别收拢到两张卡片中。
func (s *settingsUI) generalBasics(c *ui.Context, p pnPalette) {
	s.card(c, p, "主题风格", func() {
		s.row(c, p, "主题风格", "选择明亮、黑夜或跟随系统，修改后立即生效。", func() {
			modes := []string{"light", "dark", "system"}
			labels := []string{tr("明亮"), tr("黑夜"), tr("跟随系统")}
			chosen := 2
			for i, mode := range modes {
				if mode == s.draft.ThemeMode {
					chosen = i
				}
			}
			if s.themeSegments(c.Key("theme-mode"), p, &chosen, labels).Changed() {
				s.draft.ThemeMode = modes[chosen]
			}
		})
		s.row(c, p, "界面语言", "选择应用显示语言，修改后立即生效。", func() {
			codes := []string{"", "zh-Hans", "zh-Hant", "en"}
			names := []string{tr("跟随系统"), "简体中文", "繁體中文", "English"}
			chosen := names[0]
			for i, code := range codes {
				if code == s.draft.UILanguage {
					chosen = names[i]
				}
			}
			if ui.Select(c.Key("ui-language"), &chosen, names).Width(160).Height(28).Changed() {
				for i, name := range names {
					if chosen == name {
						s.draft.UILanguage = codes[i]
						break
					}
				}
			}
		})
	}, "界面语言")
	s.card(c, p, "启动", func() {
		s.row(c, p, "开机自启", "登录系统后自动启动 Glide，并在托盘后台运行。", func() {
			if ui.Switch(c, &s.openAtLogin).Label(tr("开机自启")).Size(38, 22).Disabled(s.loginBusy).Changed() {
				s.setOpenAtLogin(s.openAtLogin)
			}
		})
		s.row(c, p, "左键点击托盘显示设置面板", "", func() {
			ui.Switch(c, &s.draft.TrayClickShowsPanel).Label(tr("左键点击托盘显示设置面板")).Size(38, 22)
		})
	}, "托盘")
}

// generalTranslation 展示取词方式、默认翻译语言和历史保留偏好。
func (s *settingsUI) generalTranslation(c *ui.Context, p pnPalette) {
	s.card(c, p, "取词方式", func() {
		detail := "划选文字后自动显示翻译；关闭时使用快捷键触发。"
		if !s.state.Platform.AutomaticSelectionSupported {
			detail = "当前系统暂不支持自动划词，可以使用快捷键取词。"
		}
		s.row(c, p, "自动划词翻译", detail, func() {
			ui.Switch(c, &s.draft.AutoSelection).Label(tr("自动划词翻译")).Size(38, 22).Disabled(!s.state.Platform.AutomaticSelectionSupported)
		})
	})
	s.card(c, p, "默认语言", func() {
		s.row(c, p, "源语言", "识别选中文字所使用的语言。", func() { pnLanguageSelect(c, &s.draft.SourceLang, true, 144, false) })
		s.row(c, p, "目标语言", "智能互译：中文译为英文，其他语言译为中文；也可指定目标语言。", func() { pnLanguageSelect(c, &s.draft.TargetLang, false, 144, false) })
	})
	s.card(c, p, "翻译历史", func() {
		s.row(c, p, "保留条数", "仅保留最近的记录；选择“不保留”会清空已有历史，并停止记录。", func() {
			chosen := fmt.Sprintf("%d 条", s.draft.HistoryLimit)
			if s.draft.HistoryLimit == 0 {
				chosen = "不保留"
			}
			if localizedSelect(c, &chosen, []string{"不保留", "10 条", "50 条", "100 条"}).Width(100).Height(28).FontSize(12.5).Changed() {
				switch chosen {
				case "不保留":
					s.draft.HistoryLimit = 0
				case "10 条":
					s.draft.HistoryLimit = 10
				case "50 条":
					s.draft.HistoryLimit = 50
				case "100 条":
					s.draft.HistoryLimit = 100
				}
			}
		})
	})
}

// generalPanel 展示翻译浮窗的位置与内容选项。
func (s *settingsUI) generalPanel(c *ui.Context, p pnPalette) {
	s.card(c, p, "翻译浮窗", func() {
		s.row(c, p, "打开翻译浮窗", "也可以直接输入原文进行翻译。", func() {
			ui.Button(c, tr("打开浮窗")).Height(28).FontSize(12.5).OnClick(s.app.OpenPanel)
		})
		s.row(c, p, "浮窗随鼠标显示", "开启后在鼠标附近弹出；关闭时在鼠标所在屏幕居中显示。固定的浮窗保留当前位置。", func() { ui.Switch(c, &s.draft.PanelFollowsMouse).Label(tr("浮窗随鼠标显示")).Size(38, 22) })
		s.row(c, p, "显示原文输入框", "允许在浮窗中修改原文并重新翻译。", func() { ui.Switch(c, &s.draft.ShowSourceInput).Label(tr("显示原文输入框")).Size(38, 22) })
		s.row(c, p, "显示语言栏", "在浮窗中选择源语言和目标语言。", func() { ui.Switch(c, &s.draft.ShowLanguageBar).Label(tr("显示语言栏")).Size(38, 22) })
	})
}

// generalPermissions 展示平台取词权限及授权操作。
func (s *settingsUI) generalPermissions(c *ui.Context, p pnPalette) {
	s.card(c, p, "系统权限", func() {
		ui.Column(c).Padding(10, 0).Gap(9).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				color, title := p.Accent, "跨应用取词已获授权"
				if !s.state.Platform.PermissionGranted {
					color, title = p.Attention, "需要允许跨应用取词"
				}
				pnIcon(c, "shield", 17, color)
				ui.Text(c, tr(title)).FontSize(13.5)
			})
			ui.Text(c, tr(s.state.Platform.Message)).FontSize(12).TextColor(p.Muted).LineHeight(1.4)
			ui.Row(c).Gap(8).Children(func() {
				if s.state.Platform.OS == "darwin" {
					ui.Button(c, tr("打开辅助功能设置")).Height(28).FontSize(12.5).OnClick(func() { s.externalAction(s.app.OpenSelectionSettings) })
				}
				ui.Button(c, tr("刷新权限")).Height(28).FontSize(12.5).OnClick(s.refreshPermission)
			})
		})
	})
}

// services 用独立列表和凭据表单配置翻译服务，支持同类多个实例。
func (s *settingsUI) services(c *ui.Context, p pnPalette) {
	ui.Row(c).Grow(1).MinWidth(0).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Width(188).Shrink(0).FillHeight().Padding(12, 0, 12, 12).Gap(8).Children(func() {
			if len(s.draft.Providers) == 0 {
				ui.Text(c, tr("尚未配置服务")).Grow(1).FontSize(12).TextColor(p.Muted).Padding(12, 4)
			} else {
				ui.List(c.Key("services-list"), &s.providerList, len(s.draft.Providers)).Grow(1).FillWidth().Gap(3).Rows(func(row ui.ListRow) {
					provider := &s.draft.Providers[row.Index]
					id := provider.ID
					ui.Row(row.Context.Key(provider.ID)).Height(38).Padding(5, 5).Gap(5).Radius(8).Background(s.serviceBackground(p, provider.ID)).Children(func() {
						ui.Box(c).Size(8, 20).Shrink(0).Tooltip(tr("拖动调整服务顺序")).Draw(func(painter *ui.Painter, rect ui.Rect) {
							for y := 0; y < 3; y++ {
								for x := 0; x < 2; x++ {
									painter.Fill(ui.Rect{X: rect.X + 1 + float32(x)*4, Y: rect.Y + 5 + float32(y)*4, W: 2, H: 2}, p.Faint, 1)
								}
							}
						})
						ui.ButtonBase(c).Grow(1).MinWidth(0).Gap(5).Justify(ui.Start).AlignItems(ui.Center).Tooltip(provider.Name).Children(func() {
							ui.Box(c).Size(22, 22).Shrink(0).Radius(6).Background(p.Surface).Center().Children(func() { pnProviderIcon(c, providerIconKind(*provider), 17, p.Text) })
							ui.Text(c, providerDisplayName(provider.Name)).Grow(1).MinWidth(0).FontSize(12).SingleLine().TextAlign(ui.Start)
						}).OnClick(func() { s.providerID = id })
						toggle := ui.Switch(c, &provider.Enabled).Size(28, 17).Shrink(0).Label(tr("启用" + provider.Name))
						// 开关保留自己的指针操作，拖动时不会被列表接管为排序。
						toggle.PressedPointer()
					}).ContextMenu(func(menu *ui.Menu) {
						s.providerID = id
						if menu.Item(tr("复制当前服务")).Disabled(s.saving || len(s.draft.Providers) >= 24).Chosen() {
							s.duplicateProvider(id)
						}
						if menu.Item(tr("删除")).Disabled(s.saving).Chosen() {
							s.removeProvider()
						}
					})
				})
			}
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				ui.MenuButton(c, tr("添加服务"), func(menu *ui.Menu) {
					for _, definition := range s.state.Definitions {
						if menu.Item(tr(definition.Name)).Chosen() {
							s.addProvider(definition)
						}
					}
				}).Grow(1).Height(29).AlignItems(ui.Center).FontSize(12)
				ui.ButtonBase(c).Size(29, 29).Center().Radius(7).Border(.5, p.FieldBorder).Label(tr("删除选中服务")).Tooltip(tr("删除选中服务")).Disabled(s.providerID == "").Children(func() { pnIcon(c, "trash", 14, p.Muted) }).OnClick(s.removeProvider)
			})
		})
		ui.Box(c).Width(1).Shrink(0).FillHeight().Margin(12, 0).Background(p.FieldBorder)
		ui.Scroll(c.Key("service-editor")).Grow(1).MinWidth(0).FillHeight().Gap(14).Padding(16, 14, 16, 0).Children(func() {
			var selected *translation.Config
			for i := range s.draft.Providers {
				if s.draft.Providers[i].ID == s.providerID {
					selected = &s.draft.Providers[i]
					break
				}
			}
			if selected == nil {
				ui.Text(c, tr("选择一个服务，设置连接方式。")).FontSize(13).TextColor(p.Muted).Padding(30, 0)
				s.inlineNotice(c, p, "服务配置")
				return
			}
			ui.Column(c.Key(selected.ID)).Shrink(0).Gap(14).Transition(settingsPageTransition()).Children(func() {
				ui.Row(c).Gap(9).Children(func() {
					ui.Box(c).Size(32, 32).Shrink(0).Radius(9).Background(p.Group).Center().Children(func() { pnProviderIcon(c, providerIconKind(*selected), 23, p.Text) })
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, providerDisplayName(selected.Name)).FontSize(15).FontWeight(650)
					})
				})
				s.field(c, p, "服务名称", &selected.Name, "为此服务起个名字", false)
				s.serviceIconPicker(c, p, "services", selected.ID, providerIconKind(*selected))
				if selected.Kind == "openai" {
					s.aiFormatField(c, p, selected)
				}
				for _, definition := range s.state.Definitions {
					if definition.Kind != selected.Kind {
						continue
					}
					for _, field := range definition.Fields {
						if selected.Kind == "openai" && field.Key == "model" {
							s.aiModelField(c, p, selected)
							continue
						}
						value := s.providerField(selected, field.Key)
						if value == nil {
							continue
						}
						label := tr(field.Label)
						if field.Required {
							label += " *"
						}
						s.field(c, p, label, value, field.Placeholder, field.Secret)
					}
				}
				if selected.Kind == "openai" {
					s.aiConnectionTest(c, p, selected)
				}
				if selected.Kind == "google" || selected.Kind == "bing" {
					ui.Text(c, tr("内置服务无需密钥，可用性受网络及服务调整影响。遇到限制时，可以改用官方 API。")).FontSize(12).TextColor(p.Muted).LineHeight(1.4)
				}
				if selected.Kind == "deepl" {
					ui.Text(c, tr("服务地址留空时自动选择 Free 或 Pro。也可填写域名、/v2 基础地址或完整翻译接口地址。")).FontSize(12).TextColor(p.Muted).LineHeight(1.4)
				}
				if selected.Kind == "deeplx" {
					ui.Text(c, tr("请先启动 DeepLX 服务。地址可填写域名或完整 /translate 接口地址；访问令牌按需填写。")).FontSize(12).TextColor(p.Muted).LineHeight(1.4)
				}
				if selected.Kind == "ollama" {
					ui.Text(c, tr("请先启动 Ollama，填写已下载的模型名称。可以连接本机或自定义服务地址。")).FontSize(12).TextColor(p.Muted).LineHeight(1.4)
				}
				url := s.serviceDocs(selected.Kind)
				ui.Link(c, tr("查看服务说明 ↗"), "").AlignSelf(ui.Start).FontSize(12).TextColor(p.Accent).OnClick(func() { s.externalAction(func() error { return s.app.OpenExternal(url) }) })
				s.inlineNotice(c, p, "服务配置")
			})
		})
	})
}

// serviceBackground 用浅色填充标记选中服务，保持正文为中性色。
func (s *settingsUI) serviceBackground(p pnPalette, id string) ui.Color {
	if id == s.providerID {
		return p.Group
	}
	return ui.Transparent
}

// field 统一表单样式，密钥默认隐藏，可通过眼睛按钮切换可见性。
func (s *settingsUI) field(c *ui.Context, p pnPalette, label string, value *string, placeholder string, secret bool) {
	providerID := s.providerID
	if s.category == "ocr" {
		providerID = s.ocrProviderID
	}
	key := s.category + ":" + providerID + ":" + label
	ui.Column(c.Key(label)).Shrink(0).Gap(6).Children(func() {
		ui.Text(c, tr(label)).FontSize(12).TextColor(p.Muted)
		ui.Row(c).Gap(6).Children(func() {
			input := ui.TextInput(c, value).Placeholder(tr(placeholder)).Label(tr(label)).Height(28).Radius(7).FontSize(12.5).Grow(1).MinWidth(0)
			if secret {
				visible := s.visibleSecrets[key]
				if !visible {
					input.Password()
				}
				icon, action := "eye", tr("显示")+label
				if visible {
					icon, action = "eye-off", tr("隐藏")+label
				}
				ui.ButtonBase(c.Key("visibility")).Size(28, 28).Radius(6).Center().Label(tr(action)).Tooltip(tr(action)).Children(func() {
					pnIcon(c, icon, 15, p.Muted)
				}).OnClick(func() { s.visibleSecrets[key] = !visible })
			}
		})
	})
}

// providerField 将声明式渠道字段映射到强类型配置，不允许写入未知字段。
func (s *settingsUI) providerField(provider *translation.Config, key string) *string {
	switch key {
	case "endpoint":
		return &provider.Endpoint
	case "apiKey":
		return &provider.APIKey
	case "appId":
		return &provider.AppID
	case "secretId":
		return &provider.SecretID
	case "secretKey":
		return &provider.SecretKey
	case "region":
		return &provider.Region
	case "model":
		return &provider.Model
	}
	return nil
}

// shortcut 提供全局和浮窗操作的快捷键录入。
func (s *settingsUI) shortcut(c *ui.Context, p pnPalette) {
	if s.state.HotkeyError != s.lastHotkeyError {
		s.lastHotkeyError = s.state.HotkeyError
		var err error
		if s.lastHotkeyError != "" {
			err = fmt.Errorf("%s", s.lastHotkeyError)
		}
		s.showNotice("shortcut/全局快捷键", "", err)
	}
	ui.Scroll(c).Grow(1).FillWidth().Padding(18).Gap(17).Children(func() {
		s.group(c, p, "全局快捷键", func() {
			s.shortcutRow(c, p, "翻译选中文字", &s.draft.Shortcut, false)
			s.shortcutRow(c, p, "输入翻译", &s.draft.InputShortcut, false)
			s.shortcutRow(c, p, "剪贴板翻译", &s.draft.ClipboardShortcut, false)
			s.shortcutRow(c, p, "截图 OCR 翻译", &s.draft.OCRShortcut, false)
		})
		s.group(c, p, "翻译浮窗", func() {
			s.shortcutRow(c, p, "重新翻译", &s.draft.PanelTranslateShortcut, true)
			s.shortcutRow(c, p, "关闭浮窗", &s.draft.PanelCloseShortcut, true)
		})
		ui.Text(c, tr("点击录入快捷键，设置后自动生效。全局录入按 Esc 取消；浮窗支持 Enter、Esc 等按键，仅在浮窗中生效。")).FontSize(12).TextColor(p.Muted)
	})
}

// shortcutLabel 在 macOS 使用系统按键符号，在 Windows 使用文字组合。
func (s *settingsUI) shortcutLabel(shortcut string) string {
	if s.state.Platform.OS == "darwin" {
		return strings.NewReplacer("CmdOrCtrl", "⌘", "Command", "⌘", "Cmd", "⌘", "Control", "⌃", "Ctrl", "⌃", "Shift", "⇧", "Option", "⌥", "Alt", "⌥", "Enter", "↩", "Escape", "⎋", "+", " ").Replace(shortcut)
	}
	return strings.ReplaceAll(shortcut, "CmdOrCtrl", "Ctrl")
}

// shortcutRow 录入组合键；panel 允许无修饰键的特殊按键，已设置时提供清除按钮。
func (s *settingsUI) shortcutRow(c *ui.Context, p pnPalette, title string, value *string, panel bool) {
	s.row(c, p, title, "", func() {
		border := p.FieldBorder
		if s.recordingShortcut == title {
			border = p.Accent
		}
		ui.Row(c.Key(title+"-field")).Width(184).Height(32).Shrink(0).Padding(3, 6).Gap(4).AlignItems(ui.Center).
			Background(p.Surface).Radius(8).Border(.5, border).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, .025)).Children(func() {
			label := s.shortcutLabel(*value)
			if label == "" {
				label = "未设置"
			}
			if s.recordingShortcut == title {
				label = "请按快捷键…"
			}
			color := p.Text
			if *value == "" {
				color = p.Faint
			}
			if s.recordingShortcut == title {
				color = p.Accent
			}
			button := ui.ButtonBase(c.Key(title)).Grow(1).MinWidth(0).FillHeight().Radius(5).Padding(0, 5).Justify(ui.Start).AlignItems(ui.Center).Label(tr(title + "快捷键")).Children(func() {
				ui.Text(c, tr(label)).FontSize(12.5).TextColor(color).SingleLine()
			})
			button.OnClick(func() { s.recordingShortcut = title })
			button.HandleInput(func(event ui.InputEvent) bool {
				if s.recordingShortcut != title || event.Kind != ui.InputKeyDown {
					return false
				}
				if !panel && event.Key == ui.KeyEscape {
					s.recordingShortcut = ""
					return true
				}
				key, mods := event.Key, event.Mods
				letter := panelShortcutKeyName(key)
				if !panel && !((key >= ui.KeyA && key <= ui.KeyZ) || (key >= ui.Key0 && key <= ui.Key9)) {
					return true
				}
				if letter == "" || !panelShortcutAllowed(mods, key) || (!panel && mods&(ui.Ctrl|ui.Super|ui.Alt) == 0) {
					return true
				}
				parts := []string{}
				if mods&ui.Ctrl != 0 {
					parts = append(parts, "Ctrl")
				}
				if mods&ui.Alt != 0 {
					parts = append(parts, "Alt")
				}
				if mods&ui.Shift != 0 {
					parts = append(parts, "Shift")
				}
				if mods&ui.Super != 0 {
					modifier := "Command"
					if s.state.Platform.OS == "windows" {
						modifier = "Win"
					}
					parts = append(parts, modifier)
				}
				*value = strings.Join(append(parts, letter), "+")
				s.recordingShortcut = ""
				return true
			})
			if *value != "" {
				clear := ui.ButtonBase(c.Key(title+"-clear")).Size(20, 20).Shrink(0).Center().Radius(10).Label(tr("清除" + title + "快捷键")).Tooltip(tr("清除快捷键"))
				if clear.Hovered() {
					clear.Background(p.Hover)
				}
				clear.Children(func() { pnIcon(c, "close", 12, p.Muted) }).OnClick(func() {
					s.confirmDeletion("清除快捷键", tr("确定清除“")+title+tr("”的快捷键？"), func() { *value = ""; s.recordingShortcut = "" })
				})
			}
		})
	})
}

// historyView 提供本机历史查询和展开阅读，查看记录不会重新发送翻译请求。
func (s *settingsUI) historyView(c *ui.Context, p pnPalette) {
	ui.Column(c).Grow(1).FillWidth().Padding(18).Gap(13).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			ui.SearchField(c, &s.historyQuery).Placeholder(tr("搜索原文或译文")).Grow(1).Height(30)
			ui.Button(c, tr("刷新")).Height(30).Disabled(s.loadingHistory || s.saving).OnClick(s.loadHistory)
			label := "清空历史"
			if s.clearConfirm {
				label = "确认清空"
			}
			ui.Button(c, tr(label)).Height(30).TextColor(p.Muted).Disabled(s.saving || s.loadingHistory || len(s.history) == 0).OnClick(s.clearHistory)
		})
		s.inlineNotice(c, p, "翻译历史")
		ui.Scroll(c.Key("history-scroll")).Grow(1).FillWidth().Gap(12).Children(func() {
			if s.loadingHistory {
				ui.Text(c, tr("正在读取历史…")).FontSize(12).TextColor(p.Muted)
			}
			shown := 0
			for _, record := range s.history {
				query := strings.ToLower(strings.TrimSpace(s.historyQuery))
				combined := record.Text
				for _, result := range record.Results {
					combined += " " + result.Text
				}
				if query != "" && !strings.Contains(strings.ToLower(combined), query) {
					continue
				}
				shown++
				record := record
				ui.Column(c.Key(record.RequestID)).Background(p.Group).Radius(12).Padding(14).Gap(10).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.ButtonBase(c).Grow(1).MinWidth(0).Gap(8).Justify(ui.Start).Children(func() {
							icon := "chevron-right"
							if s.expanded[record.RequestID] {
								icon = "chevron-down"
							}
							pnIcon(c, icon, 14, p.Muted)
							ui.Text(c, record.Text).Grow(1).FontSize(13).MaxLines(2)
							ui.Text(c, tr(s.historyDate(record.CreatedAt))).FontSize(11).TextColor(p.Muted)
						}).OnClick(func() { s.expanded[record.RequestID] = !s.expanded[record.RequestID] })
						ui.ButtonBase(c.Key("delete-history")).Size(24, 24).Center().Radius(6).Label(tr("删除记录")).Tooltip(tr("删除记录")).Disabled(s.saving || s.loadingHistory).Children(func() {
							pnIcon(c, "close", 14, p.Muted)
						}).OnClick(func() { s.deleteHistory(record.RequestID) })
					})
					if s.expanded[record.RequestID] {
						ui.Text(c, tr(pnLanguageName(record.SourceLang)+" → "+pnLanguageName(record.TargetLang))).FontSize(11).TextColor(p.Muted)
						for _, result := range record.Results {
							result := result
							ui.Column(c).Gap(7).Padding(10, 0).BorderWidth(.5, 0, 0, 0).BorderColor(p.Divider).Children(func() {
								ui.Row(c).Children(func() {
									ui.Text(c, tr(result.ProviderName)).FontSize(12).FontWeight(600).Grow(1)
									if result.Text != "" {
										ui.Button(c, tr("复制")).Height(25).FontSize(11).OnClick(func() { s.app.CopyText(result.Text); s.showNotice("history/翻译历史", "译文已复制", nil) })
									}
								})
								if result.Error != "" {
									ui.Text(c, tr(result.Error)).FontSize(12).TextColor(p.Danger).Selectable()
								} else {
									ui.Text(c, result.Text).FontSize(13).LineHeight(1.5).Selectable()
								}
							})
						}
					} else {
						for _, result := range record.Results {
							if result.Text != "" {
								ui.Text(c, result.Text).FontSize(12).TextColor(p.Muted).MaxLines(2)
								break
							}
						}
					}
				})
			}
			if shown == 0 && !s.loadingHistory {
				ui.Text(c, tr("暂无匹配的翻译记录")).FontSize(13).TextColor(p.Muted).Padding(35, 0)
			}
		})
	})
}

// privacyContent 在关于页下方说明数据存储与发送范围，并保留历史清空入口。
func (s *settingsUI) privacyContent(c *ui.Context, p pnPalette) {
	ui.Column(c).Shrink(0).FillWidth().Gap(10).Children(func() {
		s.group(c, p, "隐私与数据", func() {
			s.row(c, p, "服务密钥", "密钥保存在本机 settings.json 中；导出或上传配置时会包含服务密钥。", func() {})
			s.row(c, p, "翻译请求", "原文只发送至当前启用的翻译服务。Ollama 使用本机地址时，翻译请求无需离开本机。", func() {})
			s.row(c, p, "OCR 图片", "系统 OCR 在本机识别；云端 OCR 会将图片发送至所选厂商，识别文字再发送至已启用的翻译服务。", func() {})
			detail := fmt.Sprintf(tr("最多保留 %d 条记录，存储在本机。"), s.state.Settings.HistoryLimit)
			if s.state.Settings.HistoryLimit == 0 {
				detail = "当前不保留翻译历史。"
			}
			s.row(c, p, "翻译历史", detail, func() {
				label := "清空历史"
				if s.clearConfirm {
					label = "确认清空"
				}
				ui.Button(c, tr(label)).Height(29).FontSize(12.5).TextColor(p.Muted).OnClick(s.clearHistory)
			})
		})
		if s.clearConfirm {
			ui.Text(c, tr("再次点击“确认清空”将删除本机全部翻译记录。")).FontSize(12).TextColor(p.Attention)
		}
	})
}

// about 展示应用版本、项目地址与签名更新状态。
func (s *settingsUI) about(c *ui.Context, p pnPalette) {
	ui.Scroll(c).Grow(1).FillWidth().Padding(18).Gap(17).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(8).Padding(18, 0, 12).Children(func() {
			pnBrandLogo(c, 58).Shadow(0, 2, 5, 0, ui.RGBA(0, 0, 0, .20))
			ui.Text(c, "Glide").FontSize(22).FontWeight(650)
			ui.Text(c, tr("划词翻译")).FontSize(13).TextColor(p.Muted)
		})
		s.group(c, p, "应用信息", func() {
			s.row(c, p, "当前版本", "", func() { ui.Text(c, tr(s.state.Version)).FontSize(13).TextColor(p.Muted) })
			s.row(c, p, "开源仓库", "", func() {
				ui.Link(c, "https://github.com/anghunk/glide", "https://github.com/anghunk/glide").FontSize(12).TextColor(p.Accent)
			})
			s.row(c, p, "问题反馈", "", func() {
				ui.Link(c, "https://github.com/anghunk/glide/issues", "https://github.com/anghunk/glide/issues").FontSize(12).TextColor(p.Accent)
			})
		})
		s.app.renderUpdateSettings(c, p)
		s.privacyContent(c, p)
	})
}

// historyDate 将历史时间显示为便于扫描的本地时间。
func (s *settingsUI) historyDate(value string) string {
	if date, err := time.Parse(time.RFC3339, value); err == nil {
		return date.Local().Format(tr("01月02日 15:04"))
	}
	return value
}

// serviceDocs 返回选中渠道的官方说明入口。
func (s *settingsUI) serviceDocs(kind string) string {
	switch kind {
	case "google":
		return "https://translate.google.com/"
	case "google-cloud":
		return "https://cloud.google.com/translate/docs"
	case "bing":
		return "https://www.bing.com/translator"
	case "azure":
		return "https://learn.microsoft.com/azure/ai-services/translator/"
	case "tencent":
		return "https://cloud.tencent.com/product/tmt"
	case "baidu":
		return "https://api.fanyi.baidu.com/"
	case "deepl":
		return "https://developers.deepl.com/docs"
	case "deeplx":
		return "https://github.com/OwO-Network/DLX"
	case "ollama":
		return "https://ollama.com/"
	}
	return "https://platform.openai.com/api-keys"
}

// deleteHistory 确认后删除指定记录，避免误触关闭图标造成数据丢失。
func (s *settingsUI) deleteHistory(requestID string) {
	if s.saving || s.loadingHistory {
		return
	}
	s.confirmDeletion("删除翻译记录", "确定删除这条翻译记录？", func() { s.deleteHistoryConfirmed(requestID) })
}

// deleteHistoryConfirmed 在后台执行已确认的删除，失败时保留当前内容。
func (s *settingsUI) deleteHistoryConfirmed(requestID string) {
	if s.saving || s.loadingHistory {
		return
	}
	s.saving = true
	scope := "history/翻译历史"
	s.showNotice(scope, "", nil)
	go func() {
		err := s.app.DeleteHistory(requestID)
		items := s.app.GetHistory()
		s.app.mainWindow.Update(func() {
			s.saving = false
			if err != nil {
				s.showNotice(scope, "", err)
				return
			}
			s.history = items
			delete(s.expanded, requestID)
			s.clearConfirm = false
		})
	}()
}

// confirmDeletion 使用系统弹窗确认删除；取消为默认操作，关闭弹窗不执行删除。
func (s *settingsUI) confirmDeletion(title, message string, confirmed func()) {
	if s.confirmingDelete || s.saving {
		return
	}
	s.confirmingDelete = true
	scope := s.operationScope()
	go func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: s.app.mainWindow, Type: mygo.MessageWarning, Title: tr(title), Message: tr(message),
			Buttons: []string{tr("取消"), tr("确认")}, DefaultButton: 0, CancelButton: 0, DestructiveButtons: []int{1},
		})
		s.app.mainWindow.Update(func() {
			s.confirmingDelete = false
			if err != nil {
				s.showNotice(scope, "", err)
				return
			}
			if result.Button == 1 {
				confirmed()
			}
		})
	}()
}

// mergeSettingsDraft 只合并相对基线有改动的字段，保留外部语言切换等并行修改。
func mergeSettingsDraft(baseline, edited, latest Settings) Settings {
	if edited.ThemeMode == baseline.ThemeMode {
		edited.ThemeMode = latest.ThemeMode
	}
	if edited.UILanguage == baseline.UILanguage {
		edited.UILanguage = latest.UILanguage
	}
	if edited.ProxyAddress == baseline.ProxyAddress {
		edited.ProxyAddress = latest.ProxyAddress
	}
	if edited.UseSystemProxy == baseline.UseSystemProxy {
		edited.UseSystemProxy = latest.UseSystemProxy
	}
	if edited.PanelFollowsMouse == baseline.PanelFollowsMouse {
		edited.PanelFollowsMouse = latest.PanelFollowsMouse
	}
	if edited.PanelTranslateShortcut == baseline.PanelTranslateShortcut {
		edited.PanelTranslateShortcut = latest.PanelTranslateShortcut
	}
	if edited.PanelCloseShortcut == baseline.PanelCloseShortcut {
		edited.PanelCloseShortcut = latest.PanelCloseShortcut
	}
	if edited.OCRShortcut == baseline.OCRShortcut {
		edited.OCRShortcut = latest.OCRShortcut
	}
	if edited.WebDAV == baseline.WebDAV {
		edited.WebDAV = latest.WebDAV
	}
	if edited.TrayClickShowsPanel == baseline.TrayClickShowsPanel {
		edited.TrayClickShowsPanel = latest.TrayClickShowsPanel
	}
	if edited.SourceLang == baseline.SourceLang {
		edited.SourceLang = latest.SourceLang
	}
	if edited.TargetLang == baseline.TargetLang {
		edited.TargetLang = latest.TargetLang
	}
	if edited.InputShortcut == baseline.InputShortcut {
		edited.InputShortcut = latest.InputShortcut
	}
	if edited.ClipboardShortcut == baseline.ClipboardShortcut {
		edited.ClipboardShortcut = latest.ClipboardShortcut
	}
	if edited.HistoryLimit == baseline.HistoryLimit {
		edited.HistoryLimit = latest.HistoryLimit
	}
	if edited.Shortcut == baseline.Shortcut {
		edited.Shortcut = latest.Shortcut
	}
	if edited.AutoSelection == baseline.AutoSelection {
		edited.AutoSelection = latest.AutoSelection
	}
	if edited.ShowSourceInput == baseline.ShowSourceInput {
		edited.ShowSourceInput = latest.ShowSourceInput
	}
	if edited.ShowLanguageBar == baseline.ShowLanguageBar {
		edited.ShowLanguageBar = latest.ShowLanguageBar
	}
	if reflect.DeepEqual(edited.Providers, baseline.Providers) {
		edited.Providers = latest.Providers
	}
	if reflect.DeepEqual(edited.OCRProviders, baseline.OCRProviders) {
		edited.OCRProviders = latest.OCRProviders
	}
	return cloneSettings(edited)
}
