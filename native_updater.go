package main

import (
	"context"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	updaterui "github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

// appUpdateState 是关于页及托盘所需的轻量快照，不持有下载或安装任务。
type appUpdateState struct {
	Loaded, Enabled, Saving             bool
	AutomaticChecks, AutomaticDownloads bool
	AvailableVersion                    string
	LastCheck                           time.Time
}

// appUpdater 桥接官方原生更新插件；插件负责检查合并、签名验证、安装进度和重启确认。
type appUpdater struct {
	mu          sync.RWMutex
	refreshMu   sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	off         func()
	prefsSaving bool
	state       appUpdateState
}

// configureUpdater 在 App.Run 前注册原生更新插件，避免引入 WebView。
// 开发模式不注册插件，因此不会读写正式版的 updater.json 或请求更新。
func configureUpdater() {
	if mygo.IsDev() {
		return
	}
	icon, _ := brandAssets.ReadFile("assets/brand/logo.png")
	mygo.Updater.SetPrereleases(false)
	mygo.Use(updaterui.New(updater.Options{
		Interval: 6 * time.Hour,
		Icon:     icon,
		Language: "zh-CN",
	}))
}

// startUpdater 在窗口创建后连接状态通知；官方插件默认于启动十秒后首次检查。
func (a *GlideService) startUpdater() {
	a.mu.Lock()
	if a.quitting || a.updater != nil {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u := &appUpdater{ctx: ctx, cancel: cancel}
	a.updater = u
	a.mu.Unlock()
	if mygo.IsDev() {
		u.mu.Lock()
		u.state.Loaded = true
		u.mu.Unlock()
		return
	}
	u.mu.Lock()
	u.off = updater.OnChange(a.refreshUpdaterState)
	u.mu.Unlock()
	a.refreshUpdaterState()
}

// stopUpdater 取消快照读取并移除订阅；检查与安装窗口的任务由官方插件自身管理。
func (a *GlideService) stopUpdater() {
	a.mu.RLock()
	u := a.updater
	a.mu.RUnlock()
	if u == nil {
		return
	}
	u.cancel()
	u.mu.Lock()
	off := u.off
	u.off = nil
	u.mu.Unlock()
	if off != nil {
		off()
	}
}

// updateState 读取内存快照，绘制时不探测安装目录、不读文件或访问网络。
func (a *GlideService) updateState() appUpdateState {
	a.mu.RLock()
	u := a.updater
	a.mu.RUnlock()
	if u == nil {
		return appUpdateState{}
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.state
}

// refreshUpdaterState 在后台串行读取插件偏好及安装目录状态，避免阻塞原生绘制。
func (a *GlideService) refreshUpdaterState() {
	a.mu.RLock()
	u := a.updater
	a.mu.RUnlock()
	if u == nil || mygo.IsDev() || u.ctx.Err() != nil {
		return
	}
	go func() {
		u.refreshMu.Lock()
		defer u.refreshMu.Unlock()
		if u.ctx.Err() != nil {
			return
		}
		state := appUpdateState{
			Loaded: true, Enabled: mygo.Updater.Enabled(),
			AutomaticChecks: updater.AutomaticChecks(), AutomaticDownloads: updater.AutomaticDownloads(),
			AvailableVersion: updater.AvailableVersion(), LastCheck: updater.LastCheck(),
		}
		if u.ctx.Err() != nil {
			return
		}
		u.mu.Lock()
		state.Saving = u.prefsSaving
		u.state = state
		u.mu.Unlock()
		a.notifySettings()
	}()
}

// CheckForUpdates 打开官方原生更新窗口，完整展示检查结果、说明、安装进度及重启选择。
// 后台检查共用同一会话；重复调用只会将正在进行的更新窗口带到前台。
func (a *GlideService) CheckForUpdates() {
	if mygo.IsDev() {
		a.showSettingsCategory("about")
		return
	}
	updater.CheckForUpdates()
}

// updateMenuLabel 显示已发现的新版本；下载或安装行为仍需进入更新窗口操作。
func (a *GlideService) updateMenuLabel() string {
	if version := a.updateState().AvailableVersion; version != "" {
		return "查看新版本 " + version + "…"
	}
	return "检查更新…"
}

// setUpdatePreference 将可逆偏好写入官方插件的 updater.json，不阻塞设置窗口。
func (a *GlideService) setUpdatePreference(downloads, on bool) {
	a.mu.RLock()
	u := a.updater
	a.mu.RUnlock()
	if u == nil || mygo.IsDev() || u.ctx.Err() != nil {
		return
	}
	u.mu.Lock()
	if u.prefsSaving || !u.state.Loaded || !u.state.Enabled {
		u.mu.Unlock()
		return
	}
	u.prefsSaving = true
	u.state.Saving = true
	u.mu.Unlock()
	a.notifySettings()
	go func() {
		if u.ctx.Err() != nil {
			return
		}
		if downloads {
			updater.SetAutomaticDownloads(on)
		} else {
			updater.SetAutomaticChecks(on)
		}
		u.mu.Lock()
		u.prefsSaving = false
		u.mu.Unlock()
		a.refreshUpdaterState()
	}()
}

// renderUpdateSettings 展示偏好及上次成功检查的信息，详细更新流程交给官方原生窗口。
func (a *GlideService) renderUpdateSettings(c *ui.Context, p pnPalette) {
	state := a.updateState()
	ui.Column(c.Key("application-updates")).Gap(8).Children(func() {
		ui.Text(c, "软件更新").FontSize(12).FontWeight(600).TextColor(p.Muted).Padding(0, 4)
		ui.Column(c).Background(p.Group).Radius(12).Padding(13, 14).Gap(11).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					title, detail := "保持 Glide 为最新版本", "更新来自 GitHub Releases，安装前会验证签名。"
					if state.AvailableVersion != "" {
						title = "发现新版本 " + state.AvailableVersion
						detail = "查看发布说明，选择安装或稍后提醒。"
					}
					if mygo.IsDev() {
						detail = "开发版本不检查更新，请使用正式安装包。"
					} else if state.Loaded && !state.Enabled {
						detail = "此构建未启用更新，或安装目录不可写。请使用正式安装包并放在可写目录。"
					}
					ui.Text(c, title).FontSize(13.5).FontWeight(600)
					ui.Text(c, detail).FontSize(12).TextColor(p.Muted).LineHeight(1.4)
				})
				label := "检查更新"
				if state.AvailableVersion != "" {
					label = "查看更新"
				}
				ui.Button(c, label).Height(29).FontSize(12.5).Disabled(mygo.IsDev()).OnClick(a.CheckForUpdates)
			})
			if !mygo.IsDev() {
				checks, downloads := state.AutomaticChecks, state.AutomaticDownloads
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "自动检查更新").Grow(1).FontSize(13)
					if ui.Switch(c, &checks).Label("自动检查更新").Size(38, 22).Disabled(!state.Enabled || state.Saving).Changed() {
						a.setUpdatePreference(false, checks)
					}
				})
				ui.Row(c).Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Text(c, "自动下载并安装").FontSize(13)
						ui.Text(c, "下次启动时生效，不会自动重启。").FontSize(11.5).TextColor(p.Muted)
					})
					if ui.Switch(c, &downloads).Label("自动下载并安装更新").Size(38, 22).Disabled(!state.Enabled || state.Saving).Changed() {
						a.setUpdatePreference(true, downloads)
					}
				})
			}
			if !state.LastCheck.IsZero() {
				ui.Text(c, "上次成功检查："+state.LastCheck.Local().Format("2006-01-02 15:04")).FontSize(11.5).TextColor(p.Muted)
			}
		})
	})
}
