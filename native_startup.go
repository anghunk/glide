package main

import (
	"fmt"

	"github.com/egoist/mygo"
)

// refreshOpenAtLogin 在主线程读取系统实际状态，反映用户在系统设置中的修改。
// 自启由操作系统管理，不写入可跨设备恢复的翻译配置。
func (s *settingsUI) refreshOpenAtLogin() {
	if s.loginBusy {
		return
	}
	s.openAtLogin = mygo.App.OpenAtLogin()
	s.app.mainWindow.Invalidate()
}

// setOpenAtLogin 将 enabled 应用到系统登录项；操作期间禁用开关，完成后回读实际状态。
// 注册失败或系统要求额外授权时，在所属分组显示错误，并恢复真实的开关状态。
func (s *settingsUI) setOpenAtLogin(enabled bool) {
	if s.loginBusy {
		return
	}
	s.loginBusy = true
	s.showNotice("general/启动", "", nil)
	go func() {
		err := mygo.App.SetOpenAtLogin(enabled)
		actual := mygo.App.OpenAtLogin()
		s.app.mainWindow.Update(func() {
			s.loginBusy = false
			s.openAtLogin = actual
			if err != nil {
				s.showNotice("general/启动", "", fmt.Errorf(tr("修改开机自启失败：%w"), err))
			} else if actual != enabled {
				s.showNotice("general/启动", "", fmt.Errorf("%s", tr("开机自启未生效，请检查系统的登录项或启动应用设置。")))
			}
		})
	}()
}
