package translation

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

type systemProxyKey struct{}

type proxySettings struct {
	enabled bool
	address string
}

// WithProxy 为本次请求指定代理开关和手动地址；地址为空时使用系统代理，关闭时直连。
func WithProxy(ctx context.Context, enabled bool, address string) context.Context {
	return context.WithValue(ctx, systemProxyKey{}, proxySettings{enabled: enabled, address: address})
}

// NormalizeProxyAddress 补全省略的 HTTP 协议，拒绝无效端口及非代理 URL，空值表示跟随系统。
func NormalizeProxyAddress(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", nil
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	invalid := errors.New("代理地址无效，请填写 http://、https://、socks5:// 或 socks5h:// 地址及端口")
	if err != nil || parsed.Hostname() == "" {
		return "", invalid
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", invalid
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalid
	}
	parsed.Path = ""
	return parsed.String(), nil
}

// translationProxy 仅在主动开启代理后读取环境或系统代理，并遵守 NO_PROXY。
func translationProxy(request *http.Request) (*url.URL, error) {
	settings, _ := request.Context().Value(systemProxyKey{}).(proxySettings)
	if !settings.enabled {
		return nil, nil
	}
	config := httpproxy.FromEnvironment()
	if settings.address != "" {
		address, err := NormalizeProxyAddress(settings.address)
		if err != nil {
			return nil, err
		}
		return (&httpproxy.Config{HTTPProxy: address, HTTPSProxy: address, NoProxy: config.NoProxy}).ProxyFunc()(request.URL)
	}
	if request.URL.Scheme == "http" && config.HTTPProxy != "" ||
		request.URL.Scheme == "https" && config.HTTPSProxy != "" {
		return config.ProxyFunc()(request.URL)
	}
	system, excludeSimple, err := systemProxy(request.Context())
	if err != nil {
		return nil, err
	}
	if excludeSimple && !strings.ContainsAny(request.URL.Hostname(), ".:") {
		return nil, nil
	}
	system.NoProxy = strings.Join([]string{system.NoProxy, config.NoProxy}, ",")
	return system.ProxyFunc()(request.URL)
}
