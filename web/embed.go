// Package web 把模板与静态资源嵌入到二进制文件中，
// 使最终产物是一个无需附带任何文件的独立可执行程序。
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sort"
	"sync"
)

// FS 包含全部前端资源：
//
//	templates/  服务端渲染的 html/template 模板
//	static/     CSS / JavaScript / 图标
//
//go:embed templates static
var FS embed.FS

// staticVersion 是 static/ 下所有文件「路径 + 内容」的哈希摘要。
//
// 它的存在是为了给资源 URL 加指纹（/static/js/app.js?v=xxxx）。
// 没有指纹时只能靠 max-age 缓存，而一旦被缓存，二进制更新后浏览器仍会
// 继续使用旧文件 —— 又因为没有 ETag / Last-Modified，连重新验证的机会
// 都没有，表现为「服务端明明改了，前端功能却还是旧的」。
// 有了指纹，内容一变 URL 就变，浏览器会当成全新资源拉取，
// 同时也就能够安全地长期缓存了。
var staticVersion = sync.OnceValue(func() string {
	sub, err := fs.Sub(FS, "static")
	if err != nil {
		return "0"
	}

	var paths []string
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	// 排序后再计算，保证结果与遍历顺序无关
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			continue
		}
		_, _ = h.Write([]byte(p))
		_, _ = h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
})

// StaticVersion 返回静态资源的版本标识（内容哈希前 12 位），
// 供模板给 /static/ 下的 URL 加查询参数。
func StaticVersion() string { return staticVersion() }
