package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

const webDAVBackupDirectory = "Glide-backup"
const webDAVProperties = `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/><d:getetag/></d:prop></d:propfind>`

var webDAVBackupName = regexp.MustCompile(`^glide-settings-([0-9]{8}-[0-9]{6})(-[a-f0-9]{8})?\.json$`)

// webDAVBackup 仅标识专用目录内的备份，ETag 用于避免恢复或删除已变更的文件。
type webDAVBackup struct {
	Name string
	ETag string
	Size int64
}

// webDAVClient 固定本次连接及目录，避免界面修改后误操作另一台服务器。
type webDAVClient struct {
	config    WebDAVConfig
	directory *url.URL
}

type webDAVHTTPError struct{ status int }

// Error 返回状态码及可操作的错误提示，不携带服务器凭据。
func (e *webDAVHTTPError) Error() string {
	switch e.status {
	case 401, 403:
		return fmt.Sprintf("WebDAV 认证失败或没有操作权限（HTTP %d）", e.status)
	case 404:
		return "WebDAV 目录或备份不存在，请刷新列表或检查目录地址"
	case 409:
		return "无法创建 Glide-backup，请确认填写的父目录已存在"
	case 412:
		return "远程备份已存在或已变更，请刷新列表后重试"
	}
	return fmt.Sprintf("WebDAV 请求失败（HTTP %d）", e.status)
}

// newWebDAVClient 校验父目录地址，所有备份请求限定在其 Glide-backup 子目录。
func newWebDAVClient(config WebDAVConfig) (*webDAVClient, error) {
	u, err := url.Parse(strings.TrimSpace(config.URL))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("请输入有效的 WebDAV 父目录地址（http 或 https，不含账号、查询参数）")
	}
	parent := path.Clean("/" + strings.TrimLeft(u.Path, "/"))
	if path.Base(parent) != webDAVBackupDirectory {
		parent = strings.TrimRight(parent, "/") + "/" + webDAVBackupDirectory
	}
	u.Path = parent + "/"
	u.RawPath = ""
	return &webDAVClient{config: config, directory: u}, nil
}

// request 限时访问固定目录，禁止重定向；条件请求防止覆盖已有文件。
func (d *webDAVClient) request(method, name, depth string, data []byte, etag string) ([]byte, error) {
	u := *d.directory
	if name != "" {
		if !webDAVBackupName.MatchString(name) {
			return nil, fmt.Errorf("无效的备份文件名")
		}
		u.Path += name
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法创建 WebDAV 请求")
	}
	req.SetBasicAuth(d.config.Username, d.config.Password)
	switch method {
	case "PROPFIND":
		req.Header.Set("Depth", depth)
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	case http.MethodPut:
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-None-Match", "*")
	}
	if strings.HasPrefix(strings.TrimSpace(etag), `"`) {
		req.Header.Set("If-Match", etag)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("WebDAV 连接失败，请检查地址、网络和证书")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &webDAVHTTPError{res.StatusCode}
	}
	if res.StatusCode == 207 && method != "PROPFIND" {
		return nil, fmt.Errorf("服务器未确认文件操作成功，请刷新备份列表")
	}
	if method != http.MethodGet && method != "PROPFIND" {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBackupBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 WebDAV 响应失败")
	}
	if len(body) > maxBackupBytes {
		return nil, fmt.Errorf("WebDAV 响应不能超过 4 MB")
	}
	return body, nil
}

type webDAVResponse struct {
	Href      string `xml:"href"`
	PropStats []struct {
		Status string `xml:"status"`
		Prop   struct {
			Type *struct {
				Collection *struct{} `xml:"collection"`
			} `xml:"resourcetype"`
			ETag string `xml:"getetag"`
			Size int64  `xml:"getcontentlength"`
		} `xml:"prop"`
	} `xml:"propstat"`
}

// responses 解析多状态响应，拒绝登录页或非 WebDAV XML。
func (d *webDAVClient) responses(depth string) ([]webDAVResponse, error) {
	data, err := d.request("PROPFIND", "", depth, []byte(webDAVProperties), "")
	if err != nil {
		return nil, err
	}
	var listing struct {
		XMLName   xml.Name         `xml:"DAV: multistatus"`
		Responses []webDAVResponse `xml:"response"`
	}
	if err := xml.Unmarshal(data, &listing); err != nil {
		return nil, fmt.Errorf("无法读取 WebDAV 目录列表，请确认地址支持 WebDAV")
	}
	return listing.Responses, nil
}

