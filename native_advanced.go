package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// advanced 提供本地备份与 WebDAV 多版本备份管理，恢复和删除需要确认。
func (s *settingsUI) advanced(c *ui.Context, p pnPalette) {
	ui.Scroll(c).Grow(1).FillWidth().Padding(18).Gap(17).Disabled(s.advancedBusy).Children(func() {
		s.group(c, p, "导入与导出", func() {
			s.row(c, p, "导出配置", "包含翻译服务、服务密钥和偏好，不含历史及 WebDAV 登录信息。", func() {
				ui.Button(c, "导出…").Height(28).OnClick(func() { s.startAdvanced("export") })
			})
			s.row(c, p, "导入配置", "用 JSON 文件替换本机服务和偏好，保留 WebDAV 配置。", func() {
				ui.Button(c, "导入…").Height(28).OnClick(func() { s.startAdvanced("import") })
			})
		})
		s.group(c, p, "WebDAV 同步", func() {
			ui.Column(c).Padding(12).Gap(12).Children(func() {
				s.field(c, p, "WebDAV 目录地址", &s.draft.WebDAV.URL, "https://dav.example.com/Glide/", false)
				s.field(c, p, "用户名", &s.draft.WebDAV.Username, "WebDAV 用户名", false)
				s.field(c, p, "密码 / 应用专用密码", &s.draft.WebDAV.Password, "WebDAV 密码", true)
				ui.Text(c, "填写 WebDAV 父目录，自动检测并创建 Glide-backup 文件夹。每次上传以年月日时分秒命名，保留历史备份；下载时选择要恢复的版本。建议使用 HTTPS。").FontSize(12).TextColor(p.Muted).LineHeight(1.5)
				ui.Row(c).Gap(8).Children(func() {
					ui.Button(c, "测试连接").Height(28).OnClick(func() { s.startAdvanced("test") })
					ui.Button(c, "上传配置").Height(28).OnClick(func() { s.startAdvanced("upload") })
					ui.Button(c, "下载配置").Height(28).OnClick(func() { s.startAdvanced("download") })
				})
			})
		})
		ui.Text(c, "备份含明文服务密钥，请妥善保存文件并使用可信任的 WebDAV 服务器。").FontSize(12).TextColor(p.Muted)
	})
}

// startAdvanced 先保存当前草稿，再执行文件或网络操作，期间阻止并行修改。
func (s *settingsUI) startAdvanced(action string) {
	s.startAdvancedBackup(action, webDAVBackup{})
}

// startAdvancedBackup 固定选中的备份与连接快照，防止切换服务器后误恢复或删除。
func (s *settingsUI) startAdvancedBackup(action string, backup webDAVBackup) {
	if s.advancedBusy || s.saving || s.autoSaving {
		s.showNotice(s.operationScope(), "请等待当前设置保存完成后重试", nil)
		return
	}
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.advancedBusy, s.saving = true, true
	scope := "advanced/WebDAV 同步"
	if action == "export" || action == "import" {
		scope = "advanced/导入与导出"
	}
	if action == "download" {
		s.backupDrawerOpen = true
	}
	if s.backupDrawerOpen && action != "export" && action != "import" {
		scope = "advanced/远程备份"
	}
	s.showNotice(scope, "", nil)
	draft, baseline := cloneSettings(s.draft), cloneSettings(s.state.Settings)
	source := s.remoteBackupsSource
	go func() {
		state, err := s.app.SaveSettings(mergeSettingsDraft(baseline, draft, s.app.GetState().Settings))
		result := advancedResult{}
		saved := err == nil
		if err == nil {
			result, err = s.performAdvanced(action, state.Settings, backup, source)
		}
		latest := s.app.GetState()
		s.app.mainWindow.Update(func() {
			s.advancedBusy, s.saving = false, false
			if saved {
				s.adopt(latest)
				s.observedSettings = cloneSettings(s.draft)
			}
			if result.listUpdated {
				s.remoteBackups, s.remoteBackupsSource, s.remoteBackupsLoaded = result.backups, result.source, true
			}
			if result.uploaded {
				s.remoteBackupsLoaded = false
			}
			if result.removed != "" {
				s.remoteBackups = slices.DeleteFunc(s.remoteBackups, func(item webDAVBackup) bool { return item.Name == result.removed })
			}
			if err != nil {
				s.showNotice(scope, "", err)
			} else {
				s.showNotice(scope, result.message, nil)
			}
		})
	}()
}

// confirmConfigReplace 用系统对话框确认本机配置替换，取消不执行恢复。
func (s *settingsUI) confirmConfigReplace(message string) (bool, error) {
	result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: s.app.mainWindow, Type: mygo.MessageWarning, Title: "覆盖配置", Message: message, Buttons: []string{"取消", "确认覆盖"}, DefaultButton: 0, CancelButton: 0})
	return result.Button == 1 && err == nil, err
}

