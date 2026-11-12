package main

import (
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// panelHeightLimits 将浮窗限制在所在屏幕可用高度的 80% 内，小屏幕同步降低最小高度。
func panelHeightLimits(workHeight int) (minimum, maximum int) {
	maximum = max(1, workHeight*4/5)
	return min(240, maximum), maximum
}

// layoutSignature 标记影响内容排版的变化，确保只在变化后的下一帧读取真实布局，不持续轮询。
func (p *panelUI) layoutSignature(state PanelState, settings Settings, width float32) uint64 {
	hash := fnv.New64a()
	fmt.Fprintf(hash, "%g/%t/%t/%t/%q/%q", width, settings.ShowSourceInput, settings.ShowLanguageBar, state.Loading, state.Error, p.message)
	if state.Response != nil {
		for _, result := range state.Response.Results {
			fmt.Fprintf(hash, "/%q/%t/%t/%q/%q", result.ProviderID, p.collapsed[result.ProviderID], result.Pending, result.Text, result.Error)
		}
	}
	return hash.Sum64()
}

// fitContent 在内容排版完成后合计高度，再异步调整原生窗口，避免布局过程中重入窗口回调。
func (p *panelUI) fitContent(c *ui.Context, state PanelState, settings Settings, composer, results ui.Element) {
	signature := p.layoutSignature(state, settings, c.Root().LayoutBounds().W)
	if signature != p.sizeSignature {
		p.sizeSignature = signature
		c.After(time.Millisecond)
		return
	}
	composerBounds, resultsBounds := composer.LayoutBounds(), results.LayoutBounds()
	if composerBounds.H <= 0 || resultsBounds.H <= 0 {
		return
	}
	// 工具栏 38、分隔线 0.5、滚动区域上下内边距 20、外边框 2。
	p.contentHeight = int(math.Ceil(float64(composerBounds.H + resultsBounds.H + 60.5)))
	window := p.app.panelWindow
	if window == nil || p.resizePending {
		return
	}
	bounds := window.Bounds()
	area := mygo.Screen.DisplayNearestPoint(mygo.Point{X: bounds.X + bounds.Width/2, Y: bounds.Y + bounds.Height/2}).WorkArea
	minimum, maximum := panelHeightLimits(area.Height)
	height := max(minimum, min(p.contentHeight, maximum))
	if bounds.Height == height && p.heightLimit == maximum {
		return
	}
	p.resizePending = true
	window.Update(func() {
		p.resizePending = false
		bounds := window.Bounds()
		area := mygo.Screen.DisplayNearestPoint(mygo.Point{X: bounds.X + bounds.Width/2, Y: bounds.Y + bounds.Height/2}).WorkArea
		minimum, maximum := panelHeightLimits(area.Height)
		height := max(minimum, min(p.contentHeight, maximum))
		p.heightLimit = maximum
		window.SetMinimumSize(min(360, max(1, area.Width-16)), minimum)
		window.SetMaximumSize(min(420, max(1, area.Width-16)), maximum)
		window.SetSize(bounds.Width, height)
		// 向下扩展触及屏幕底部时上移，固定浮窗也始终保持在可用区域内。
		y := max(area.Y, min(bounds.Y, area.Y+area.Height-height))
		if y != bounds.Y {
			window.SetPosition(bounds.X, y)
		}
	})
}
