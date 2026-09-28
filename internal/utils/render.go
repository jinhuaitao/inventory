package utils

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Renderer 负责把 html/template 模板渲染为 HTTP 响应。
//
// 模板通过 embed.FS 嵌入二进制，因此运行时不依赖任何外部文件。
// 生产模式下每个页面只解析一次并缓存；开发模式下每次请求重新解析，
// 方便直接修改模板即时生效。
type Renderer struct {
	fsys   fs.FS
	dev    bool
	logger *slog.Logger

	mu    sync.RWMutex
	cache map[string]*template.Template
}

// NewRenderer 创建渲染器并预编译全部页面模板。
func NewRenderer(fsys fs.FS, dev bool, logger *slog.Logger) (*Renderer, error) {
	r := &Renderer{
		fsys:   fsys,
		dev:    dev,
		logger: logger,
		cache:  make(map[string]*template.Template),
	}

	pages, err := r.discoverPages()
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("未在 templates 目录下发现任何页面模板")
	}

	for _, p := range pages {
		if _, err := r.compile(p); err != nil {
			return nil, fmt.Errorf("编译模板 %s 失败: %w", p, err)
		}
	}

	if logger != nil {
		logger.Debug("模板预编译完成", "页面数", len(pages))
	}
	return r, nil
}

// discoverPages 遍历 templates 目录，收集所有页面模板（排除布局与局部模板）。
func (r *Renderer) discoverPages() ([]string, error) {
	var pages []string
	err := fs.WalkDir(r.fsys, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		rel := strings.TrimPrefix(p, "templates/")
		if rel == "layout.html" || strings.HasPrefix(rel, "partials/") {
			return nil
		}
		pages = append(pages, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("扫描模板目录失败: %w", err)
	}
	return pages, nil
}

// compile 为单个页面构建模板集：布局 + 局部模板 + 页面本身。
func (r *Renderer) compile(page string) (*template.Template, error) {
	t, err := template.New("layout.html").
		Funcs(r.funcMap()).
		ParseFS(r.fsys,
			"templates/layout.html",
			"templates/partials/*.html",
			"templates/"+page,
		)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.cache[page] = t
	r.mu.Unlock()
	return t, nil
}

// templateFor 取出（或按需编译）页面模板。
func (r *Renderer) templateFor(page string) (*template.Template, error) {
	if r.dev {
		return r.compile(page)
	}

	r.mu.RLock()
	t, ok := r.cache[page]
	r.mu.RUnlock()
	if ok {
		return t, nil
	}
	return r.compile(page)
}

// Render 渲染页面并写入响应。渲染先在内存缓冲中完成，
// 只有成功后才写出，避免出错时返回半截页面。
func (r *Renderer) Render(w http.ResponseWriter, status int, page string, data any) {
	t, err := r.templateFor(page)
	if err != nil {
		r.serverError(w, page, err)
		return
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		r.serverError(w, page, err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil && r.logger != nil {
		r.logger.Error("写出响应失败", "页面", page, "错误", err)
	}
}

// RenderString 直接渲染一段模板字符串（用于邮件等场景）。
func (r *Renderer) RenderString(page string, data any) (string, error) {
	t, err := r.templateFor(page)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (r *Renderer) serverError(w http.ResponseWriter, page string, err error) {
	if r.logger != nil {
		r.logger.Error("渲染模板失败", "页面", page, "错误", err)
	}
	http.Error(w, "服务器内部错误，请稍后重试", http.StatusInternalServerError)
}

// textToHTML 把纯文本转换为安全的 HTML：先转义，再把换行变成 <br>。
// 用于展示来自 GitHub Release 的说明文本。
func textToHTML(s string) template.HTML {
	escaped := template.HTMLEscapeString(strings.ReplaceAll(s, "\r\n", "\n"))
	escaped = strings.ReplaceAll(escaped, "\n", "<br>")
	//nolint:gosec // 内容已在上一步完成 HTML 转义
	return template.HTML(escaped)
}

// funcMap 提供模板中可用的辅助函数。
func (r *Renderer) funcMap() template.FuncMap {
	return template.FuncMap{
		// 时间
		"formatDateTime":    FormatDateTime,
		"formatTime":        FormatTime,
		"formatDate":        FormatDate,
		"formatDateTimePtr": FormatDateTimePtr,
		"relativeTime":      RelativeTime,
		"now":               time.Now,

		// 数字与金额
		"money":      FormatMoney,
		"moneyPlain": FormatMoneyPlain,
		"num":        FormatNumber,
		"float":      FormatFloat,
		"percent":    Percent,
		"ratio":      Ratio,
		"barHeight":  BarHeight,
		"pct": func(part, total float64) float64 {
			if total <= 0 {
				return 0
			}
			v := part / total * 100
			if v < 0 {
				return 0
			}
			if v > 100 {
				return 100
			}
			return v
		},
		"add": Add,
		"sub": Sub,
		"mul": Mul,
		"max": Max,
		"min": Min,
		"seq": Seq,

		// 字符串
		"truncate": Truncate,
		"initial":  Initial,
		"upper":    strings.ToUpper,
		"lower":    strings.ToLower,
		"trim":     strings.TrimSpace,
		"default":  DefaultString,
		"hasPrefix": func(s, prefix string) bool {
			return strings.HasPrefix(s, prefix)
		},
		"join": strings.Join,

		// 文件体积（用于更新页展示安装包大小）
		"fileSize": FormatBytes,
		"nl2br":    func(s string) template.HTML { return textToHTML(s) },

		// 逻辑
		"eqStr": func(a, b string) bool { return a == b },
		"dict": func(values ...any) map[string]any {
			m := make(map[string]any, len(values)/2)
			for i := 0; i+1 < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					continue
				}
				m[key] = values[i+1]
			}
			return m
		},
	}
}
