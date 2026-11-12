# GitHub 发布与自动更新

版本唯一来源是 `mygo.json` 中的 `version`。应用读取同一配置显示版本，CI 也从这里决定是否发布，不需要再手动维护 Go 常量或标签。

首次配置完成后，将新版本号推送到 GitHub 默认分支，即会运行 `.github/workflows/release.yml`。也可在 Actions 中手动运行“发布桌面版本”。其他分支不发布；同一版本重复推送不会覆盖已有 Releases。版本必须高于已发布的稳定版；例如 `0.1.0 → 0.1.1`，预发布可用 `0.2.0-beta.1`。

## 更新日志

每次发布时，在根目录 `CHANGELOG.md` 顶部添加与 `mygo.json` 一致的 `## 版本号`，下面使用中文列表记录变更。版本标题不带 `v`，参考：

```markdown
## 0.1.1

- 修复翻译浮窗显示问题。
- 优化服务配置体验。
```

构建会将该节日志写入各平台更新清单，检查更新发现新版本时会在独立更新窗口中展示。GitHub Release 使用同一节日志作为正文，名称与标签均为 `v<版本号>`（如 `v0.1.1`）。缺少日志、日志为空或清单日志不一致时，流水线会停止发布。历史版本日志继续保留在文件中。

## 首次配置

1. 仓库需要公开，应用才能匿名读取 Release 更新清单与安装包。当前更新源为 `anghunk/glide`；CI 会自动采用工作流所在的 `github.repository`，复制项目到其他仓库后也不会错误地发布到原仓库。
2. 将生成的 Ed25519 私钥内容保存在该仓库的 Actions Repository Secret `MYGO_UPDATER_PRIVATE_KEY`。公钥已经记录在 `mygo.json` 的 `updates.publicKey` 与 `resources/mygo-update.pub`。私钥不应提交到 Git，也不能打印到日志或写入应用包。
3. 保留这对密钥的备份；后续发布必须继续使用同一私钥。随意重新生成公钥会导致已经安装的版本无法信任新更新。
4. 首次正常发布需要默认分支已有这套工作流。不要把 `build/`、本地配置或翻译凭据推送到仓库。

推荐使用项目提供的配置命令，它会先检查私钥位置、文件权限及公钥配对，再同步到 GitHub Secret；不会生成替代密钥或创建 Git 提交：

```bash
python3 scripts/setup-release.py
```

也可以显式上传现有私钥，使用文件重定向，避免写进命令历史：

```bash
gh secret set MYGO_UPDATER_PRIVATE_KEY --repo anghunk/glide < "/私钥所在目录/mygo-update.key"
```

工作流的发布步骤使用 GitHub 自动提供的 `GITHUB_TOKEN`，不需要额外创建 PAT。它只有发布步骤拥有 `contents: write`，构建步骤仅有读取权限。用户安装的应用不包含 GitHub Token。

## 默认流水线

1. 检查默认分支、公开仓库、严格 SemVer，以及 GitHub 上所有已发布稳定版。相同或较旧版本直接跳过，API 鉴权失败不会当作“首次发布”。
2. 独立构建三个目标：macOS 通用版（Apple Silicon + Intel）、Windows x64、Windows ARM64。Windows 构建器由 MyGo 自动准备 NSIS，生成每用户安装器。
3. 每个目标使用同一个 Ed25519 私钥，生成更新归档及 `update-<target>.json`，并将安装包和更新文件上传为 Actions 临时产物。
4. 等待三个目标全部成功，再聚合产物、验证公钥签名、版本、下载地址及归档尺寸。另提供 `update-darwin-arm64.json` / `update-darwin-amd64.json`，指向同一通用归档，兼容本机单架构构建的更新入口。
5. 生成合并的 `SHA256SUMS`，创建 Release 草稿，上传完整文件并复查远端大小与散列，再一次性正式发布。应用不会看到尚未构建完整的版本。
6. 稳定版设为 `latest`；包含 `-beta.1` 等后缀的版本标记为预发布，不改变稳定更新入口。默认应用不接收预发布。

发布以完整提交 SHA 为目标自动创建 `v<version>` 标签。已有正式 Release 始终禁止覆盖。如果上传暂时失败，同一提交重跑可以继续本工具创建的未发布草稿；其他来源或其他提交的同名草稿会被拒绝，需要人工检查。

发布内容包括：

| 平台 | 用户安装包 | 自动更新文件 |
| --- | --- | --- |
| macOS 通用版 | `glide-<版本>.dmg` | `glide-<版本>-darwin-universal.tar.gz`、3 个 macOS 架构清单 |
| Windows x64 | `glide-setup-<版本>-amd64.exe`、便携 ZIP | `glide-<版本>-windows-amd64.tar.gz`、对应清单 |
| Windows ARM64 | `glide-setup-<版本>-arm64.exe`、便携 ZIP | `glide-<版本>-windows-arm64.tar.gz`、对应清单 |

