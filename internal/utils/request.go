package utils

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// ClientIP 尽力还原真实客户端 IP。
//
// ⚠️ X-Forwarded-For / X-Real-IP 是**客户端可以随意伪造**的请求头。
// 只有在请求确实来自可信反向代理时才能采信，否则任何人都能用
// `X-Forwarded-For: 1.2.3.4` 把自己的真实地址换成任意值 ——
// 审计日志会被污染，基于 IP 的限制也会形同虚设。
//
// 因此先判断 RemoteAddr 是否落在 trusted 网段内：
//   - 不在 → 一律返回 RemoteAddr，完全忽略转发头
//   - 在   → 按代理链**从右往左**找第一个不可信地址
//
// 从右往左是关键：反向代理通常是**追加**（Nginx 的
// `$proxy_add_x_forwarded_for`）而不是覆盖，所以最右侧才是真实客户端，
// 它左边的全部是客户端自己塞进来的伪造值。取最左值是最常见的错误做法。
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	remote := parseIP(host, err)
	if remote == nil {
		// 解析不出地址（如 unix socket）时原样返回，便于排查
		return r.RemoteAddr
	}
	if !ipInAny(remote, trusted) {
		return remote.String()
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(parts[i]))
			if ip == nil {
				continue
			}
			if !ipInAny(ip, trusted) {
				return ip.String()
			}
		}
		// 整条链都是可信代理（代理未追加真实地址），只能退回下一手段
	}

	if xri := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); xri != nil {
		return xri.String()
	}
	return remote.String()
}

// parseIP 解析 host:port 或裸 IP，失败返回 nil。
func parseIP(host string, err error) net.IP {
	if err != nil {
		return net.ParseIP(strings.TrimSpace(host))
	}
	return net.ParseIP(host)
}

// ipInAny 判断 IP 是否落在任一网段内。
func ipInAny(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n != nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseTrustedProxies 把逗号分隔的 CIDR 列表解析成网段。
// 同时接受裸 IP（自动按 /32 或 /128 处理），方便配置。
func ParseTrustedProxies(spec string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, raw := range strings.Split(spec, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, &net.ParseError{Type: "IP 地址", Text: item}
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			item = item + "/" + strconv.Itoa(bits)
		}
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return nil, err
		}
		out = append(out, network)
	}
	return out, nil
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
