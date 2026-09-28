package utils

import (
	"strings"
	"testing"
)

func TestIsValidUsername(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"纯字母", "zhangsan", true},
		{"字母加数字", "user123", true},
		{"含下划线", "user_name", true},
		{"含连字符", "user-name", true},
		{"含点号", "user.name", true},
		{"大小写混合", "UserABC", true},
		{"长度恰好 3", "abc", true},
		{"长度恰好 32", strings.Repeat("a", 32), true},
		{"空字符串", "", false},
		{"长度 2", "ab", false},
		{"长度 33", strings.Repeat("a", 33), false},
		{"含中文", "张三", false},
		{"含空格", "user name", false},
		{"含 at 符号", "user@name", false},
		{"含斜杠", "user/name", false},
		{"含表情", "user😀", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidUsername(tt.input); got != tt.want {
				t.Errorf("IsValidUsername(%q) = %v, 期望 %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"常规邮箱", "user@example.com", true},
		{"带子域名", "user@mail.example.com", true},
		{"带加号", "user+tag@example.com", true},
		{"带点号", "first.last@example.com", true},
		{"大写", "USER@EXAMPLE.COM", true},
		{"缺少 at", "userexample.com", false},
		{"缺少域名", "user@", false},
		{"缺少用户名", "@example.com", false},
		{"含空格", "user @example.com", false},
		{"空字符串", "", false},
		{"显示名格式", "张三 <user@example.com>", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidEmail(tt.input); got != tt.want {
				t.Errorf("IsValidEmail(%q) = %v, 期望 %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"标准强密码", "Test@12345", false},
		{"仅字母数字", "abcd1234", false},
		{"恰好 8 位", "abcd1234", false},
		{"太短", "abc123", true},
		{"纯字母", "abcdefgh", true},
		{"纯数字", "12345678", true},
		{"空密码", "", true},
		{"超长", strings.Repeat("a1", 40), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePassword(%q) 错误 = %v, 期望出错 = %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestPasswordStrength(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		minScore int
		maxScore int
	}{
		{"空密码", "", 0, 0},
		{"弱密码", "abc", 0, 1},
		{"中等密码", "abcd1234", 2, 3},
		{"强密码", "Abcd1234!@#$", 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, text := PasswordStrength(tt.input)
			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("PasswordStrength(%q) 得分 = %d, 期望在 [%d, %d] 区间",
					tt.input, score, tt.minScore, tt.maxScore)
			}
			if tt.input == "" && text != "请输入密码" {
				t.Errorf("空密码提示应为「请输入密码」，实际为 %q", text)
			}
		})
	}
}

func TestValidator(t *testing.T) {
	v := NewValidator()

	if !v.Valid() {
		t.Fatal("新建校验器应当无错误")
	}

	v.Check(false, "name", "名称不能为空")
	v.Check(true, "email", "邮箱格式不正确")

	if v.Valid() {
		t.Error("存在错误时 Valid() 应返回 false")
	}
	if !v.Has("name") {
		t.Error("应当记录 name 字段的错误")
	}
	if v.Has("email") {
		t.Error("通过的检查不应记录错误")
	}
	if v.Count() != 1 {
		t.Errorf("错误数量 = %d, 期望 1", v.Count())
	}
	if got := v.Get("name"); got != "名称不能为空" {
		t.Errorf("Get(name) = %q", got)
	}

	// 同一字段只保留第一条错误
	v.Add("name", "第二条错误")
	if v.Get("name") != "名称不能为空" {
		t.Error("同一字段应只保留第一条错误")
	}
}

func TestNormalizeSKU(t *testing.T) {
	tests := []struct{ in, want string }{
		{"sku-001", "SKU-001"},
		{"  sku-002  ", "SKU-002"},
		{"Sku-003", "SKU-003"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeSKU(tt.in); got != tt.want {
			t.Errorf("NormalizeSKU(%q) = %q, 期望 %q", tt.in, got, tt.want)
		}
	}
}
