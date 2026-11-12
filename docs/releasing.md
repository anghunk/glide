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

Ed25519 更新签名与 Apple Developer ID / Windows Authenticode 代码签名用途不同。当前流程可生成完整更新链路和可下载安装包，但没有厂商证书时 macOS 使用本地临时签名，Windows 没有发布者证书；公开分发时需根据账号配置平台签名及 macOS 公证。这不影响同一更新密钥对归档的真实性校验。

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
