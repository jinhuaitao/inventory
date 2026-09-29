package web

import (
	"bytes"
	"encoding/hex"
	"html/template"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"inventory/internal/utils"
)

// 站点的 CSP 由 middleware.SecurityHeaders 设定，script-src 只有 'self'：
// 没有 'unsafe-inline'，也没有 nonce / hash。因此内联脚本、内联事件处理器
// 与 javascript: 伪协议都会被浏览器直接拒绝执行。
//
// 这类问题的失败是**静默**的 —— 控制台报错、页面看起来完全正常，
// 只是功能不动（倒计时不走、按钮点了没反应），极易漏掉，所以用测试钉住。
var (
	scriptTagRe   = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	inlineEventRe = regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`)
	jsURLRe       = regexp.MustCompile(`(?i)(?:href|src|action)\s*=\s*["']\s*javascript:`)
	staticRefRe   = regexp.MustCompile(`(?i)(?:href|src)\s*=\s*"(/static/[^"]*)"`)
)

// StaticVersion 是静态资源 URL 的指纹来源，必须稳定且非空，
// 否则所有带 ?v= 的 URL 会退化成同一个值，等于没有指纹。
func TestStaticVersionIsStableAndNonEmpty(t *testing.T) {
	v := StaticVersion()
	if len(v) != 12 {
		t.Fatalf("StaticVersion() = %q，期望 12 位十六进制", v)
	}
	if _, err := hex.DecodeString(v); err != nil {
		t.Fatalf("StaticVersion() = %q 不是合法的十六进制: %v", v, err)
	}
	if again := StaticVersion(); again != v {
		t.Errorf("两次调用结果不一致: %q vs %q", v, again)
	}
}

// 静态资源按 max-age=31536000, immutable 下发，所以模板里的每一个
// /static/ 引用都必须带内容指纹；漏掉一个，那个文件就会被浏览器
// 永久缓存，二进制升级后也拿不到新版本。
func TestStaticReferencesAreFingerprinted(t *testing.T) {
	err := fs.WalkDir(FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		raw, err := fs.ReadFile(FS, p)
		if err != nil {
			return err
		}
		for _, m := range staticRefRe.FindAllStringSubmatch(string(raw), -1) {
			if !strings.Contains(m[1], "?v=") {
				t.Errorf("%s: 静态资源引用缺少内容指纹: %s", p, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历模板失败: %v", err)
	}
}

func TestTemplatesHaveNoCSPBlockedInlineScript(t *testing.T) {
	err := fs.WalkDir(FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		raw, err := fs.ReadFile(FS, p)
		if err != nil {
			return err
		}
		body := string(raw)

		for _, tag := range scriptTagRe.FindAllString(body, -1) {
			if !strings.Contains(tag, "src=") {
				t.Errorf("%s: 内联脚本会被 CSP 拦下，请改为外链脚本 + data-* 属性驱动: %s", p, tag)
			}
		}
		if m := inlineEventRe.FindString(body); m != "" {
			t.Errorf("%s: 内联事件处理器会被 CSP 拦下: %s", p, strings.TrimSpace(m))
		}
		if m := jsURLRe.FindString(body); m != "" {
			t.Errorf("%s: javascript: 伪协议会被 CSP 拦下: %s", p, strings.TrimSpace(m))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历模板失败: %v", err)
	}
}

// pageStub 只保留 content 块需要的字段，避免为了渲染而构造完整的 PageData。
type pageStub struct {
	Data any
}

// 「更新完成 / 数据恢复」两个等待页的自动刷新完全依赖 app.js 读取
// data-restart-* 属性，属性名或取值一旦改动就会静默失效，这里做渲染级校验。
func TestRestartPagesCarryAutoRefreshAttributes(t *testing.T) {
	cases := []struct {
		page     string
		data     map[string]any
		wantHref string
	}{
		{
			page: "templates/update/restarting.html",
			data: map[string]any{
				"From":      "v1.0.1",
				"To":        "v1.0.2",
				"Backup":    "inventory-server.old",
				"Refresh":   "/admin/update",
				"Countdown": 5,
			},
			wantHref: "/admin/update",
		},
		{
			page: "templates/maintenance/restoring.html",
			data: map[string]any{
				"Source": "backup.db",
				"Size":   int64(2048),
				"Counts": map[string]int64{
					"products": 1, "categories": 2, "suppliers": 3, "users": 4,
				},
				"Refresh":   "/admin/maintenance",
				"Countdown": 5,
			},
			wantHref: "/admin/maintenance",
		},
	}

	for _, tc := range cases {
		t.Run(tc.page, func(t *testing.T) {
			tpl, err := template.New("layout.html").
				Funcs(template.FuncMap{"fileSize": utils.FormatBytes}).
				ParseFS(FS, "templates/layout.html", "templates/partials/*.html", tc.page)
			if err != nil {
				t.Fatalf("解析模板失败: %v", err)
			}

			var buf bytes.Buffer
			if err := tpl.ExecuteTemplate(&buf, "content", pageStub{Data: tc.data}); err != nil {
				t.Fatalf("渲染模板失败: %v", err)
			}
			out := buf.String()

			for _, want := range []string{
				`data-restart-target="` + tc.wantHref + `"`,
				`data-restart-probe="/readyz"`,
				`data-restart-seconds="5"`,
				`data-restart-countdown`,
				`data-restart-hint`,
			} {
				if !strings.Contains(out, want) {
					t.Errorf("渲染结果缺少 %s —— 自动刷新由 app.js 依据这些属性驱动", want)
				}
			}
			if strings.Contains(out, "<script") {
				t.Error("等待页不应包含内联脚本：CSP 为 script-src 'self'，会被浏览器拒绝执行")
			}
		})
	}
}
