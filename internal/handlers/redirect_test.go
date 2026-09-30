package handlers

import "testing"

// TestSanitizeNext_AllowsInternalPaths 站内相对路径应原样放行。
func TestSanitizeNext_AllowsInternalPaths(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"根路径", "/", "/"},
		{"业务路径", "/products", "/products"},
		{"带查询串", "/products?status=low&page=2", "/products?status=low&page=2"},
		{"带路径参数", "/products/12/edit", "/products/12/edit"},
		{"查询串含编码字符", "/products?q=%E9%92%A5%E5%8C%99", "/products?q=%E9%92%A5%E5%8C%99"},
		{"首尾空白被 trim", "  /products  ", "/products"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeNext(tc.in); got != tc.want {
				t.Errorf("sanitizeNext(%q) = %q，期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeNext_RejectsOpenRedirect 站外跳转一律拒绝（返回空串）。
func TestSanitizeNext_RejectsOpenRedirect(t *testing.T) {
	cases := []struct{ name, in string }{
		{"绝对 URL", "http://evil.com"},
		{"协议相对", "//evil.com"},
		{"反斜杠变形", "/\\evil.com"},
		{"双反斜杠", "/\\\\evil.com"},
		{"javascript 伪协议", "javascript:alert(1)"},
		{"data 伪协议", "data:text/html,<script>alert(1)</script>"},
		{"非斜杠开头", "evil.com"},
		{"带凭据的绝对 URL", "http://user@evil.com/path"},
		{"大小写混合协议", "HTTPS://evil.com"},

		// 下面几条是本次修复的重点：浏览器按 WHATWG 规范解析 Location 时
		// 会先剥离 ASCII 空白，Tab 被剥掉后 "/\t/evil.com" 就等价于
		// "//evil.com"，一次登录跳转即被送到站外。
		{"裸 Tab 绕过", "/\t/evil.com"},
		{"裸换行绕过", "/\n/evil.com"},
		{"裸回车绕过", "/\r/evil.com"},
		{"垂直制表符绕过", "/\v/evil.com"},
		{"换页符绕过", "/\f/evil.com"},
		{"NUL 字节绕过", "/\x00/evil.com"},
		// 百分号编码的控制字符在字符串层面看不出来，只有 url.Parse
		// 解码之后才会现形 —— 所以必须在解析**之后**再查一次。
		{"编码 Tab 绕过", "/%09/evil.com"},
		{"编码换行绕过", "/%0a/evil.com"},
		{"编码回车绕过", "/%0d/evil.com"},
		// 空格同样会被浏览器剥离，配合前面的斜杠可拼出协议相对 URL
		{"编码空格+斜杠", "/%20/evil.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeNext(tc.in); got != "" {
				t.Errorf("sanitizeNext(%q) = %q，期望被拒绝（空串）", tc.in, got)
			}
		})
	}
}

// TestSanitizeNext_EmptyInput 空输入返回空串，调用方据此回落到首页。
func TestSanitizeNext_EmptyInput(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\t"} {
		if got := sanitizeNext(in); got != "" {
			t.Errorf("sanitizeNext(%q) = %q，期望空串", in, got)
		}
	}
}

// TestSanitizeNext_DropsFragment fragment 不参与导航目标，丢弃可减少歧义。
func TestSanitizeNext_DropsFragment(t *testing.T) {
	if got := sanitizeNext("/products#section"); got != "/products" {
		t.Errorf("sanitizeNext(\"/products#section\") = %q，期望 \"/products\"", got)
	}
}