// advancedResult 将后台结果一次性发布到主线程，不在网络回调中直接改列表。
type advancedResult struct {
	message               string
	backups               []webDAVBackup
	source                WebDAVConfig
	listUpdated, uploaded bool
	removed               string
}

// performAdvanced 使用调用时的已保存快照执行操作，不从后台读取界面草稿。
func (s *settingsUI) performAdvanced(action string, settings Settings, backup webDAVBackup, source WebDAVConfig) (advancedResult, error) {
	result := advancedResult{}
	filters := []mygo.FileFilter{{Name: "Glide 配置", Extensions: []string{"json"}}}
	switch action {
	case "export":
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: s.app.mainWindow, Title: "导出 Glide 配置", ButtonLabel: "保存", DefaultPath: "glide-settings.json", Filters: filters})
		if err != nil || path == "" {
			return result, err
		}
		if filepath.Clean(path) == filepath.Join(s.app.dir, "settings.json") || filepath.Clean(path) == filepath.Join(s.app.dir, "history.json") {
			return result, fmt.Errorf("请选择应用数据文件以外的导出位置")
		}
		data, err := encodeBackup(settings)
		if err != nil {
			return result, err
		}
		if err := writeJSON(path, json.RawMessage(data)); err != nil {
			return result, err
		}
		result.message = "配置已导出"
		return result, nil
	case "import":
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: s.app.mainWindow, Title: "导入 Glide 配置", ButtonLabel: "打开", Filters: filters})
		if err != nil || len(paths) == 0 {
			return result, err
		}
		data, err := readBackup(paths[0])
		if err != nil {
			return result, err
		}
		result.message, err = s.restoreBackup(data, settings.WebDAV)
		return result, err
	}
	client, err := newWebDAVClient(settings.WebDAV)
	if err != nil {
		return result, err
	}
	switch action {
	case "test":
		err = client.ensureDirectory()
		result.message = "WebDAV 连接成功，Glide-backup 文件夹已就绪"
	case "upload":
		var data []byte
		data, err = encodeBackup(settings)
		if err == nil {
			var name string
			name, err = client.uploadBackup(data)
			result.uploaded = err == nil
			result.message = "备份已上传：" + name
		}
	case "download":
		result.backups, err = client.listBackups()
		result.source, result.listUpdated = settings.WebDAV, err == nil
	case "restore-backup", "delete-backup":
		if settings.WebDAV != source {
			return result, fmt.Errorf("WebDAV 连接已改变，请重新获取备份列表")
		}
		if !webDAVBackupName.MatchString(backup.Name) {
			return result, fmt.Errorf("请选择有效的备份文件")
		}
		if action == "restore-backup" {
			var data []byte
			data, err = client.request(http.MethodGet, backup.Name, "", nil, backup.ETag)
			if err == nil {
				result.message, err = s.restoreBackup(data, settings.WebDAV)
			}
		} else {
			_, err = client.request(http.MethodDelete, backup.Name, "", nil, backup.ETag)
			if err == nil {
				result.removed = backup.Name
				result.message = "远程备份已删除"
			}
		}
	default:
		err = fmt.Errorf("未知的配置操作")
	}
	return result, err
}

// restoreBackup 验证备份并确认后替换本机配置，保留本机 WebDAV 登录信息。
func (s *settingsUI) restoreBackup(data []byte, local WebDAVConfig) (string, error) {
	imported, err := decodeBackup(data, local)
	if err != nil {
		return "", err
	}
	ok, err := s.confirmConfigReplace(fmt.Sprintf("将用备份中的 %d 个翻译服务及偏好替换本机配置。WebDAV 登录信息和翻译历史不导入；历史保留条数降低时会清理超额记录。", len(imported.Providers)))
	if err != nil || !ok {
		return "", err
	}
	_, err = s.app.SaveSettings(imported)
	return "配置已恢复到本地", err
}

