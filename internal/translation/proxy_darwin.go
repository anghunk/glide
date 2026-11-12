//go:build darwin

package translation

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// systemProxy 每次读取当前 macOS 手动代理与排除列表，使代理开关变更无需重启应用。
func systemProxy(ctx context.Context) (*httpproxy.Config, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return nil, false, errors.New("无法读取系统代理，请检查系统网络设置")
	}
	return parseSystemProxy(string(output))
}

// parseSystemProxy 解析 scutil 的静态代理设置；HTTPS 代理通过 HTTP CONNECT 建立隧道。
func parseSystemProxy(output string) (*httpproxy.Config, bool, error) {
	values := make(map[string]string)
	var exceptions []string
	inExceptions := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if inExceptions && line == "}" {
			inExceptions = false
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		if key == "ExceptionsList" {
			inExceptions = true
			continue
		}
		if inExceptions {
			if value == "<local>" {
				values["ExcludeSimpleHostnames"] = "1"
			} else {
				exceptions = append(exceptions, value)
			}
			continue
		}
		values[key] = value
	}
	proxyURL := func(prefix, scheme string) string {
		if values[prefix+"Enable"] != "1" || values[prefix+"Proxy"] == "" {
			return ""
		}
		port, err := strconv.Atoi(values[prefix+"Port"])
		if err != nil || port < 1 || port > 65535 {
			return ""
		}
		return (&url.URL{Scheme: scheme, Host: net.JoinHostPort(values[prefix+"Proxy"], strconv.Itoa(port))}).String()
	}
	socks := proxyURL("SOCKS", "socks5")
	config := &httpproxy.Config{HTTPProxy: proxyURL("HTTP", "http"), HTTPSProxy: proxyURL("HTTPS", "http"), NoProxy: strings.Join(exceptions, ",")}
	if config.HTTPProxy == "" {
		config.HTTPProxy = socks
	}
	if config.HTTPSProxy == "" {
		config.HTTPSProxy = socks
	}
	return config, values["ExcludeSimpleHostnames"] == "1", nil
}
