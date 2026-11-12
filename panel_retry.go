package main

import (
	"context"
	"slices"
)

// cancelPanelRequestsLocked 取消整批与单渠道请求；调用方必须持有 a.mu 写锁。
func (a *GlideService) cancelPanelRequestsLocked() {
	if a.panelCancel != nil {
		a.panelCancel()
		a.panelCancel = nil
	}
	for _, cancel := range a.panelRetries {
		cancel()
	}
	a.panelRetries = nil
}

// updatePanelResponseLocked 合并整批进度并保留已重译渠道，避免较早请求覆盖新结果。
// 调用方必须持有 a.mu 写锁。
func (a *GlideService) updatePanelResponseLocked(response TranslationResponse) {
	response.Results = slices.Clone(response.Results)
	if a.panel.Response != nil {
		for i, result := range response.Results {
			if _, retried := a.panelRetries[result.ProviderID]; !retried {
				continue
			}
			for _, current := range a.panel.Response.Results {
				if current.ProviderID == result.ProviderID {
					response.Results[i] = current
					break
				}
			}
		}
	}
	a.panel.Response = &response
	a.panel.Loading = slices.ContainsFunc(response.Results, func(result ChannelResult) bool { return result.Pending })
}

// RetryPanelChannel 仅重译当前原文的指定渠道，保留其他渠道的译文和在途请求。
// 切换原文、关闭浮窗或退出时取消请求，过期结果不会写回新一批翻译。
func (a *GlideService) RetryPanelChannel(providerID string) {
	a.mu.Lock()
	if a.panel.Response == nil || a.quitting {
		a.mu.Unlock()
		return
	}
	response := a.panel.Response
	index := slices.IndexFunc(response.Results, func(result ChannelResult) bool { return result.ProviderID == providerID })
	if index < 0 || response.Results[index].Pending {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	if a.panelRetries == nil {
		a.panelRetries = make(map[string]context.CancelFunc)
	}
	if previous := a.panelRetries[providerID]; previous != nil {
		previous()
	}
	a.panelRetries[providerID] = cancel
	generation := a.panelGeneration
	req := TranslateRequest{Text: response.Text, SourceLang: response.SourceLang, TargetLang: response.TargetLang, ProviderIDs: []string{providerID}}
	response.Results[index] = ChannelResult{ProviderID: providerID, ProviderName: response.Results[index].ProviderName, Pending: true}
	a.panel.Loading = true
	a.mu.Unlock()
	a.notifyPanel()
	go func() {
		defer cancel()
		retried, err := a.translate(ctx, req, nil)
		a.mu.Lock()
		if a.panelGeneration != generation || a.panel.Response == nil {
			a.mu.Unlock()
			return
		}
		result := ChannelResult{ProviderID: providerID, ProviderName: a.panel.Response.Results[index].ProviderName}
		if len(retried.Results) > 0 {
			result = retried.Results[0]
		}
		if err != nil {
			result.Error = err.Error()
		}
		a.panel.Response.Results[index] = result
		a.panel.Loading = slices.ContainsFunc(a.panel.Response.Results, func(result ChannelResult) bool { return result.Pending })
		a.mu.Unlock()
		a.notifyPanel()
	}()
}
