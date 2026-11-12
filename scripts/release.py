#!/usr/bin/env python3
"""Glide 发布工具：版本判定、构建配置、产物聚合与 GitHub 草稿发布。"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import zipfile

TARGETS = ("darwin-universal", "windows-amd64", "windows-arm64")
VERSION = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$")


def semver(value):
    """解析发布版本，拒绝前导零及没有更新优先级的 build metadata。"""
    match = VERSION.fullmatch(value)
    if not match:
        raise ValueError(f"无效发布版本 {value!r}，请使用 1.2.3 或 1.2.3-beta.1")
    pre = match[4].split(".") if match[4] else []
    if any(part.isdigit() and len(part) > 1 and part[0] == "0" for part in pre):
        raise ValueError("预发布版本的数字标识不能有前导零")
    return tuple(int(match[i]) for i in (1, 2, 3)), pre


def compare(left, right):
    """按 SemVer 比较稳定版及预发布版，避免字符串排序造成版本倒退。"""
    a, ap = semver(left)
    b, bp = semver(right)
    if a != b:
        return (a > b) - (a < b)
    if not ap or not bp:
        return (not ap) - (not bp)
    for x, y in zip(ap, bp):
        if x == y:
            continue
        if x.isdigit() and y.isdigit():
            return (int(x) > int(y)) - (int(x) < int(y))
        if x.isdigit() != y.isdigit():
            return -1 if x.isdigit() else 1
        return (x > y) - (x < y)
    return (len(ap) > len(bp)) - (len(ap) < len(bp))


def config():
    """读取唯一版本来源与签名配置；发布源必须是公共 GitHub 仓库。"""
    data = json.loads(Path("mygo.json").read_text(encoding="utf-8"))
    semver(data["version"])
    key = base64.b64decode(data["updates"]["publicKey"].strip(), validate=True)
    if len(key) != 32:
        raise ValueError("updates.publicKey 不是 Ed25519 公钥")
    return data


def repository(value):
    """校验仓库名，命令参数从不拼接为 shell 脚本。"""
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", value):
        raise ValueError("仓库必须为 owner/repo")
    return value


def api(path, method="GET", body=None, missing=False):
    """调用 GitHub CLI API；只有明确的 404 可当作尚未发布。"""
    args = ["gh", "api", "--hostname", "github.com", path, "--method", method]
    if body is not None:
        args += ["--input", "-"]
    result = subprocess.run(args, input=json.dumps(body) if body is not None else None,
                            capture_output=True, text=True, check=False)
    if result.returncode:
        if missing and "HTTP 404" in result.stderr:
            return None
        raise RuntimeError(result.stderr.strip() or "GitHub API 请求失败")
    return json.loads(result.stdout) if result.stdout.strip() else None


def releases(repo):
    """读取所有发布记录，确保较旧的 latest 标记不会允许发布版本倒退。"""
    page = 1
    found = []
    while True:
        batch = api(f"repos/{repo}/releases?per_page=100&page={page}")
        found.extend(batch)
        if len(batch) < 100:
            return found
        page += 1


def release_decision(version, existing):
    """相同版本不覆盖；只有高于所有已发布稳定版的版本才能进入构建。"""
    tag = "v" + version
    for item in existing:
        if item["tag_name"] == tag and not item["draft"]:
            return False, "此版本已经发布，跳过且不覆盖"
    for item in existing:
        if item["draft"] or item["prerelease"] or not item["tag_name"].startswith("v"):
            continue
        candidate = item["tag_name"][1:]
        try:
            order = compare(version, candidate)
        except ValueError:
            continue
        if order <= 0:
            return False, f"版本未高于已发布稳定版 {candidate}，跳过"
    return True, "检测到尚未发布的新版本"


def plan(args):
    """在默认分支判定新版本；此阶段不修改标签或发布记录。"""
    data = config()
    repo = repository(args.repository)
    info = api(f"repos/{repo}")
    if info["private"]:
        raise ValueError("自动更新使用匿名下载，需要公开仓库及公开 Releases")
    items = releases(repo)
    allowed, reason = release_decision(data["version"], items)
    values = {"publish": str(allowed).lower(), "version": data["version"],
              "tag": "v" + data["version"], "prerelease": str("-" in data["version"]).lower()}
    if args.output:
        with Path(args.output).open("a", encoding="utf-8") as out:
            out.write("".join(f"{key}={value}\n" for key, value in values.items()))
    print(reason + "：" + values["tag"])


def prepare(args):
    """将 CI 的当前仓库写入构建配置，私钥仅从环境读取且不落进源码。"""
    data = config()
    repo = repository(args.repository)
    secret = os.environ.get("MYGO_UPDATER_PRIVATE_KEY", "").strip()
    if not secret:
        raise ValueError("缺少 Repository Secret：MYGO_UPDATER_PRIVATE_KEY")
    if len(base64.b64decode(secret, validate=True)) != 64:
        raise ValueError("MYGO_UPDATER_PRIVATE_KEY 不是 MyGo Ed25519 私钥")
    data["updates"] = {"github": repo, "publicKey": data["updates"]["publicKey"],
                       "tagPrefix": "v", "deltas": 0}
    Path("mygo.json").write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"已配置 {data['version']} 的发布源：{repo}")


def collect(args):
    """仅复制安装包、更新包与安装脚本，Windows 便携包从相同更新归档生成。"""
    data = config()
    version, slug = data["version"], data.get("slug", "glide")
    source = Path("build") / args.target
    destination = Path(args.output)
    destination.mkdir(parents=True, exist_ok=True)
    archive = f"{slug}-{version}-{args.target}.tar.gz"
    required = [archive, f"update-{args.target}.json"]
    if args.target == "darwin-universal":
        required += [f"{slug}-{version}.dmg", "install.sh"]
    else:
        required += [f"{slug}-setup-{version}-{args.target.split('-')[1]}.exe", "install.ps1"]
    for name in required:
        path = source / name
        if not path.is_file() or not path.stat().st_size:
            raise ValueError(f"缺少必须发布的产物：{path}")
        shutil.copy2(path, destination / name)
    if args.target.startswith("windows-"):
        with tarfile.open(source / archive, "r:gz") as tar, zipfile.ZipFile(
                destination / f"{slug}-{version}-{args.target}.zip", "w", zipfile.ZIP_DEFLATED) as out:
            for entry in tar:
                if not entry.isfile():
                    continue
                name = Path(entry.name)
                if name.is_absolute() or ".." in name.parts or "\\" in entry.name:
                    raise ValueError("更新归档包含不安全路径")
                out.writestr(entry.name, tar.extractfile(entry).read())
    print(f"已整理 {args.target} 安装包与签名更新文件")


def merge(args):
    """聚合所有矩阵产物，同名脚本必须一致；补充 Mac 架构清单供旧包升级。"""
    destination = Path(args.output)
    destination.mkdir(parents=True, exist_ok=True)
    for target in TARGETS:
        folder = Path(args.input) / ("release-" + target)
        if not folder.is_dir():
            raise ValueError(f"构建矩阵尚未完成：{target}")
        for path in folder.iterdir():
            if not path.is_file():
                raise ValueError(f"发布目录中出现非文件项：{path}")
            existing = destination / path.name
            if existing.exists() and existing.read_bytes() != path.read_bytes():
                raise ValueError(f"各架构产生了不一致的同名文件：{path.name}")
            shutil.copy2(path, existing)
    universal = destination / "update-darwin-universal.json"
    for arch in ("arm64", "amd64"):
        shutil.copy2(universal, destination / f"update-darwin-{arch}.json")
    lines = []
    for path in sorted(destination.iterdir()):
        if path.name != "SHA256SUMS":
            lines.append(f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n")
    (destination / "SHA256SUMS").write_text("".join(lines), encoding="utf-8")
    print("全部矩阵产物已聚合，Mac 单架构更新入口已指向通用包")


def publish(args):
    """所有产物验证后先上传草稿，完整上传后一次发布，失败可重跑自己的草稿。"""
    data = config()
    repo, version = repository(args.repository), data["version"]
    if not re.fullmatch(r"[0-9a-f]{40}", args.sha):
        raise ValueError("发布必须固定到完整提交 SHA")
    tag = "v" + version
    marker = f"<!-- glide-release:{args.sha} -->"
    allowed, reason = release_decision(version, releases(repo))
    if not allowed:
        raise ValueError("发布前复查失败：" + reason)
    ref = api(f"repos/{repo}/git/ref/tags/{tag}", missing=True)
    if ref is not None:
        obj = ref["object"]
        for _ in range(8):
            if obj["type"] != "tag":
                break
            obj = api(f"repos/{repo}/git/tags/{obj['sha']}")["object"]
        if obj["type"] != "commit" or obj["sha"] != args.sha:
            raise ValueError("现有版本标签没有指向本次构建提交，拒绝生成误标版本")
    release = api(f"repos/{repo}/releases/tags/{tag}", missing=True)
    if release is not None:
        if not release["draft"] or marker not in (release.get("body") or ""):
            raise ValueError("标签对应已有发布或其他草稿，拒绝覆盖")
    else:
        prerelease = "-" in version
        notes = api(f"repos/{repo}/releases/generate-notes", "POST", {"tag_name": tag, "target_commitish": args.sha})
        release = api(f"repos/{repo}/releases", "POST", {
            "tag_name": tag, "target_commitish": args.sha, "name": f"Glide {version}",
            "draft": True, "prerelease": prerelease, "make_latest": "false",
            "body": marker + "\n\n" + notes["body"],
        })
    files = sorted(path for path in Path(args.input).iterdir() if path.is_file())
    if not files or not any(path.name == "SHA256SUMS" for path in files):
        raise ValueError("尚未整理发布产物或 SHA256SUMS")
    # 仅本工具同提交创建的未发布草稿允许覆盖，已发布版本始终被上面的检查拒绝。
    subprocess.run(["gh", "release", "upload", tag, "--repo", repo, "--clobber", *map(str, files)], check=True)
    complete = api(f"repos/{repo}/releases/{release['id']}")
    actual = {asset["name"]: asset for asset in complete["assets"]}
    if set(actual) != {path.name for path in files}:
        raise ValueError("草稿中的产物不完整或包含未知文件，保留草稿供检查")
    for path in files:
        asset = actual[path.name]
        if asset["size"] != path.stat().st_size:
            raise ValueError(f"上传文件尺寸不一致：{path.name}")
        digest = asset.get("digest")
        if digest and digest != "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest():
            raise ValueError(f"上传文件散列不一致：{path.name}")
    api(f"repos/{repo}/releases/{release['id']}", "PATCH", {
        "draft": False, "prerelease": "-" in version, "make_latest": "false" if "-" in version else "true"})
    print(f"已发布 https://github.com/{repo}/releases/tag/{tag}")


def main():
    """提供可独立验证的发布步骤，所有命令失败时均终止流水线。"""
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for name, action in (("plan", plan), ("prepare", prepare), ("publish", publish)):
        command = commands.add_parser(name)
        command.add_argument("--repository", required=True)
        if name == "plan":
            command.add_argument("--output")
        if name == "publish":
            command.add_argument("--sha", required=True)
            command.add_argument("--input", default="release-dist")
        command.set_defaults(action=action)
    command = commands.add_parser("collect")
    command.add_argument("--target", choices=TARGETS, required=True)
    command.add_argument("--output", default="release-out")
    command.set_defaults(action=collect)
    command = commands.add_parser("merge")
    command.add_argument("--input", default="release-artifacts")
    command.add_argument("--output", default="release-dist")
    command.set_defaults(action=merge)
    args = parser.parse_args()
    args.action(args)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"发布失败：{error}", file=sys.stderr)
        sys.exit(1)
