package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"glide/internal/selection"
	"glide/internal/translation"
)

// Settings 描述语言、触发方式及各翻译渠道的独立配置。
type Settings struct {
	ThemeMode              string               `json:"themeMode"`
	UILanguage             string               `json:"uiLanguage"`
	ProxyAddress           string               `json:"proxyAddress"`
	UseSystemProxy         bool                 `json:"useSystemProxy"`
	PanelTranslateShortcut string               `json:"panelTranslateShortcut"`
	PanelCloseShortcut     string               `json:"panelCloseShortcut"`
	PanelFollowsMouse      bool                 `json:"panelFollowsMouse"`
	OCRShortcut            string               `json:"ocrShortcut"`
	WebDAV                 WebDAVConfig         `json:"webdav"`
	TrayClickShowsPanel    bool                 `json:"trayClickShowsPanel"`
	InputShortcut          string               `json:"inputShortcut"`
	ClipboardShortcut      string               `json:"clipboardShortcut"`
	HistoryLimit           int                  `json:"historyLimit"`
	SourceLang             string               `json:"sourceLang"`
	TargetLang             string               `json:"targetLang"`
	Shortcut               string               `json:"shortcut"`
	AutoSelection          bool                 `json:"autoSelection"`
	ShowSourceInput        bool                 `json:"showSourceInput"`
	ShowLanguageBar        bool                 `json:"showLanguageBar"`
	Providers              []translation.Config `json:"providers"`
}

// PlatformState 向设置界面报告真实的取词支持及权限状态。
type PlatformState struct {
	OS                          string `json:"os"`
	SelectionSupported          bool   `json:"selectionSupported"`
	AutomaticSelectionSupported bool   `json:"automaticSelectionSupported"`
	PermissionGranted           bool   `json:"permissionGranted"`
	Message                     string `json:"message"`
}

// AppState 是原生设置界面使用的应用快照。
type AppState struct {
	Settings    Settings                 `json:"settings"`
	Definitions []translation.Definition `json:"definitions"`
	Platform    PlatformState            `json:"platform"`
	Version     string                   `json:"version"`
	HotkeyError string                   `json:"hotkeyError"`
}

// TranslateRequest 可指定部分渠道；未指定时使用设置中启用的渠道。
type TranslateRequest struct {
	Text        string   `json:"text"`
	SourceLang  string   `json:"sourceLang"`
	TargetLang  string   `json:"targetLang"`
	ProviderIDs []string `json:"providerIds"`
}

// ClipboardRequest 仅携带本次渠道和语言，原文由原生服务在点击时读取。
type ClipboardRequest struct {
	SourceLang  string   `json:"sourceLang"`
	TargetLang  string   `json:"targetLang"`
	ProviderIDs []string `json:"providerIds"`
}

// ChannelResult 逐渠道携带成功结果或错误，单个渠道失败不会阻断其他渠道。
type ChannelResult struct {
	ProviderID   string `json:"providerId"`
	ProviderName string `json:"providerName"`
	Text         string `json:"text"`
	DetectedLang string `json:"detectedLang"`
	DurationMS   int64  `json:"durationMs"`
	Error        string `json:"error"`
	Pending      bool   `json:"pending"`
}

// TranslationResponse 同时用于翻译结果和最多保留一百条的本地历史。
type TranslationResponse struct {
	RequestID  string          `json:"requestId"`
	Text       string          `json:"text"`
	SourceLang string          `json:"sourceLang"`
	TargetLang string          `json:"targetLang"`
	TargetMode string          `json:"targetMode,omitempty"`
	Results    []ChannelResult `json:"results"`
	CreatedAt  string          `json:"createdAt"`
}

// PanelState 使重复使用的浮窗能读取最新一批翻译，避免过期请求覆盖新选区。
type PanelState struct {
	Pinned   bool                 `json:"pinned"`
	Loading  bool                 `json:"loading"`
	Response *TranslationResponse `json:"response"`
	Error    string               `json:"error"`
}

