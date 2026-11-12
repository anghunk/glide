#!/usr/bin/env python3
"""校验本地 MyGo 更新签名密钥，并上传至仓库的 Actions Secret。"""

import argparse
import base64
import json
import os
from pathlib import Path
import subprocess
import sys


def default_key_path():
    """返回系统应用配置目录中的密钥位置，始终位于工作树以外。"""
    if sys.platform == "darwin":
        folder = Path.home() / "Library/Application Support"
    elif os.name == "nt":
        folder = Path(os.environ.get("APPDATA", Path.home()))
    else:
        folder = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config"))
    return folder / "Glide/release-keys/mygo-update.key"


def main():
    """只同步匹配已发布公钥的私钥，不生成替代密钥或创建 Git 提交。"""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--private-key", type=Path, default=default_key_path())
    parser.add_argument("--repo", help="默认使用 mygo.json 中的 updates.github")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    config = json.loads((root / "mygo.json").read_text(encoding="utf-8"))
    updates = config.get("updates", {})
    repo = args.repo or updates.get("github", "")
    if repo.count("/") != 1 or not all(repo.split("/")):
        parser.error("请配置 updates.github 或传入 --repo owner/name")
    key_path = args.private_key.expanduser().resolve()
    if not key_path.is_file():
        parser.error(f"找不到更新私钥：{key_path}。请从备份恢复与当前公钥配对的密钥。")
    if key_path.is_relative_to(root):
        parser.error("请将私钥移动到工作树外，再运行此命令")
    if os.name != "nt" and key_path.stat().st_mode & 0o077:
        parser.error("私钥权限过宽，请先执行 chmod 600 <私钥路径>")
    secret = key_path.read_bytes().strip()
    try:
        private = base64.b64decode(secret, validate=True)
        public = base64.b64decode(updates.get("publicKey", ""), validate=True)
    except ValueError:
        parser.error("密钥必须为 MyGo keygen 生成的 Base64 Ed25519 格式")
    if len(private) != 64 or len(public) != 32 or private[32:] != public:
        parser.error("私钥与 mygo.json 公钥不匹配，已中止同步；不会自动更换发布密钥")
    subprocess.run(
        ["gh", "secret", "set", "MYGO_UPDATER_PRIVATE_KEY", "--repo", repo],
        input=secret + b"\n", check=True,
    )
    print(f"已同步 {repo} 的 MYGO_UPDATER_PRIVATE_KEY；未输出或保存私钥到工作树。")


if __name__ == "__main__":
    try:
        main()
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"配置发布失败：{error}", file=sys.stderr)
        sys.exit(1)