还会提供 `install.sh`、`install.ps1` 和全平台的 `SHA256SUMS`。安装脚本从公开的 latest Release 获取对应平台安装包。

## 应用端更新

MyGo 在正式打包时把更新源和公钥写入程序。应用使用 MyGo 官方原生更新插件，内部由 `mygo.Updater.Check` 检测新版本、`Update.Install` 下载及验证签名并安装，再通过 `App.Relaunch` 重启。Ed25519 签名不匹配的归档无法安装。

默认启动后 10 秒检查一次，随后每 6 小时检查；后台自动检查默认开启，自动下载和安装默认关闭。可以在原生更新窗口修改偏好，保存于应用数据目录的 `updater.json`；重启始终由用户点击确认。关于页及托盘菜单也提供检查更新入口。

开发模式中 MyGo 禁用自动更新。macOS 从 DMG 打开时应先拖到可写的“应用程序”目录再运行；Windows 使用默认的每用户安装目录。更新目标不可写时，应用会报告原因，不会静默要求管理员权限。

Ed25519 更新签名与 macOS / Windows 代码签名用途不同。macOS 现使用固定的自签名证书，Windows 没有发布者证书；归档继续使用原来的 Ed25519 密钥验证。

## 固定的 macOS 自签名身份

macOS 构建使用独立于 Apple 账号的 `Glide Local Signing` 自签名证书。`mygo.json` 的 `macos.signingIdentity` 固定为证书 SHA-1 指纹，用于选择签名身份。不同版本的程序散列会变化，但指定要求仍绑定同一证书及 `app.glide.translator`，使系统可以识别更新前后是同一应用。切换自临时签名后需要重新授权一次。已在本机验证：授权后替换为内容不同、使用同一证书的新构建，重启后辅助功能权限保持有效，无需再次移除和添加授权。

本机证书、私钥、加密 PKCS#12 备份和密码保存在工作树之外：

```text
~/Library/Application Support/Glide/release-keys/macos-signing/
```

请离线备份整个目录，尤其是 `identity.p12` 和 `p12-password`。不要提交至 Git、作为 Release 附件发布，或重新生成同名证书替换。证书名相同不代表签名身份相同。更换电脑时，从备份恢复这个目录，再准备专用钥匙串；若证书尚未被信任，需在系统中确认它仅用于代码签名。证书有效期至 2046 年 11 月，换证前需重新评估身份迁移。

```bash
# 本地构建前解锁专用钥匙串，不会重新生成证书。
python3 scripts/macos-signing.py prepare
go tool mygo build -platform darwin/universal

# 开发版使用同一证书，但保留独立的 .dev 应用标识和权限。
make dev

# 校验证书指纹后，将现有证书备份同步到 updates.github 指定仓库。
python3 scripts/macos-signing.py sync
```

CI 使用 `MACOS_SIGNING_P12_BASE64` 和 `MACOS_SIGNING_P12_PASSWORD` 两个 Actions Secrets，校验指纹后导入临时专用钥匙串，只为代码签名信任该证书。构建结束清理钥匙串和临时文件。缺少证书或指纹不匹配时直接停止，不回退到临时签名。同步 Secrets 不会提交或推送代码，流水线改动需按正常流程推送后才生效。

没有发布证书的贡献者可使用 `go tool mygo dev` 运行临时签名的开发版，不需要获取项目私钥。直接运行该命令不会采用 `macos.signingIdentity`，需要保持开发版权限时使用 `make dev`。

自签名不依赖 Apple 开发者账号，也不能获得 Apple 公证；首次下载打开时仍可能遇到 Gatekeeper 来源验证提示。

## 本地验证

```bash
# 只读检查版本是否需要发布
python3 scripts/release.py plan --repository anghunk/glide

# 使用私钥环境变量，构建正式 macOS 通用版（不要将私钥直接写入命令行）
export MYGO_UPDATER_PRIVATE_KEY="$(cat '/私钥所在目录/mygo-update.key')"
go tool mygo build -platform darwin/universal
unset MYGO_UPDATER_PRIVATE_KEY
```

`scripts/release.py` 将版本判定与发布步骤分开，可以用临时 JSON 输入或导入函数验证版本排序，不需要创建项目测试文件。仅本地通过构建并不等于 GitHub Actions 已执行；首个真实发布需要先将当前代码推送到配置好的默认分支。

依据：[MyGo 上游](https://github.com/egoist/mygo)、[GitHub CLI Releases](https://cli.github.com/manual/gh_release_create)、[GitHub 自动令牌权限](https://docs.github.com/actions/security-for-github-actions/security-guides/automatic-token-authentication)。当前实现按项目固定的 MyGo v0.8.3 接口核对。
