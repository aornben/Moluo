#!/bin/sh
set -eu
BASE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "$(uname -s)" != "Linux" ] || [ "$(uname -m)" != "aarch64" ]; then
  echo '严重异常：当前包只支持 RikkaHub Rootfs 的 Linux arm64（aarch64）。' >&2
  exit 2
fi
if [ ! -d /workspace ]; then
  echo '严重异常：RikkaHub 工作区 /workspace 不存在；请先绑定独立的小说工作区。' >&2
  exit 2
fi
if ! command -v sha256sum >/dev/null 2>&1; then
  echo '严重异常：Rootfs 缺少 sha256sum，无法核验程序。' >&2
  exit 2
fi
cd "$BASE"
if ! sha256sum -c SHA256SUMS; then
  echo '严重异常：程序哈希不符；停止安装，不读取或改写小说数据。' >&2
  exit 2
fi
RUNTIME=/workspace/moluo-v4-runtime/bin
if [ -L /workspace/moluo-v4-runtime ] || [ -L "$RUNTIME" ]; then
  echo '严重异常：runtime 路径含符号链接，拒绝安装。' >&2
  exit 2
fi
mkdir -p "$RUNTIME"
CURRENT=$RUNTIME/moluo
STAGED=$RUNTIME/moluo.tmp.$$
PREVIOUS=$RUNTIME/moluo.previous.$$
if [ -e "$STAGED" ] || [ -L "$STAGED" ] || [ -e "$PREVIOUS" ] || [ -L "$PREVIOUS" ]; then
  echo '严重异常：安装临时路径已被占用，拒绝覆盖。' >&2
  exit 2
fi
COMMITTED=0
MOVED=0
HAD_PREVIOUS=0
cleanup() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ "$COMMITTED" -ne 1 ] && [ "$MOVED" -eq 1 ]; then
    if [ "$HAD_PREVIOUS" -eq 1 ]; then
      if ! mv -f "$PREVIOUS" "$CURRENT"; then
        echo "严重异常：旧程序自动回退失败，旧副本保留在 $PREVIOUS；停止使用该 runtime。" >&2
      fi
    else
      rm -f "$CURRENT"
    fi
  fi
  rm -f "$STAGED"
  if [ "$COMMITTED" -eq 1 ] || [ "$MOVED" -eq 0 ]; then rm -f "$PREVIOUS"; fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 2' HUP INT TERM
cp bin/moluo-linux-arm64 "$STAGED"
chmod 700 "$STAGED"
EXPECTED=''
while read -r DIGEST FILE; do
  if [ "$FILE" = 'bin/moluo-linux-arm64' ]; then
    EXPECTED=$DIGEST
  fi
done < SHA256SUMS
if [ -z "$EXPECTED" ]; then
  echo '严重异常：安装包缺少程序哈希。' >&2
  exit 2
fi
ACTUAL=$(sha256sum "$STAGED" | cut -d ' ' -f 1)
if [ "$EXPECTED" != "$ACTUAL" ]; then
  echo '严重异常：安装后的程序哈希不符。' >&2
  exit 2
fi
if ! OUTPUT=$("$STAGED" version 2>&1); then
  echo "严重异常：程序无法在当前 Rootfs 执行：$OUTPUT" >&2
  exit 2
fi
VERSION=$(cat VERSION)
if ! printf '%s\n' "$OUTPUT" | grep -F '"version":"'"$VERSION"'"' >/dev/null; then
  echo '严重异常：程序版本与安装包不一致。' >&2
  exit 2
fi
if [ -e "$CURRENT" ] || [ -L "$CURRENT" ]; then
  if [ ! -f "$CURRENT" ] || [ -L "$CURRENT" ]; then
    echo '严重异常：旧 runtime 不是普通文件；拒绝替换。' >&2
    exit 2
  fi
  cp -p "$CURRENT" "$PREVIOUS"
  HAD_PREVIOUS=1
fi
mv -f "$STAGED" "$CURRENT"
MOVED=1
if ! POST=$("$CURRENT" version 2>&1) || [ "$POST" != "$OUTPUT" ]; then
  echo '严重异常：安装后程序验证失败，正在恢复旧 runtime。' >&2
  exit 2
fi
COMMITTED=1
printf '%s\n' "$OUTPUT"
