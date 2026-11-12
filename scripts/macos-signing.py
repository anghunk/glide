#!/usr/bin/env python3
"""复用 Glide 的固定自签名身份，在本机或 macOS CI 中准备代码签名。"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import shlex
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]
LOCAL = Path.home() / "Library/Application Support/Glide/release-keys/macos-signing"


def run(args, *, data=None, env=None):
    """执行系统命令；失败时不输出可能包含密码的参数或命令输出。"""
    result = subprocess.run(args, input=data, env=env, capture_output=True)
    if result.returncode:
        raise RuntimeError(f"{Path(args[0]).name} {args[1]} 失败（退出码 {result.returncode}）")
    return result.stdout


def config():
    """读取已固定的证书指纹及发布仓库。"""
    return json.loads((ROOT / "mygo.json").read_text())


def validate(p12, password):
    """从加密备份中提取公证书，拒绝与项目指纹不一致的签名身份。"""
    env = {**os.environ, "GLIDE_P12_PASSWORD": password}
    pem = run(["/usr/bin/openssl", "pkcs12", "-in", str(p12), "-clcerts", "-nokeys",
               "-passin", "env:GLIDE_P12_PASSWORD"], env=env)
    der = run(["/usr/bin/openssl", "x509", "-outform", "DER"], data=pem)
    fingerprint = hashlib.sha1(der).hexdigest().upper()
    if fingerprint != config()["macos"]["signingIdentity"]:
        raise RuntimeError("证书与 mygo.json 固定指纹不匹配；请恢复原证书，不要生成替代身份")
    return run(["/usr/bin/openssl", "x509", "-outform", "PEM"], data=pem)


def folder():
    """CI 使用运行器的临时目录，本地密钥始终留在工作树外。"""
    if os.environ.get("GITHUB_ACTIONS") == "true":
        return Path(os.environ["RUNNER_TEMP"]) / "glide-macos-signing"
    return LOCAL


def prepare():
    """导入并解锁专用钥匙串，只信任该证书的代码签名用途。"""
    target = folder()
    target.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(target, 0o700)
    ci = os.environ.get("GITHUB_ACTIONS") == "true"
    if ci:
        encoded = os.environ.get("MACOS_SIGNING_P12_BASE64", "")
        password = os.environ.get("MACOS_SIGNING_P12_PASSWORD", "")
        if not encoded or not password:
            raise RuntimeError("缺少 MACOS_SIGNING_P12_BASE64 / MACOS_SIGNING_P12_PASSWORD，拒绝回退到临时签名")
        (target / "identity.p12").write_bytes(base64.b64decode(encoded, validate=True))
        if not (target / "keychain-password").exists():
            (target / "keychain-password").write_text(secrets.token_urlsafe(32))
    else:
        password = (target / "p12-password").read_text().strip()
    cert = target / "certificate.pem"
    cert.write_bytes(validate(target / "identity.p12", password))
    keychain = str(target / "glide-signing.keychain-db")
    keypass = (target / "keychain-password").read_text().strip()
    if not Path(keychain).exists():
        run(["security", "create-keychain", "-p", keypass, keychain])
        run(["security", "unlock-keychain", "-p", keypass, keychain])
        run(["security", "import", str(target / "identity.p12"), "-k", keychain,
             "-P", password, "-T", "/usr/bin/codesign"])
        run(["security", "set-key-partition-list", "-S", "apple-tool:,apple:", "-s",
             "-k", keypass, keychain])
    run(["security", "set-keychain-settings", "-t", "21600", keychain])
    run(["security", "unlock-keychain", "-p", keypass, keychain])
    existing = shlex.split(run(["security", "list-keychains", "-d", "user"]).decode())
    if keychain not in existing:
        run(["security", "list-keychains", "-d", "user", "-s", *existing, keychain])
    if ci:
        run(["sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot",
             "-p", "codeSign", "-k", keychain, str(cert)])
    identity_args = ["security", "find-identity", "-v", "-p", "codesigning", keychain]
    identities = run(identity_args).decode()
    if not ci and config()["macos"]["signingIdentity"] not in identities:
        run(["security", "add-trusted-cert", "-r", "trustRoot", "-p", "codeSign",
             "-k", keychain, str(cert)])
        identities = run(identity_args).decode()
    if config()["macos"]["signingIdentity"] not in identities:
        raise RuntimeError("证书尚未被信任用于代码签名；请完成系统信任确认后重试")
    print("固定代码签名身份已就绪：Glide Local Signing")


def sync():
    """将已校验的同一份加密证书备份同步到配置仓库的 Actions Secrets。"""
    p12 = LOCAL / "identity.p12"
    password = (LOCAL / "p12-password").read_text().strip()
    validate(p12, password)
    repo = config()["updates"]["github"]
    for name, value in {
        "MACOS_SIGNING_P12_BASE64": base64.b64encode(p12.read_bytes()),
        "MACOS_SIGNING_P12_PASSWORD": password.encode(),
    }.items():
        run(["gh", "secret", "set", name, "--repo", repo], data=value)
    print(f"已同步 {repo} 的两个 macOS 签名 Secrets；未输出私钥或密码")


def clean():
    """只清理 CI 创建的专用钥匙串与临时密钥，不操作本机备份。"""
    if os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("清理命令仅适用于 CI")
    target = folder()
    cert = target / "certificate.pem"
    if cert.exists():
        run(["sudo", "security", "remove-trusted-cert", "-d", str(cert)])
    keychain = target / "glide-signing.keychain-db"
    if keychain.exists():
        run(["security", "delete-keychain", str(keychain)])
    if target.exists():
        import shutil
        shutil.rmtree(target)


def main():
    """显式选择准备、同步或清理操作；任何操作均不重新生成证书。"""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["prepare", "sync", "clean", "identity"])
    args = parser.parse_args()
    os.umask(0o077)
    if args.action == "identity":
        print(config()["macos"]["signingIdentity"])
        return
    if sys.platform != "darwin":
        raise RuntimeError("此操作需要 macOS")
    {"prepare": prepare, "sync": sync, "clean": clean}[args.action]()


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError) as error:
        print(f"配置 macOS 签名失败：{error}", file=sys.stderr)
        sys.exit(1)
