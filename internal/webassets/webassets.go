// Package webassets 内嵌前端构建产物，使桌面端能产出单个 exe。
//
// 资源缺失时不直接编译失败：Round 1 关口要能在前端尚未构建时先跑通 Go 侧
// （装配、会话、启动 token），因此嵌入一个占位目录并由 serveStatic 给出
// 可执行的修复指引，而不是让 go:embed 报 "no matching files"。
package webassets

import (
	"embed"
	"io/fs"
)

// dist 下的内容由 `npm run build` 生成并被 .gitignore 排除；
// 提交进仓库的只有下面这个占位文件，用于让 embed 在未构建时也能编译。
//
//go:embed all:dist
var embedded embed.FS

// FS 返回内嵌资源视图，资源位于其 "dist" 子目录下。
func FS() fs.FS { return embedded }

// Dir 是资源在 FS 内的前缀。
const Dir = "dist"
