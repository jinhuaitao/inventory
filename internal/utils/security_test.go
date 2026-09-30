package utils

import (
	"strings"
	"testing"
)

func TestRandomToken(t *testing.T) {
	a, err := RandomToken(32)
	if err != nil {
		t.Fatalf("生成令牌失败: %v", err)
	}
	// 32 字节 → 64 个十六进制字符
	if len(a) != 64 {
		t.Errorf("令牌长度 = %d, 期望 64", len(a))
	}

	b, err := RandomToken(32)
	if err != nil {
		t.Fatalf("生成令牌失败: %v", err)
	}
	if a == b {
		t.Error("两次生成的令牌不应相同")
	}
}

func TestHashToken(t *testing.T) {
	h1 := HashToken("hello")
	h2 := HashToken("hello")
	h3 := HashToken("world")

	if h1 != h2 {
		t.Error("相同输入应产生相同摘要")
	}
	if h1 == h3 {
		t.Error("不同输入应产生不同摘要")
	}
	// SHA-256 摘要为 64 个十六进制字符
	if len(h1) != 64 {
		t.Errorf("摘要长度 = %d, 期望 64", len(h1))
	}
}

func TestSecureCompare(t *testing.T) {
	if !SecureCompare("abc123", "abc123") {
		t.Error("相同字符串应返回 true")
	}
	if SecureCompare("abc123", "abc124") {
		t.Error("不同字符串应返回 false")
	}
	if SecureCompare("abc", "abcd") {
		t.Error("长度不同应返回 false")
	}
	if !SecureCompare("", "") {
		t.Error("两个空字符串应返回 true")
	}
}

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"zhangsan@example.com", "zh***@example.com"},
		{"ab@example.com", "a***@example.com"},
		{"invalid", "invalid"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := MaskEmail(tt.in); got != tt.want {
			t.Errorf("MaskEmail(%q) = %q, 期望 %q", tt.in, got, tt.want)
		}
	}
}

func TestDefaultString(t *testing.T) {
	if got := DefaultString("", "兜底"); got != "兜底" {
		t.Errorf("空字符串应返回兜底值，实际 %q", got)
	}
	if got := DefaultString("   ", "兜底"); got != "兜底" {
		t.Errorf("空白字符串应返回兜底值，实际 %q", got)
	}
	if got := DefaultString("实际值", "兜底"); got != "实际值" {
		t.Errorf("非空字符串应原样返回，实际 %q", got)
	}
}

func TestTruncateUnicode(t *testing.T) {
	// 中文按字符而非字节截断
	got := Truncate("中文测试字符串", 4)
	if got != "中文测…" {
		t.Errorf("Truncate 中文 = %q, 期望 中文测…", got)
	}
	if strings.Count(got, "") < 4 {
		t.Error("截断结果长度不正确")
	}
}

// TestTruncateHandlesNonPositiveMax 锁死「非正数上限不 panic」。
//
// 早先 max <= 0 时会走到 runes[:max]，负数下标直接 panic。
// 一个格式化辅助函数不该把调用方的参数错误升级成整个请求崩掉 ——
// 截断到「零个字符」，返回空串才是合理语义。
func TestTruncateHandlesNonPositiveMax(t *testing.T) {
	for _, max := range []int{0, -1, -100} {
		got := Truncate("中文测试字符串", max)
		if got != "" {
			t.Errorf("Truncate(s, %d) = %q，期望空串", max, got)
		}
	}
	// 空串输入同样不能出问题
	if got := Truncate("", -1); got != "" {
		t.Errorf("Truncate(\"\", -1) = %q，期望空串", got)
	}
}

// TestTruncateBoundaries 边界：max=1 只留一个字符且不加省略号。
func TestTruncateBoundaries(t *testing.T) {
	if got := Truncate("abc", 1); got != "a" {
		t.Errorf("Truncate(\"abc\", 1) = %q，期望 a", got)
	}
	if got := Truncate("abc", 2); got != "a…" {
		t.Errorf("Truncate(\"abc\", 2) = %q，期望 a…", got)
	}
	// 长度恰好等于上限时原样返回，不加省略号
	if got := Truncate("abc", 3); got != "abc" {
		t.Errorf("Truncate(\"abc\", 3) = %q，期望 abc", got)
	}
}
