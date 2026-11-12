package main

import "github.com/egoist/mygo/ui"

// proxy 提供独立的系统代理开关，默认开启；保存后对新发起的翻译和模型请求生效。
func (s *settingsUI) proxy(c *ui.Context, p pnPalette) {
	ui.Scroll(c.Key("proxy-scroll")).Grow(1).FillWidth().Padding(18).Gap(17).Children(func() {
		s.group(c, p, "系统代理", func() {
			detail := "开启后使用系统代理；关闭后翻译请求直接连接服务。"
			if s.state.Platform.OS != "darwin" {
				detail = "开启后使用 HTTP_PROXY / HTTPS_PROXY 环境代理，也可填写下方地址；关闭后直连。"
			}
			s.row(c, p, "使用系统代理", detail, func() {
				ui.Switch(c, &s.draft.UseSystemProxy).Label(tr("使用系统代理")).Size(38, 22)
			})
			ui.Column(c).Shrink(0).FillWidth().Children(func() {
				s.row(c, p, "代理地址", "留空跟随系统代理，填写后优先使用此地址。支持 HTTP、HTTPS 和 SOCKS5。", func() {})
				ui.TextInput(c.Key("proxy-address"), &s.draft.ProxyAddress).FillWidth().Height(29).FontSize(12.5).Label(tr("代理地址")).Placeholder(tr("例如 http://127.0.0.1:7890")).Disabled(!s.draft.UseSystemProxy)
			})
		})
	})
}
