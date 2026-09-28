// Package web 把模板与静态资源嵌入到二进制文件中，
// 使最终产物是一个无需附带任何文件的独立可执行程序。
package web

import "embed"

// FS 包含全部前端资源：
//
//	templates/  服务端渲染的 html/template 模板
//	static/     CSS / JavaScript / 图标
//
//go:embed templates static
var FS embed.FS
