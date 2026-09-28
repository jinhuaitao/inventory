package utils

import (
	"encoding/base64"
	"net/http"
	"strings"
)

const flashCookieName = "inventory_flash"

// Flash 是一条一次性提示消息，通过 cookie 在重定向之间传递。
type Flash struct {
	Type    string // success | error | warning | info
	Message string
}

// IsSuccess 等便捷判断，供模板使用。
func (f *Flash) IsSuccess() bool { return f != nil && f.Type == "success" }
func (f *Flash) IsError() bool   { return f != nil && f.Type == "error" }

// SetFlash 写入一条提示消息（存活 60 秒，读取一次后立即失效）。
func SetFlash(w http.ResponseWriter, typ, message string) {
	raw := typ + "|" + message
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookieName,
		Value:    base64.RawURLEncoding.EncodeToString([]byte(raw)),
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// SetSuccess / SetError 是 SetFlash 的语义化封装。
func SetSuccess(w http.ResponseWriter, msg string) { SetFlash(w, "success", msg) }
func SetError(w http.ResponseWriter, msg string)   { SetFlash(w, "error", msg) }
func SetWarning(w http.ResponseWriter, msg string) { SetFlash(w, "warning", msg) }
func SetInfo(w http.ResponseWriter, msg string)    { SetFlash(w, "info", msg) }

// GetFlash 读取并清除提示消息。
func GetFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(flashCookieName)
	if err != nil || c.Value == "" {
		return nil
	}

	// 立即失效，保证只展示一次
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	decoded, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil
	}

	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return nil
	}
	return &Flash{Type: parts[0], Message: parts[1]}
}
