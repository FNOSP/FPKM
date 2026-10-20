#!/usr/bin/env bash
# 构建并（可选）推送 FPKM Docker 镜像
#
# 用法：
#   ./scripts/build-image.sh                    # 构建 amd64 + arm64，不推送
#   ./scripts/build-image.sh --push             # 构建并推送到 Docker Hub
#   ./scripts/build-image.sh --push --user=x    # 指定账号
#
# 前置：
#   1. 已执行 ./build.sh release 产出 dist/fpkm-linux-{amd64,arm64}
#   2. 推送前已 docker login
#   3. 使用 buildx 才能构建多架构（需 docker buildx create 一次）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

USER_NAME="qilinzhu"
IMAGE="fpkm"
VERSION="${VERSION:-1.0.1}"
PUSH=0

for arg in "$@"; do
  case "$arg" in
    --push) PUSH=1 ;;
    --user=*) USER_NAME="${arg#*=}" ;;
    --version=*) VERSION="${arg#*=}" ;;
    *) echo "未知参数: $arg" >&2; exit 1 ;;
  esac
done

# 校验二进制存在（两个架构都要，多架构镜像需要）
for a in amd64 arm64; do
  f="dist/fpkm-linux-$a"
  if [ ! -f "$f" ]; then
    echo "缺少 $f —— 请先执行: ./build.sh release" >&2
    exit 1
  fi
done

TAGS=(-t "${USER_NAME}/${IMAGE}:${VERSION}" -t "${USER_NAME}/${IMAGE}:latest")

echo "==> 构建多架构镜像 ${USER_NAME}/${IMAGE}:${VERSION} (amd64, arm64)"
set -x
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --build-arg FPKM_VERSION="${VERSION}" \
  "${TAGS[@]}" \
  $( [ "$PUSH" -eq 1 ] && echo --push || echo --load ) \
  .
set +x

if [ "$PUSH" -eq 1 ]; then
  echo
  echo "已推送："
  echo "  docker pull ${USER_NAME}/${IMAGE}:${VERSION}"
  echo "  docker pull ${USER_NAME}/${IMAGE}:latest"
else
  echo
  echo "本地构建完成（未推送）。推送请加 --push"
fi
