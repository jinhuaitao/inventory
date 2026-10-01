package utils

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

// flashCookie 构造一个携带 flash 内容的请求，值按 SetFlash 的编码规则生成。
func flashCookie(t *testing.T, raw string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{
		Name:  flashCookieName,
		Value: base64.RawURLEncoding.EncodeToString([]byte(raw)),
	})
	return req
}

// TestGetFlashKeepsWhitelistedTypes 四种合法类型原样透传。
func TestGetFlashKeepsWhitelistedTypes(t *testing.T) {
	for _, typ := range []string{"success", "error", "warning", "info"} {
		f := GetFlash(httptest.NewRecorder(), flashCookie(t, typ+"|已保存"))
		if f == nil || f.Type != typ || f.Message != "已保存" {
			t.Errorf("类型 %q 应原样保留，实际 %+v", typ, f)
		}
	}
}

// TestGetFlashSanitizesUnknownType 模板把 Type 拼进 class 属性，
// cookie 内容不可信：未知类型必须降级为 info，防止注入任意 class 词。
func TestGetFlashSanitizesUnknownType(t *testing.T) {
	f := GetFlash(httptest.NewRecorder(), flashCookie(t, `success" onclick="x|已保存`))
	if f == nil {
		t.Fatal("应解析出 flash")
	}
	if f.Type != "info" {
		t.Errorf("未知类型应降级为 info，实际 %q", f.Type)
	}
	if f.Message != "已保存" {
		t.Errorf("消息应保留，实际 %q", f.Message)
	}
}

// TestGetFlashSingleUse 读取一次后 cookie 被清除（MaxAge=-1）。
func TestGetFlashSingleUse(t *testing.T) {
	rec := httptest.NewRecorder()
	GetFlash(rec, flashCookie(t, "info|一次性"))

	var cleared *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == flashCookieName {
			cleared = c
		}
	}
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Errorf("读取后应写入过期 cookie 使其只展示一次，实际 %+v", cleared)
	}
}