// GlideService 连接原生界面、系统取词及翻译渠道。
type GlideService struct {
	ocrBusy         bool
	ocrCancel       context.CancelFunc
	mu              sync.RWMutex
	settingsMu      sync.Mutex
	triggersMu      sync.Mutex
	historyMu       sync.Mutex
	settings        Settings
	history         []TranslationResponse
	dir             string
	mainWindow      *mygo.Window
	panelWindow     *mygo.Window
	settingsUI      *settingsUI
	panelUI         *panelUI
	trayMenu        *nativeTrayMenu
	updater         *appUpdater
	panel           PanelState
	panelCancel     context.CancelFunc
	panelRetries    map[string]context.CancelFunc
	panelGeneration uint64
	watchCancel     context.CancelFunc
	watchStop       func()
	extraHotkeys    map[string]string
	hotkey          string
	hotkeyError     string
	watchError      string
	quitting        bool
	lastSelection   string
	lastSelectionAt time.Time
}

// defaultSettings 只默认启用无需付费凭据的渠道，自动划词由用户主动开启。
func defaultSettings() Settings {
	return Settings{ThemeMode: "system", UseSystemProxy: true, PanelTranslateShortcut: "CmdOrCtrl+Enter", PanelCloseShortcut: "Escape", OCRShortcut: "CmdOrCtrl+Shift+O", TrayClickShowsPanel: true, HistoryLimit: 50, SourceLang: "auto", TargetLang: "smart", Shortcut: "CmdOrCtrl+Shift+T", ShowSourceInput: true, ShowLanguageBar: true, Providers: translation.DefaultConfigs()}
}

// cloneSettings 复制渠道切片，避免界面快照与运行配置共用可变数据。
func cloneSettings(s Settings) Settings { s.Providers = slices.Clone(s.Providers); return s }

