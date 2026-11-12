//go:build !darwin

package translation

import (
	"context"

	"golang.org/x/net/http/httpproxy"
)

// systemProxy 在其他平台保持标准环境代理行为。
func systemProxy(context.Context) (*httpproxy.Config, bool, error) {
	return &httpproxy.Config{}, false, nil
}
