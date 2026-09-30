// Package utils 提供跨模块复用的通用工具：模板渲染、分页、校验、
// 安全随机数、CSV 导出与格式化函数。
package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// RandomToken 生成 n 字节的密码学安全随机令牌（十六进制字符串）。
func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机令牌失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// HashToken 计算令牌的 SHA-256 摘要。
// 数据库中只保存摘要，即使数据库泄露也无法直接复用令牌。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SecureCompare 以恒定时间比较两个字符串，避免时序侧信道。
func SecureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// MaskEmail 对邮箱做脱敏展示，例如 ab***@example.com。
func MaskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return email
	}
	local, domain := email[:at], email[at:]
	if len(local) <= 2 {
		return local[:1] + "***" + domain
	}
	return local[:2] + strings.Repeat("*", 3) + domain
}

// Truncate 按字符（而非字节）截断字符串，并追加省略号。
//
// max 小于等于 0 时返回空串：截断到「零个字符」本就该是空串，
// 而放任负数走到 runes[:max] 会直接 panic ——
// 一个格式化辅助函数不该把调用方的参数错误升级成整个请求崩掉。
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return string(runes[:1])
	}
	return string(runes[:max-1]) + "…"
}

// DefaultString 在字符串为空时返回默认值。
func DefaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
