package utils

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"
)

// Validator 收集表单字段的校验错误。
type Validator struct {
	Errors map[string]string
}

// NewValidator 创建校验器。
func NewValidator() *Validator {
	return &Validator{Errors: make(map[string]string)}
}

// Add 记录一条字段错误（同一字段只保留第一条）。
func (v *Validator) Add(field, msg string) {
	if _, exists := v.Errors[field]; !exists {
		v.Errors[field] = msg
	}
}

// Check 当 ok 为 false 时记录错误。
func (v *Validator) Check(ok bool, field, msg string) {
	if !ok {
		v.Add(field, msg)
	}
}

// Has 判断某字段是否有错误。
func (v *Validator) Has(field string) bool {
	_, ok := v.Errors[field]
	return ok
}

// Get 返回某字段的错误信息。
func (v *Validator) Get(field string) string { return v.Errors[field] }

// Valid 判断是否全部通过。
func (v *Validator) Valid() bool { return len(v.Errors) == 0 }

// Count 返回错误数量。
func (v *Validator) Count() int { return len(v.Errors) }

// FirstError 返回第一条错误（map 顺序不确定，仅用于提示）。
func (v *Validator) FirstError() string {
	for _, msg := range v.Errors {
		return msg
	}
	return ""
}

// JoinErrors 把所有错误拼成一句话，用于日志或顶部提示。
func (v *Validator) JoinErrors() string {
	msgs := make([]string, 0, len(v.Errors))
	for _, m := range v.Errors {
		msgs = append(msgs, m)
	}
	return strings.Join(msgs, "；")
}

// ---------------------------------------------------------------------------
// 通用校验函数
// ---------------------------------------------------------------------------

// 密码长度限制：bcrypt 只处理前 72 字节。
const (
	PasswordMinLen = 8
	PasswordMaxLen = 72
)

// IsValidUsername 校验用户名：3-32 位，允许字母、数字、下划线、连字符与点。
func IsValidUsername(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

// IsValidEmail 校验邮箱格式。
func IsValidEmail(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return false
	}
	// 必须是纯邮箱地址（不含 "名字 <a@b>" 形式）
	return addr.Address == s && strings.Contains(s, "@")
}

// ValidatePassword 校验密码强度：至少 8 位且同时包含字母与数字。
func ValidatePassword(pw string) error {
	if len(pw) < PasswordMinLen {
		return errors.New("密码长度至少 8 位")
	}
	if len(pw) > PasswordMaxLen {
		return errors.New("密码长度不能超过 72 位")
	}
	var hasLetter, hasDigit bool
	for _, r := range pw {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return errors.New("密码必须同时包含字母和数字")
	}
	return nil
}

// PasswordStrength 返回 0-4 的强度评分与文字说明，用于注册页的强度条。
func PasswordStrength(pw string) (int, string) {
	if pw == "" {
		return 0, "请输入密码"
	}
	score := 0
	if len(pw) >= 8 {
		score++
	}
	if len(pw) >= 12 {
		score++
	}
	var hasUpper, hasLower, hasDigit, hasSymbol bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSymbol = true
		}
	}
	if hasUpper && hasLower {
		score++
	}
	if hasDigit {
		score++
	}
	if hasSymbol {
		score++
	}
	if score > 4 {
		score = 4
	}

	switch score {
	case 0, 1:
		return score, "弱"
	case 2:
		return score, "一般"
	case 3:
		return score, "较强"
	default:
		return score, "很强"
	}
}

// NormalizeSKU 规范化 SKU：去空格并转大写。
func NormalizeSKU(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}
