"""开发机打包器；手机端只运行打入 ZIP 的 Go 二进制。"""
from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
import re
import struct
import subprocess
import zipfile

HERE = Path(__file__).resolve().parent
CORE = HERE.parent / "rikka-mobile-v4-core"
BIN = HERE / "bin" / "moluo-linux-arm64"


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def require_version_contracts(version: str) -> None:
    """Require one exact current-version declaration in every shipped contract."""
    contracts = {
        "SKILL.md": [
            r"^当前程序版本：`([^`]+)`（候选版，尚未完成安卓真机集中验收）。`version` 返回的版本必须与包内 `VERSION` 一致。$",
            r"^## 29 个用户可用命令索引（与 ([^ ]+) 源码一致）$",
        ],
        "README.md": [
            r"^# 墨络 v4 手机本地候选包（([^，]+)，RC，未通过 Android 真机验收）$",
            r"^当前\*\*候选版本\*\*为 \*\*([^*]+)\*\*：`dist/moluo-v4-local-android-candidate-([^`]+)\.zip`。",
        ],
        "ANDROID_CONCENTRATED_TEST.md": [
            r"^> 目标候选版本：`([^`]+)`；安装后的真实 `version`、包内 `VERSION` 与最终交付记录必须完全一致，否则立即停止。$",
            r"^> \*\*([^ ]+) 增量验收项（同一虚构作品，按顺序执行）：\*\*$",
        ],
    }
    for name, patterns in contracts.items():
        text = (HERE / name).read_text(encoding="utf-8")
        for pattern in patterns:
            matches = re.findall(pattern, text, flags=re.MULTILINE)
            if len(matches) != 1:
                raise RuntimeError(f"{name} 当前版本声明缺失或重复，拒绝打包: {pattern}")
            groups = matches[0] if isinstance(matches[0], tuple) else (matches[0],)
            if any(group != version for group in groups):
                raise RuntimeError(f"{name} 当前版本声明与二进制 {version} 不一致: {groups}")


def require_command_contract() -> None:
    """Compare the shipped command table with the actual CLI dispatch."""
    main = (CORE / "main.go").read_text(encoding="utf-8")
    cli = set(re.findall(r'case "([a-z0-9-]+)"', main))
    cli.discard("append")  # append is a refusal-only compatibility entry
    cli.add("version")     # handled before flag parsing
    skill = set(re.findall(r"^\| `([a-z0-9-]+)`", (HERE / "SKILL.md").read_text(encoding="utf-8"), flags=re.MULTILINE))
    if cli != skill:
        raise RuntimeError(f"SKILL 命令表与 CLI 不一致: 缺少={sorted(cli - skill)} 多余={sorted(skill - cli)}")


def main() -> None:
    go = os.environ.get("MOLUO_GO", "go")
    BIN.parent.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, CGO_ENABLED="0", GOOS="linux", GOARCH="arm64")
    subprocess.run([go, "build", "-trimpath", "-ldflags=-s -w", "-o", str(BIN), "."], cwd=CORE, env=env, check=True)
    header = BIN.read_bytes()[:20]
    if header[:4] != b"\x7fELF" or header[4] != 2 or struct.unpack("<H", header[18:20])[0] != 183:
        raise RuntimeError("生成物不是 64 位 Linux ARM64 ELF")
    match = re.search(r'const buildVersion = "([^"]+)"', (CORE / "main.go").read_text(encoding="utf-8"))
    if match is None:
        raise RuntimeError("找不到手机核心程序版本")
    version = match.group(1)
    # All three user-facing contracts must have one exact current-version
    # declaration. A loose substring check previously allowed the Android test
    # prompt to keep a stale version and obsolete behavior in a newer ZIP.
    require_version_contracts(version)
    require_command_contract()
    out = HERE / "dist" / f"moluo-v4-local-android-candidate-{version}.zip"
    (HERE / "VERSION").write_bytes((version + "\n").encode("ascii"))
    checked_names = ["SKILL.md", "README.md", "ANDROID_CONCENTRATED_TEST.md", "install.sh", "VERSION", "bin/moluo-linux-arm64"]
    checksums = "".join(f"{sha(HERE / name)}  {name}\n" for name in checked_names)
    (HERE / "SHA256SUMS").write_bytes(checksums.encode("ascii"))
    names = ["SKILL.md", "README.md", "ANDROID_CONCENTRATED_TEST.md", "install.sh", "VERSION", "SHA256SUMS", "bin/moluo-linux-arm64"]
    manifest = {"format": "moluo-v4-local-package", "version": version, "target": "RikkaHub 3.3.1 + Linux arm64 Rootfs", "files": {name: sha(HERE / name) for name in names}}
    (HERE / "PACKAGE_MANIFEST.json").write_bytes((json.dumps(manifest, ensure_ascii=False, indent=2) + "\n").encode("utf-8"))
    names.append("PACKAGE_MANIFEST.json")
    out.parent.mkdir(parents=True, exist_ok=True)
    # A versioned ZIP is immutable once produced. Rebuilding identical bytes is
    # idempotent; changed content needs a new version instead of replacing history.
    staged = out.with_suffix(".zip.tmp")
    if staged.exists():
        raise RuntimeError(f"打包临时路径已占用: {staged}")
    with zipfile.ZipFile(staged, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        for name in names:
            info = zipfile.ZipInfo(name, date_time=(2026, 11, 22, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (0o755 if name == "install.sh" else 0o644) << 16
            archive.writestr(info, (HERE / name).read_bytes())
    with zipfile.ZipFile(staged) as archive:
        if archive.testzip() is not None or sorted(archive.namelist()) != sorted(names):
            raise RuntimeError("ZIP 结构损坏")
        for name, digest in manifest["files"].items():
            if hashlib.sha256(archive.read(name)).hexdigest() != digest:
                raise RuntimeError(f"ZIP 文件哈希错误: {name}")
    if out.exists():
        if sha(out) != sha(staged):
            staged.unlink()
            raise RuntimeError(f"已有同版本 ZIP 内容不同，拒绝覆盖: {out}")
        staged.unlink()
    else:
        os.replace(staged, out)
    print(json.dumps({"path": str(out), "sha256": sha(out), "files": len(names)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
