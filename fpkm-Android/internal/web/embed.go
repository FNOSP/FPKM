package web

import "embed"

// distFS 保存前端构建产物。
// 由 web/ 前端工程执行 `npm run build` 生成到 internal/web/dist。
// 使用 all: 前缀以确保包含以下划线或点开头的文件。
//
//go:embed all:dist
var distFS embed.FS