// backupList 展示最新优先的备份，文字与抽屉标题对齐，操作和文件信息分行排列。
func (s *settingsUI) backupList(c *ui.Context, p pnPalette) {
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Row(c).FillWidth().Shrink(0).Padding(0, 0, 10).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, .5, 0).BorderColor(p.FieldBorder).Children(func() {
			ui.Text(c, fmt.Sprintf("Glide-backup · %d 份备份", len(s.remoteBackups))).Grow(1).MinWidth(0).SingleLine().TextAlign(ui.Start).FontSize(12).TextColor(p.Muted)
			ui.Button(c, "刷新列表").Height(28).Shrink(0).Disabled(s.advancedBusy).OnClick(func() { s.startAdvanced("download") })
		})
		if len(s.remoteBackups) == 0 {
			ui.Column(c).FillWidth().Padding(20, 0).Gap(6).Children(func() {
				ui.Text(c, "暂无远程备份").FontSize(13).FontWeight(600)
				ui.Text(c, "点击“上传配置”创建第一份备份。").FontSize(12).TextColor(p.Muted)
			})
			return
		}
		ui.Scroll(c.Key("remote-backups")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			for _, backup := range s.remoteBackups {
				ui.Column(c.Key(backup.Name)).FillWidth().Shrink(0).Padding(12, 0).Gap(6).BorderWidth(0, 0, .5, 0).BorderColor(p.Divider).Children(func() {
					title := backup.Name
					if parts := webDAVBackupName.FindStringSubmatch(backup.Name); len(parts) > 1 {
						if date, err := time.Parse("20060102-150405", parts[1]); err == nil {
							title = date.Format("2006-01-02 15:04:05")
						}
					}
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, title).Grow(1).MinWidth(0).SingleLine().TextAlign(ui.Start).FontSize(13).FontWeight(600).Tooltip(title)
						ui.Button(c, "恢复").Height(28).Padding(0, 12).Shrink(0).FontSize(12).Disabled(s.advancedBusy).OnClick(func() { s.startAdvancedBackup("restore-backup", backup) })
						ui.ButtonBase(c).Size(28, 28).Shrink(0).Radius(6).Center().Disabled(s.advancedBusy).Label("删除备份 " + backup.Name).Tooltip("删除远程备份").Children(func() { pnIcon(c, "trash", 14, p.Muted) }).OnClick(func() {
							s.confirmDeletion("删除远程备份", "确定永久删除备份“"+backup.Name+"”？此操作无法撤销。", func() { s.startAdvancedBackup("delete-backup", backup) })
						})
					})
					ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, backup.Name).Grow(1).MinWidth(0).SingleLine().TextAlign(ui.Start).FontSize(11).TextColor(p.Muted).Tooltip(backup.Name)
						if backup.Size > 0 {
							ui.Text(c, fmt.Sprintf("%.1f KB", float64(backup.Size)/1024)).Shrink(0).SingleLine().FontSize(11).TextColor(p.Muted)
						}
					})
				})
			}
		})
	})
}

// backupDrawer 用持久动画进度控制右侧滑入与滑出，退场结束后再移除模态层。
func (s *settingsUI) backupDrawer(c *ui.Context, p pnPalette) {
	if s.category != "advanced" {
		s.backupDrawerOpen = false
	}
	target := float32(0)
	animation := ui.Animation{Duration: 220 * time.Millisecond, Ease: ui.EaseInOut}
	if s.backupDrawerOpen {
		target = 1
		animation = ui.Animation{Duration: 280 * time.Millisecond, Ease: ui.EaseOut}
	}
	// 根节点始终存在，首次展开以及途中反向都从当前进度平滑衔接。
	progress := c.Root().AnimateTo("backup-drawer-progress", target, animation)
	if !s.backupDrawerOpen && progress <= 0 {
		return
	}
	visible := true
	ui.DialogBase(c.Key("backup-drawer"), &visible, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .18*progress)).AlignItems(ui.End).Justify(ui.Start).Clip()
		panel.Width(460).FillHeight().Padding(20).Gap(14).Background(p.Surface).TextColor(p.Text).BorderWidth(0, 0, 0, .5).BorderColor(p.FieldBorder).Shadow(-4, 0, 18, 0, ui.RGBA(0, 0, 0, .12)).Label("WebDAV 备份列表").
			TranslatePercent(100*(1-progress), 0).Disabled(!s.backupDrawerOpen)
		ui.Row(c).Gap(10).Children(func() {
			ui.Text(c, "选择备份").Grow(1).FontSize(16).FontWeight(650)
			ui.ButtonBase(c).Size(28, 28).Radius(6).Center().Label("关闭备份列表").Tooltip("关闭").Children(func() { pnIcon(c, "close", 15, p.Muted) }).OnClick(func() { s.backupDrawerOpen = false })
		})
		ui.Text(c, "恢复到本地前会确认，WebDAV 登录信息保留在本机。").FontSize(12).TextColor(p.Muted).LineHeight(1.5)
		if s.advancedBusy {
			ui.Row(c).Gap(8).Children(func() { ui.Spinner(c).Size(13, 13); ui.Text(c, "正在处理…").FontSize(12).TextColor(p.Muted) })
		}
		s.inlineNotice(c, p, "远程备份")
		if s.remoteBackupsLoaded && s.remoteBackupsSource == s.draft.WebDAV {
			s.backupList(c, p)
		} else if !s.advancedBusy {
			ui.Text(c, "列表尚未加载，或连接信息已改变。").FontSize(12).TextColor(p.Muted)
			ui.Button(c, "重新获取列表").Height(28).OnClick(func() { s.startAdvanced("download") })
		}
	})
	if !visible {
		s.backupDrawerOpen = false
	}
}