// newService 在窗口创建前读取本地配置，不会主动发起网络翻译。
func newService() (*GlideService, error) {
	if os.Getenv("MYGO_GENERATE") != "" {
		return &GlideService{settings: defaultSettings(), history: []TranslationResponse{}}, nil
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return nil, err
	}
	s, err := readSettings(dir)
	if err != nil {
		return nil, err
	}
	a := &GlideService{settings: s, dir: dir, history: []TranslationResponse{}}
	data, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if err == nil {
		if err := json.Unmarshal(data, &a.history); err != nil {
			return nil, fmt.Errorf("读取历史记录失败：%w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if len(a.history) > a.settings.HistoryLimit {
		a.history = a.history[:a.settings.HistoryLimit]
	}
	return a, nil
}

// GetState 返回设置与实时权限状态，密钥随本地配置读取，不发送给界面以外的管理服务。
func (a *GlideService) GetState() AppState {
	cap := selection.Status()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.watchError != "" {
		cap.Message += "；" + a.watchError
	}
	return AppState{Settings: cloneSettings(a.settings), Definitions: translation.Definitions(), Version: mygo.App.Version(), HotkeyError: a.hotkeyError,
		Platform: PlatformState{OS: runtime.GOOS, SelectionSupported: cap.Supported, AutomaticSelectionSupported: cap.AutomaticSupported, PermissionGranted: cap.PermissionGranted, Message: cap.Message}}
}

// SaveSettings 验证设置、保存密钥并更新快捷键及自动取词监听。
func (a *GlideService) SaveSettings(settings Settings) (AppState, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if settings.ThemeMode == "" {
		settings.ThemeMode = "system"
	}
	switch settings.ThemeMode {
	case "system", "light", "dark":
	default:
		return AppState{}, fmt.Errorf("%s", tr("不支持的主题风格"))
	}
	switch settings.UILanguage {
	case "", "zh-Hans", "zh-Hant", "en":
	default:
		return AppState{}, fmt.Errorf("不支持的界面语言")
	}
	if settings.HistoryLimit != 0 && settings.HistoryLimit != 10 && settings.HistoryLimit != 50 && settings.HistoryLimit != 100 {
		return AppState{}, fmt.Errorf("历史保留条数仅支持不保留、10、50 或 100")
	}
	if settings.SourceLang == "" {
		settings.SourceLang = "auto"
	}
	if settings.TargetLang == "" || settings.TargetLang == "auto" {
		return AppState{}, fmt.Errorf("请选择目标语言")
	}
	normalizedProxy, err := translation.NormalizeProxyAddress(settings.ProxyAddress)
	if err != nil {
		return AppState{}, err
	}
	settings.ProxyAddress = normalizedProxy
	if len(settings.Shortcut) > 100 {
		return AppState{}, fmt.Errorf("请输入有效快捷键")
	}
	seenShortcuts := map[string]bool{shortcutIdentity(settings.Shortcut): true}
	for _, shortcut := range []string{settings.InputShortcut, settings.ClipboardShortcut, settings.OCRShortcut} {
		if shortcut == "" {
			continue
		}
		key := shortcutIdentity(shortcut)
		if len(shortcut) > 100 || seenShortcuts[key] {
			return AppState{}, fmt.Errorf("快捷键重复或无效，请使用不同组合")
		}
		seenShortcuts[key] = true
	}
	for _, shortcut := range []string{settings.PanelTranslateShortcut, settings.PanelCloseShortcut} {
		if shortcut == "" {
			continue
		}
		if _, _, ok := parsePanelShortcut(shortcut); !ok {
			return AppState{}, fmt.Errorf("浮窗快捷键无效，请重新录入")
		}
		if seenShortcuts[shortcutIdentity(shortcut)] {
			return AppState{}, fmt.Errorf("快捷键重复，请使用不同组合")
		}
		seenShortcuts[shortcutIdentity(shortcut)] = true
	}
	if len(settings.Providers) > 24 {
		return AppState{}, fmt.Errorf("最多配置 24 个翻译渠道")
	}
	kinds := map[string]bool{}
	for _, d := range translation.Definitions() {
		kinds[d.Kind] = true
	}
	ids := map[string]bool{}
	for _, p := range settings.Providers {
		if p.ID == "" || len(p.ID) > 80 || ids[p.ID] || !kinds[p.Kind] {
			return AppState{}, fmt.Errorf("渠道标识重复或类型无效")
		}
		if p.Kind == "openai" {
			switch translation.AIFormat(p) {
			case "openai", "responses", "anthropic", "gemini":
			default:
				return AppState{}, fmt.Errorf("不支持的 AI 上游格式")
			}
		}
		ids[p.ID] = true
		if p.Endpoint != "" {
			u, err := url.Parse(p.Endpoint)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				return AppState{}, fmt.Errorf("%s 的服务地址无效", p.Name)
			}
		}
	}
	if err := writeSettings(a.dir, settings); err != nil {
		return AppState{}, err
	}
	a.mu.Lock()
	previousLanguage := a.settings.UILanguage
	a.settings = cloneSettings(settings)
	a.mu.Unlock()
	if previousLanguage != settings.UILanguage {
		mygo.App.SetLocale(settings.UILanguage)
	}
	a.configureTriggers()
	a.notifySettings()
	if err := a.trimHistory(); err != nil {
		return a.GetState(), fmt.Errorf("设置已保存，但清理超额历史失败：%w", err)
	}
	return a.GetState(), nil
}

// SetLanguages 只更新最新配置中的语言，避免界面旧快照覆盖刚保存的渠道或凭据。
func (a *GlideService) SetLanguages(sourceLang, targetLang string) (AppState, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if sourceLang == "" {
		sourceLang = "auto"
	}
	if targetLang == "" || targetLang == "auto" {
		return AppState{}, fmt.Errorf("请选择目标语言")
	}
	a.mu.RLock()
	settings := cloneSettings(a.settings)
	a.mu.RUnlock()
	settings.SourceLang = sourceLang
	settings.TargetLang = targetLang
	if err := writeSettings(a.dir, settings); err != nil {
		return AppState{}, err
	}
	a.mu.Lock()
	a.settings = settings
	a.mu.Unlock()
	a.notifySettings()
	return a.GetState(), nil
}

// Translate 并行查询启用的渠道，网络错误按渠道返回，所有请求均支持取消。
func (a *GlideService) Translate(ctx context.Context, req TranslateRequest) (TranslationResponse, error) {
	return a.translate(ctx, req, nil)
}

// translate 将网络等待放在独立协程中，并允许浮窗在单个渠道完成后显示结果。
func (a *GlideService) translate(ctx context.Context, req TranslateRequest, progress func(TranslationResponse)) (TranslationResponse, error) {
	req.Text = strings.TrimSpace(req.Text)
	a.mu.RLock()
	settings := cloneSettings(a.settings)
	a.mu.RUnlock()
	if req.SourceLang == "" {
		req.SourceLang = settings.SourceLang
	}
	if req.TargetLang == "" {
		req.TargetLang = settings.TargetLang
	}
	targetMode := ""
	if req.TargetLang == "smart" {
		targetMode = "smart"
		req.TargetLang = smartTargetLanguage(req.Text, req.SourceLang)
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return TranslationResponse{}, err
	}
	response := TranslationResponse{RequestID: hex.EncodeToString(id[:]), Text: req.Text, SourceLang: req.SourceLang, TargetLang: req.TargetLang, TargetMode: targetMode, CreatedAt: time.Now().Format(time.RFC3339), Results: []ChannelResult{}}
	if req.Text == "" {
		return response, fmt.Errorf("请先输入或选择需要翻译的文字")
	}
	if utf8.RuneCountInString(req.Text) > 10000 {
		return response, fmt.Errorf("单次最多翻译 10,000 个字符，请缩短选区")
	}
	providers := []translation.Config{}
	for _, p := range settings.Providers {
		if p.Enabled && (len(req.ProviderIDs) == 0 || slices.Contains(req.ProviderIDs, p.ID)) {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return response, fmt.Errorf("请在翻译渠道中启用至少一个渠道")
	}
	response.Results = make([]ChannelResult, len(providers))
	for i, p := range providers {
		response.Results[i] = ChannelResult{ProviderID: p.ID, ProviderName: p.Name, Pending: true}
	}
	if progress != nil {
		snapshot := response
		snapshot.Results = slices.Clone(response.Results)
		progress(snapshot)
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ctx = translation.WithProxy(ctx, settings.UseSystemProxy, settings.ProxyAddress)
	type completion struct {
		index  int
		result ChannelResult
	}
	completed := make(chan completion, len(providers))
	for i, p := range providers {
		go func(i int, p translation.Config) {
			start := time.Now()
			result, err := translation.Translate(ctx, p, translation.Request{Text: req.Text, SourceLang: req.SourceLang, TargetLang: req.TargetLang})
			channel := ChannelResult{ProviderID: p.ID, ProviderName: p.Name, Text: result.Text, DetectedLang: result.DetectedLang, DurationMS: time.Since(start).Milliseconds()}
			if err != nil {
				channel.Error = err.Error()
			}
			completed <- completion{i, channel}
		}(i, p)
	}
	for range providers {
		select {
		case <-ctx.Done():
			message := "翻译已取消或超时，请重试"
			for i := range response.Results {
				if response.Results[i].Pending {
					response.Results[i].Pending = false
					response.Results[i].Error = message
				}
			}
			if progress != nil {
				snapshot := response
				snapshot.Results = slices.Clone(response.Results)
				progress(snapshot)
			}
			return response, fmt.Errorf("翻译已取消或超时：%w", ctx.Err())
		case result := <-completed:
			response.Results[result.index] = result.result
			if progress != nil {
				snapshot := response
				snapshot.Results = slices.Clone(response.Results)
				progress(snapshot)
			}
		}
	}
	if slices.ContainsFunc(response.Results, func(r ChannelResult) bool { return r.Error == "" && r.Text != "" }) {
		if err := a.recordHistory(response); err != nil {
			return response, fmt.Errorf("翻译已完成，但保存历史失败：%w", err)
		}
	}
	return response, nil
}

// GetHistory 返回历史副本，调用方无法修改运行中的历史数据。
func (a *GlideService) GetHistory() []TranslationResponse {
	a.mu.RLock()
	defer a.mu.RUnlock()
	history := slices.Clone(a.history)
	for i := range history {
		history[i].Results = slices.Clone(history[i].Results)
	}
	return history
}

// ClearHistory 清除历史文件及内存中的记录。
func (a *GlideService) ClearHistory() error {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	if err := writeJSON(filepath.Join(a.dir, "history.json"), []TranslationResponse{}); err != nil {
		return err
	}
	a.mu.Lock()
	a.history = []TranslationResponse{}
	a.mu.Unlock()
	return nil
}

// recordHistory 串行写入历史，避免并发请求完成时相互覆盖。
func (a *GlideService) recordHistory(response TranslationResponse) error {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	a.mu.RLock()
	limit := a.settings.HistoryLimit
	if limit == 0 {
		a.mu.RUnlock()
		return nil
	}
	history := append([]TranslationResponse{response}, a.history...)
	a.mu.RUnlock()
	if len(history) > limit {
		history = history[:limit]
	}
	if err := writeJSON(filepath.Join(a.dir, "history.json"), history); err != nil {
		return err
	}
	a.mu.Lock()
	a.history = history
	a.mu.Unlock()
	return nil
}

// TranslateClipboard 仅在明确点击或托盘操作时读取剪贴板，不常驻监视。
func (a *GlideService) TranslateClipboard(ctx context.Context, req ClipboardRequest) (TranslationResponse, error) {
	if len(req.ProviderIDs) == 0 {
		return TranslationResponse{}, fmt.Errorf("请为本次翻译选择至少一个渠道")
	}
	return a.Translate(ctx, TranslateRequest{Text: mygo.Clipboard.ReadText(), SourceLang: req.SourceLang, TargetLang: req.TargetLang, ProviderIDs: req.ProviderIDs})
}

// CopyText 使用系统剪贴板，在原生浮窗中复制译文。
func (a *GlideService) CopyText(text string) { mygo.Clipboard.WriteText(text) }

// GetPanelState 返回当前浮窗快照。
func (a *GlideService) GetPanelState() PanelState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s := a.panel
	if s.Response != nil {
		r := *s.Response
		r.Results = slices.Clone(r.Results)
		s.Response = &r
	}
	return s
}

// ShowMain 显示设置窗口，macOS 同时恢复 Dock 入口；翻译浮窗不会改变 Dock 状态。
func (a *GlideService) ShowMain() {
	mygo.RunOnMain(func() {
		if a.mainWindow == nil {
			return
		}
		a.mainWindow.Show()
		a.mainWindow.Focus()
	})
}

// HideMain 关闭设置面板的可见入口，保留托盘和现有翻译状态。
func (a *GlideService) HideMain() {
	if a.mainWindow != nil {
		a.mainWindow.Hide()
	}
}

// SetPanelPinned 控制浮窗是否在失焦时保留。
func (a *GlideService) SetPanelPinned(pinned bool) {
	a.mu.Lock()
	a.panel.Pinned = pinned
	a.mu.Unlock()
	a.notifyPanel()
}

// TranslatePanel 使用编辑后的原文、语言和渠道重新翻译，结果仍显示在同一个浮窗中。
func (a *GlideService) TranslatePanel(req TranslateRequest) { a.runPanelTranslation(req, false) }

// HidePanel 隐藏浮窗并取消该浮窗尚未完成的请求。
func (a *GlideService) HidePanel() {
	a.mu.Lock()
	a.panelGeneration++
	a.cancelPanelRequestsLocked()
	a.mu.Unlock()
	a.panelWindow.Hide()
}

// OpenExternal 只允许网页链接，禁止页面借此调用任意系统 URL 协议。
func (a *GlideService) OpenExternal(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("仅支持打开网页链接")
	}
	return mygo.Shell.OpenExternal(raw)
}

// OpenSelectionSettings 打开系统辅助功能设置，授权仍由用户在系统界面完成。
func (a *GlideService) OpenSelectionSettings() error { return selection.OpenPermissionSettings() }

// DeleteHistory 按请求标识删除单条记录；不存在时保持原有历史。
func (a *GlideService) DeleteHistory(requestID string) error {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	history := a.GetHistory()
	history = slices.DeleteFunc(history, func(record TranslationResponse) bool { return record.RequestID == requestID })
	return a.saveHistory(history)
}

// trimHistory 按已保存的保留上限移除最旧记录，与新增和删除操作串行执行。
func (a *GlideService) trimHistory() error {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	a.mu.RLock()
	limit := a.settings.HistoryLimit
	a.mu.RUnlock()
	history := a.GetHistory()
	if len(history) <= limit {
		return nil
	}
	return a.saveHistory(history[:limit])
}

// saveHistory 先持久化再替换内存快照；调用方须持有 historyMu。
func (a *GlideService) saveHistory(history []TranslationResponse) error {
	if err := writeJSON(filepath.Join(a.dir, "history.json"), history); err != nil {
		return err
	}
	a.mu.Lock()
	a.history = history
	a.mu.Unlock()
	return nil
}

// shortcutIdentity 统一修饰键别名及顺序，用于检测重复绑定。
func shortcutIdentity(shortcut string) string {
	key := strings.ToLower(strings.TrimSpace(shortcut))
	command := "ctrl"
	if runtime.GOOS == "darwin" {
		command = "super"
	}
	key = strings.NewReplacer("cmdorctrl", command, "command", "super", "cmd", "super", "control", "ctrl", "option", "alt", "win", "super").Replace(key)
	parts := strings.Split(key, "+")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	slices.Sort(parts)
	return strings.Join(parts, "+")
}
