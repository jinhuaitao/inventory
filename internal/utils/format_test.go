package utils

import (
	"testing"
	"time"
)

func TestFormatMoney(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "¥0.00"},
		{1, "¥1.00"},
		{12.5, "¥12.50"},
		{1234.56, "¥1,234.56"},
		{1234567.89, "¥1,234,567.89"},
		{-99.9, "-¥99.90"},
		{-1234567.89, "-¥1,234,567.89"},
	}
	for _, tt := range tests {
		if got := FormatMoney(tt.in); got != tt.want {
			t.Errorf("FormatMoney(%v) = %q, 期望 %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{1234567, "1,234,567"},
		{-1234, "-1,234"},
	}
	for _, tt := range tests {
		if got := FormatNumber(tt.in); got != tt.want {
			t.Errorf("FormatNumber(%d) = %q, 期望 %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatFloat(t *testing.T) {
	if got := FormatFloat(3.14159, 2); got != "3.14" {
		t.Errorf("FormatFloat(3.14159, 2) = %q", got)
	}
	if got := FormatFloat(10, 0); got != "10" {
		t.Errorf("FormatFloat(10, 0) = %q", got)
	}
}

func TestPercentAndRatio(t *testing.T) {
	if got := Percent(1, 4); got != 25 {
		t.Errorf("Percent(1,4) = %v, 期望 25", got)
	}
	if got := Percent(1, 0); got != 0 {
		t.Errorf("分母为 0 时应返回 0，实际 %v", got)
	}

	// Ratio 会被限制在 [0,1]
	if got := Ratio(1, 4); got != 0.25 {
		t.Errorf("Ratio(1,4) = %v, 期望 0.25", got)
	}
	if got := Ratio(10, 4); got != 1 {
		t.Errorf("超出范围应被截断为 1，实际 %v", got)
	}
	if got := Ratio(-1, 4); got != 0 {
		t.Errorf("负值应被截断为 0，实际 %v", got)
	}
}

func TestBarHeight(t *testing.T) {
	if got := BarHeight(0, 100); got != 0 {
		t.Errorf("数值为 0 时应返回 0，实际 %v", got)
	}
	if got := BarHeight(50, 100); got != 50 {
		t.Errorf("BarHeight(50,100) = %v, 期望 50", got)
	}
	if got := BarHeight(1, 1000); got < 2 {
		t.Errorf("极小值应至少返回 2，实际 %v", got)
	}
	if got := BarHeight(10, 0); got != 0 {
		t.Errorf("最大值为 0 时应返回 0，实际 %v", got)
	}
}

func TestGroupThousands(t *testing.T) {
	tests := []struct{ in, want string }{
		{"1", "1"},
		{"12", "12"},
		{"123", "123"},
		{"1234", "1,234"},
		{"12345", "12,345"},
		{"123456", "123,456"},
		{"1234567", "1,234,567"},
	}
	for _, tt := range tests {
		if got := groupThousands(tt.in); got != tt.want {
			t.Errorf("groupThousands(%q) = %q, 期望 %q", tt.in, got, tt.want)
		}
	}
}

func TestSeqAndMath(t *testing.T) {
	got := Seq(1, 5)
	if len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Errorf("Seq(1,5) = %v", got)
	}
	if Seq(5, 1) != nil {
		t.Error("起点大于终点时应返回 nil")
	}
	if Add(2, 3) != 5 || Sub(5, 3) != 2 || Mul(2, 3) != 6 {
		t.Error("基础算术函数结果不正确")
	}
	if Max(3, 7) != 7 || Min(3, 7) != 3 {
		t.Error("Max / Min 结果不正确")
	}
}

func TestFormatDateTime(t *testing.T) {
	// 数据库存 UTC，展示时转换为 UTC+8
	utc := time.Date(2026, 9, 28, 2, 30, 45, 0, time.UTC)

	if got := FormatDateTime(utc); got != "2026-09-28 10:30:45" {
		t.Errorf("FormatDateTime = %q, 期望 2026-09-28 10:30:45", got)
	}
	if got := FormatDate(utc); got != "2026-09-28" {
		t.Errorf("FormatDate = %q, 期望 2026-09-28", got)
	}
	if got := FormatTime(utc); got != "2026-09-28 10:30" {
		t.Errorf("FormatTime = %q, 期望 2026-09-28 10:30", got)
	}
	if got := FormatDateTime(time.Time{}); got != "-" {
		t.Errorf("零值时间应返回 -，实际 %q", got)
	}
	if got := FormatDateTimePtr(nil); got != "从未登录" {
		t.Errorf("nil 时间应返回「从未登录」，实际 %q", got)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{14 * 1024 * 1024, "14.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{2*1024*1024*1024 + 512*1024*1024, "2.5 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
		{-1, "-"},
	}

	for _, tc := range tests {
		if got := FormatBytes(tc.in); got != tc.want {
			t.Errorf("FormatBytes(%d) = %q, 期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestTextToHTML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"普通文本", "hello", "hello"},
		{"换行转 br", "a\nb", "a<br>b"},
		{"CRLF 归一化", "a\r\nb", "a<br>b"},
		{"转义脚本标签", "<script>alert(1)</script>", "&lt;script&gt;alert(1)&lt;/script&gt;"},
		{"转义引号", `say "hi"`, "say &#34;hi&#34;"},
		{"转义与号", "a & b", "a &amp; b"},
		{"空字符串", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(textToHTML(tc.in)); got != tc.want {
				t.Errorf("textToHTML(%q) = %q, 期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestInitialAndTruncate(t *testing.T) {
	if got := Initial("张三"); got != "张" {
		t.Errorf("Initial(张三) = %q", got)
	}
	if got := Initial(""); got != "?" {
		t.Errorf("空字符串应返回 ?，实际 %q", got)
	}
	if got := Truncate("hello world", 5); got != "hell…" {
		t.Errorf("Truncate = %q, 期望 hell…", got)
	}
	if got := Truncate("短", 5); got != "短" {
		t.Errorf("未超长时不应截断，实际 %q", got)
	}
}
