package auth

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"inventory/internal/models"
)

// multipartRequest 构造一个 multipart/form-data 请求：一个可选表单字段 + 一个文件字段。
func multipartRequest(t *testing.T, field, value string) *http.Request {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if field != "" {
		if err := mw.WriteField(field, value); err != nil {
			t.Fatalf("写入表单字段失败: %v", err)
		}
	}
	part, err := mw.CreateFormFile("backup", "inventory.db")
	if err != nil {
		t.Fatalf("创建文件字段失败: %v", err)
	}
	if _, err := part.Write([]byte("SQLite format 3\x00payload")); err != nil {
		t.Fatalf("写入文件内容失败: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/restore", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// TestProvidedCSRFToken 覆盖令牌提取的各条分支。
//
// 重点在 multipart：ParseForm 不解析 multipart 的 body，早先的实现因此
// 读不到文件上传表单里的 _csrf，所有上传都被误判为「缺少 CSRF 令牌」。
func TestProvidedCSRFToken(t *testing.T) {
	const token = "token-abc123"

	t.Run("multipart 读取 _csrf 字段", func(t *testing.T) {
		if got := ProvidedCSRFToken(multipartRequest(t, FormFieldName, token)); got != token {
			t.Fatalf("应取到 %q，实际 %q", token, got)
		}
	})

	t.Run("multipart 无 _csrf 字段返回空", func(t *testing.T) {
		if got := ProvidedCSRFToken(multipartRequest(t, "", "")); got != "" {
			t.Fatalf("应返回空字符串，实际 %q", got)
		}
	})

	t.Run("multipart 字段名不匹配返回空", func(t *testing.T) {
		if got := ProvidedCSRFToken(multipartRequest(t, "csrf", token)); got != "" {
			t.Fatalf("应返回空字符串，实际 %q", got)
		}
	})

	t.Run("urlencoded 读取 _csrf 字段", func(t *testing.T) {
		form := url.Values{FormFieldName: {token}}
		req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/backup",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		if got := ProvidedCSRFToken(req); got != token {
			t.Fatalf("应取到 %q，实际 %q", token, got)
		}
	})

	t.Run("请求头优先于表单字段", func(t *testing.T) {
		req := multipartRequest(t, FormFieldName, "form-token")
		req.Header.Set(HeaderName, token)

		if got := ProvidedCSRFToken(req); got != token {
			t.Fatalf("请求头应优先，期望 %q，实际 %q", token, got)
		}
	})

	t.Run("JSON 请求只认请求头", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/products",
			strings.NewReader(`{"name":"螺丝"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(HeaderName, token)

		if got := ProvidedCSRFToken(req); got != token {
			t.Fatalf("应取到 %q，实际 %q", token, got)
		}
	})
}

// TestCheckCSRFMultipart 确认校验函数在 multipart 上传场景下可用。
func TestCheckCSRFMultipart(t *testing.T) {
	sess := &models.Session{CSRFToken: "session-token"}

	if err := CheckCSRF(multipartRequest(t, FormFieldName, "session-token"), sess); err != nil {
		t.Fatalf("multipart 携带正确令牌时应通过，实际 %v", err)
	}
	if err := CheckCSRF(multipartRequest(t, FormFieldName, "wrong-token"), sess); err == nil {
		t.Fatal("令牌不一致时应报错")
	}
	if err := CheckCSRF(multipartRequest(t, "", ""), sess); err == nil {
		t.Fatal("缺少令牌时应报错")
	}
	if err := CheckCSRF(multipartRequest(t, FormFieldName, "session-token"), nil); err == nil {
		t.Fatal("会话为空时应报错")
	}
}

// TestIsMultipartForm 覆盖内容类型判定，包含带 boundary 与带 charset 的写法。
func TestIsMultipartForm(t *testing.T) {
	cases := []struct {
		contentType string
		want        bool
	}{
		{"multipart/form-data; boundary=----abc", true},
		{"multipart/form-data", true},
		{"application/x-www-form-urlencoded", false},
		{"application/json", false},
		{"", false},
		{"不是合法的内容类型", false},
	}

	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if c.contentType != "" {
			req.Header.Set("Content-Type", c.contentType)
		}
		if got := IsMultipartForm(req); got != c.want {
			t.Errorf("Content-Type=%q 期望 %v，实际 %v", c.contentType, c.want, got)
		}
	}
}
