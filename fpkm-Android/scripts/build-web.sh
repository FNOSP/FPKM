#!/usr/bin/env bash
# 构建前端，并处理 dist 目录被 root 属主占用的常见问题。
#
# 背景：如果 fpkm web 曾以 root 身份运行过，Vite 的 emptyOutDir 会因为
# dist/assets 属于 root 而失败（EACCES）。由于父目录仍归当前用户所有，
# 可以把整个 dist 重命名到一边，再重建同名空目录后继续构建。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="$ROOT/internal/web/dist"

# 探测 dist 是否可被 Vite 清空。
#
# 注意：只测 dist 自身是否可写是不够的 —— fpkm web 以 root 运行后，
# dist/ 可能仍归当前用户，但 dist/assets/ 变成 root 属主，
# 此时 Vite 的 emptyOutDir 删不掉 assets 里的文件而报 EACCES。
# 因此对 dist 下的每个子目录都做一次"能否删除文件"的探测。
needs_reset=0
if [ -d "$DIST" ]; then
  if ! ( : > "$DIST/.write-probe" ) 2>/dev/null; then
    needs_reset=1
  else
    rm -f "$DIST/.write-probe" 2>/dev/null || true
    for sub in "$DIST"/*/; do
      [ -d "$sub" ] || continue
      probe="$sub.write-probe"
      if ! ( : > "$probe" ) 2>/dev/null; then
        needs_reset=1
      else
        rm -f "$probe" 2>/dev/null || true
      fi
    done
  fi
fi

if [ "$needs_reset" = 1 ]; then
  echo "==> dist 或其子目录不可写（可能被 root 属主占用），重命名后重建"
  mv "$DIST" "$DIST.stale-$(date +%s)"
fi

mkdir -p "$DIST"
if [ ! -f "$DIST/index.html" ]; then
  cat > "$DIST/index.html" <<'HTML'
<!doctype html>
<html lang="zh-CN"><head><meta charset="UTF-8"><title>FPKM 应用商店</title></head>
<body><div id="root">前端产物未构建。请在 web/ 目录执行 npm install &amp;&amp; npm run build。</div></body></html>
HTML
fi
rm -f "$DIST/.write-probe" 2>/dev/null || true
chmod 755 "$DIST" 2>/dev/null || true

echo "==> 构建前端"
cd "$ROOT/web"
if [ ! -d node_modules ]; then
  echo "    安装依赖..."
  npm install
fi
# 先做类型检查再打包。
#
# 为什么必须显式加这一步：package.json 的 build 只是 `vite build`，
# Vite 用 esbuild 转译时**不做类型检查**，类型错误会被静默忽略并产出
# "能跑但有 bug" 的产物。历史上有几次改动因此漏过了检查。
echo "    类型检查 (tsc --noEmit)"
npx tsc --noEmit

npm run build

echo "==> 完成：$DIST"
ls -la "$DIST"
