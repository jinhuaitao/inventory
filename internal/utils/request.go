package utils

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP 尽力还原真实客户端 IP，兼容反向代理场景。
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// UserAgent 返回截断后的 User-Agent，避免超长字符串写库。
func UserAgent(r *http.Request) string {
	return Truncate(r.Header.Get("User-Agent"), 255)
}

// WantsJSON 判断客户端是否期望 JSON 响应。
func WantsJSON(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Requested-With"), "XMLHttpRequest")
}

// Query 读取查询参数并去除首尾空格。
func Query(r *http.Request, key string) string {
	return strings.TrimSpace(r.URL.Query().Get(key))
}
