// Command release_verify 在发布前校验 MyGo 更新签名、URL、版本与安装包完整性。
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type releaseManifest struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

// main 只读取公钥和构建产物，校验失败时阻止发布成为 latest。
func main() {
	dir := flag.String("dir", "release-dist", "待发布文件目录")
	repo := flag.String("repository", "", "GitHub owner/repo")
	flag.Parse()
	if err := verify(*dir, *repo); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("所有安装包、更新清单及 Ed25519 签名校验通过")
}

// verify 使用与 MyGo 一致的 SHA-256 + Ed25519 规则，五个 Mac/Windows 入口全部必须存在。
func verify(dir, repo string) error {
	var config struct {
		Version string `json:"version"`
		Slug    string `json:"slug"`
		Updates struct {
			PublicKey string `json:"publicKey"`
		} `json:"updates"`
	}
	raw, err := os.ReadFile("mygo.json")
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(config.Updates.PublicKey))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("无效更新公钥")
	}
	for _, target := range []string{"darwin-universal", "darwin-arm64", "darwin-amd64", "windows-amd64", "windows-arm64"} {
		archiveTarget := target
		if strings.HasPrefix(target, "darwin-") {
			archiveTarget = "darwin-universal"
		}
		name := config.Slug + "-" + config.Version + "-" + archiveTarget + ".tar.gz"
		raw, err = os.ReadFile(filepath.Join(dir, "update-"+target+".json"))
		if err != nil {
			return err
		}
		var manifest releaseManifest
		if err = json.Unmarshal(raw, &manifest); err != nil {
			return err
		}
		wantURL := "https://github.com/" + repo + "/releases/download/v" + config.Version + "/" + name
		if manifest.Version != config.Version || manifest.URL != wantURL {
			return fmt.Errorf("%s 的版本或下载源不匹配", target)
		}
		archive, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if int64(len(archive)) != manifest.Size || manifest.Size <= 0 {
			return fmt.Errorf("%s 更新归档大小不匹配", target)
		}
		sig, err := base64.StdEncoding.DecodeString(manifest.Signature)
		digest := sha256.Sum256(archive)
		if err != nil || !ed25519.Verify(ed25519.PublicKey(key), digest[:], sig) {
			return fmt.Errorf("%s 更新签名校验失败", target)
		}
	}
	for _, name := range []string{config.Slug + "-" + config.Version + ".dmg", config.Slug + "-setup-" + config.Version + "-amd64.exe", config.Slug + "-setup-" + config.Version + "-arm64.exe", "install.sh", "install.ps1", "SHA256SUMS"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("安装产物无效：%s", name)
		}
	}
	raw, err = os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	sums := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 || filepath.Base(parts[1]) != parts[1] {
			return fmt.Errorf("SHA256SUMS 格式错误")
		}
		if _, exists := sums[parts[1]]; exists {
			return fmt.Errorf("SHA256SUMS 包含重复文件")
		}
		sums[parts[1]] = parts[0]
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	count := 0
	for _, entry := range entries {
		if entry.Name() == "SHA256SUMS" {
			continue
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("发布目录中出现非普通文件")
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		if sums[entry.Name()] != fmt.Sprintf("%x", digest) {
			return fmt.Errorf("%s 的 SHA256SUMS 不匹配", entry.Name())
		}
		count++
	}
	if len(sums) != count {
		return fmt.Errorf("SHA256SUMS 包含缺失文件")
	}
	return nil
}
