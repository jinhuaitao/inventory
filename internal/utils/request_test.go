package utils

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustProxies(t *testing.T, spec string) []*net.IPNet {
	t.Helper()
	nets, err := ParseTrustedProxies(spec)
	if err != nil {
		t.Fatalf("解析可信代理 %q 失败: %v", spec, err)
	}
	return nets
}

func TestClientIPIgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	// 直连（对端不可信）时，伪造的 XFF 必须被完全忽略，
	// 否则任何人都能往审计日志里写任意 IP。
	trusted := mustProxies(t, "127.0.0.1/8")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "5.6.7.8")

	if got := ClientIP(r, trusted); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, 期望 203.0.113.9（应忽略不可信来源的转发头）", got)
	}
}

func TestClientIPUsesForwardedHeadersFromTrustedProxy(t *testing.T) {
	trusted := mustProxies(t, "127.0.0.1/8")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := ClientIP(r, trusted); got != "1.2.3.4" {
		t.Errorf("ClientIP = %q, 期望 1.2.3.4", got)
	}
}

func TestClientIPSkipsTrustedHopsFromTheRight(t *testing.T) {
	// 代理链：客户端 1.2.3.4 → 内网代理 10.0.0.1 → 本机
	// 10.0.0.1 也是可信代理，应当继续往左找到 1.2.3.4
	trusted := mustProxies(t, "127.0.0.1/8,10.0.0.0/8")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")

	if got := ClientIP(r, trusted); got != "1.2.3.4" {
		t.Errorf("ClientIP = %q, 期望 1.2.3.4", got)
	}
}

func TestClientIPResistsLeftmostSpoofing(t *testing.T) {
	// 关键用例：Nginx 用 $proxy_add_x_forwarded_for 是**追加**，
	// 所以客户端自带的 "6.6.6.6" 会留在最左边，真实地址在右边。
	// 取最左值是最常见的实现错误，会让攻击者随意伪造来源 IP。
	trusted := mustProxies(t, "127.0.0.1/8")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")

	if got := ClientIP(r, trusted); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, 期望 203.0.113.9（应取最右侧不可信地址，而不是最左值）", got)
	}
}

func TestClientIPFallsBackToRealIPHeader(t *testing.T) {
	trusted := mustProxies(t, "127.0.0.1/8")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Real-IP", "9.9.9.9")

	if got := ClientIP(r, trusted); got != "9.9.9.9" {
		t.Errorf("ClientIP = %q, 期望 9.9.9.9", got)
	}
}

func TestClientIPWithoutTrustedProxiesAlwaysUsesRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := ClientIP(r, nil); got != "127.0.0.1" {
		t.Errorf("ClientIP = %q, 期望 127.0.0.1（未配置可信代理时不得采信任何转发头）", got)
	}
}

func TestClientIPHandlesIPv6(t *testing.T) {
	trusted := mustProxies(t, "::1/128")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[::1]:54321"
	r.Header.Set("X-Forwarded-For", "2001:db8::1")

	if got := ClientIP(r, trusted); got != "2001:db8::1" {
		t.Errorf("ClientIP = %q, 期望 2001:db8::1", got)
	}
}

func TestParseTrustedProxiesAcceptsBareIP(t *testing.T) {
	nets, err := ParseTrustedProxies("192.168.1.5, 10.0.0.0/8")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(nets) != 2 {
		t.Fatalf("网段数量 = %d, 期望 2", len(nets))
	}
	// 裸 IP 应被当成单机 /32
	if nets[0].String() != "192.168.1.5/32" {
		t.Errorf("nets[0] = %s, 期望 192.168.1.5/32", nets[0])
	}
}

func TestParseTrustedProxiesRejectsGarbage(t *testing.T) {
	for _, spec := range []string{"not-an-ip", "10.0.0.0/99", "1.2.3.4/abc"} {
		if _, err := ParseTrustedProxies(spec); err == nil {
			t.Errorf("ParseTrustedProxies(%q) 应当报错", spec)
		}
	}
}

func TestParseTrustedProxiesSkipsEmptyItems(t *testing.T) {
	nets, err := ParseTrustedProxies(" , 127.0.0.1/8 , ")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(nets) != 1 {
		t.Errorf("网段数量 = %d, 期望 1（空项应被跳过）", len(nets))
	}
}