// resourcePath 只接受同一服务器的普通路径，不跟随列表中提供的外部地址。
func (d *webDAVClient) resourcePath(href string) string {
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil || ref.User != nil || ref.RawQuery != "" || ref.Fragment != "" {
		return ""
	}
	u := d.directory.ResolveReference(ref)
	if !strings.EqualFold(u.Scheme, d.directory.Scheme) || !strings.EqualFold(u.Host, d.directory.Host) {
		return ""
	}
	return u.Path
}

// checkDirectory 确认同名资源确实是目录，防止对普通文件进行集合操作。
func (d *webDAVClient) checkDirectory() error {
	responses, err := d.responses("0")
	if err != nil {
		return err
	}
	for _, response := range responses {
		if strings.TrimRight(d.resourcePath(response.Href), "/") != strings.TrimRight(d.directory.Path, "/") {
			continue
		}
		for _, stat := range response.PropStats {
			if davStatusOK(stat.Status) && stat.Prop.Type != nil && stat.Prop.Type.Collection != nil {
				return nil
			}
		}
	}
	return fmt.Errorf("Glide-backup 不是可访问的 WebDAV 文件夹")
}

// ensureDirectory 自动检测并创建备份目录，处理多个客户端同时创建的情况。
func (d *webDAVClient) ensureDirectory() error {
	err := d.checkDirectory()
	if err == nil {
		return nil
	}
	var status *webDAVHTTPError
	if !errors.As(err, &status) || status.status != 404 {
		return err
	}
	_, err = d.request("MKCOL", "", "", nil, "")
	if err != nil {
		if !errors.As(err, &status) || (status.status != 405 && status.status != 409) {
			return err
		}
	}
	return d.checkDirectory()
}

// davStatusOK 检查属性状态，不把 404 属性误当成正常备份。
func davStatusOK(status string) bool {
	fields := strings.Fields(status)
	return len(fields) >= 2 && fields[1] == "200"
}

// listBackups 只返回专用目录中名称合法的直接子文件，按时间倒序排列。
func (d *webDAVClient) listBackups() ([]webDAVBackup, error) {
	if err := d.ensureDirectory(); err != nil {
		return nil, err
	}
	responses, err := d.responses("1")
	if err != nil {
		return nil, err
	}
	backups := []webDAVBackup{}
	seen := map[string]bool{}
	for _, response := range responses {
		remotePath := d.resourcePath(response.Href)
		if path.Dir(remotePath) != strings.TrimRight(d.directory.Path, "/") {
			continue
		}
		name := path.Base(remotePath)
		if !webDAVBackupName.MatchString(name) || seen[name] {
			continue
		}
		backup := webDAVBackup{Name: name}
		file, collection := false, false
		for _, stat := range response.PropStats {
			if !davStatusOK(stat.Status) {
				continue
			}
			if stat.Prop.Type != nil {
				file = true
			}
			if stat.Prop.Type != nil && stat.Prop.Type.Collection != nil {
				collection = true
			}
			if stat.Prop.ETag != "" {
				backup.ETag = stat.Prop.ETag
			}
			if stat.Prop.Size > 0 {
				backup.Size = stat.Prop.Size
			}
		}
		if file && !collection {
			backups = append(backups, backup)
			seen[name] = true
		}
	}
	slices.SortFunc(backups, func(a, b webDAVBackup) int { return strings.Compare(b.Name, a.Name) })
	return backups, nil
}

// uploadBackup 为每次备份生成秒级时间戳；同秒冲突时附加随机后缀，始终使用禁止覆盖的条件请求。
func (d *webDAVClient) uploadBackup(data []byte) (string, error) {
	if err := d.ensureDirectory(); err != nil {
		return "", err
	}
	base := "glide-settings-" + time.Now().Format("20060102-150405")
	name := base + ".json"
	for attempt := 0; attempt < 4; attempt++ {
		_, err := d.request(http.MethodPut, name, "", data, "")
		if err == nil {
			return name, nil
		}
		var status *webDAVHTTPError
		if !errors.As(err, &status) || status.status != 412 {
			return "", err
		}
		var suffix [4]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return "", err
		}
		name = base + "-" + hex.EncodeToString(suffix[:]) + ".json"
	}
	return "", fmt.Errorf("备份文件名连续冲突，请重试")
}
