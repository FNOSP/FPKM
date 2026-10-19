#!/usr/bin/env bash
# 一键构建 fpkm 单二进制（含 Web 应用商店界面）
#
# 前置要求：
#   - Go >= 1.24
#   - Node.js >= 18
#
# 用法：
#   ./build.sh            # 构建当前平台
#   ./build.sh linux      # 交叉编译 Linux amd64
#   ./build.sh linux-arm  # 交叉编译 Linux arm64
#   ./build.sh windows    # 交叉编译 Windows amd64
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

TARGET="${1:-native}"
OUT_DIR="${OUT_DIR:-$ROOT/dist}"

# 版本号：优先用环境变量 VERSION，其次取 git tag，最后回退 dev
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
BUILD_TIME="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

mkdir -p "$OUT_DIR"

echo "==> [1/3] 构建前端 (web/)"
# 交给 scripts/build-web.sh：它会处理 dist 被 root 属主占用的情况
"$ROOT/scripts/build-web.sh"

echo "==> [2/3] 校验前端产物已生成"
if [ ! -f internal/web/dist/index.html ]; then
  echo "错误：internal/web/dist/index.html 不存在，前端构建可能失败" >&2
  exit 1
fi

echo "==> [3/3] 编译 Go 二进制 ($TARGET)"
LDFLAGS="-s -w -X 'fpkm/cmd.version=${VERSION}' -X 'fpkm/cmd.buildTime=${BUILD_TIME}'"
echo "==> 版本: ${VERSION}"
case "$TARGET" in
  native)
    go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/fpkm" .
    ;;
  linux)
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/fpkm-linux-amd64" .
    ;;
  linux-arm)
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/fpkm-linux-arm64" .
    ;;
  windows)
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/fpkm-windows-amd64.exe" .
    ;;
  darwin)
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/fpkm-darwin-amd64" .
    ;;
  darwin-arm)
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/fpkm-darwin-arm64" .
    ;;
  release)
    # 发布用：Linux amd64 + arm64（fnOS/NAS 场景最常见）
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/fpkm-linux-amd64" .
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT_DIR/fpkm-linux-arm64" .
    ;;
  *)
    echo "未知目标: $TARGET" >&2
    echo "可用: native | linux | linux-arm | windows | darwin | darwin-arm" >&2
    exit 1
    ;;
esac

echo
echo "构建完成："
ls -lh "$OUT_DIR" | tail -n +2
echo
echo "启动 Web 应用商店："
echo "  $OUT_DIR/fpkm web                 # 仅本机访问（首次打开设置管理密码）"
echo "  $OUT_DIR/fpkm web --bind 0.0.0.0  # 局域网访问"
echo "  $OUT_DIR/fpkm web --reset-password # 忘记密码时清除"
