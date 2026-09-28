package services

import (
	"errors"
	"strings"
)

// 业务层统一错误。处理器据此决定提示文案与 HTTP 状态码。
var (
	// ErrNotFound 记录不存在
	ErrNotFound = errors.New("记录不存在")
	// ErrConflict 唯一约束冲突（用户名 / 邮箱 / SKU / 名称重复）
	ErrConflict = errors.New("记录已存在")
	// ErrInvalidInput 参数不合法
	ErrInvalidInput = errors.New("参数不合法")
	// ErrInsufficientStock 库存不足
	ErrInsufficientStock = errors.New("库存不足")
	// ErrAccountDisabled 账号已被禁用
	ErrAccountDisabled = errors.New("账号已被禁用，请联系管理员")
	// ErrInvalidCredentials 用户名或密码错误
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	// ErrTokenInvalid 令牌无效或已过期
	ErrTokenInvalid = errors.New("链接无效或已过期")
	// ErrLastAdmin 不允许删除或降级最后一个管理员
	ErrLastAdmin = errors.New("系统必须保留至少一个启用的管理员账号")
	// ErrSecurityQuestionsNotSet 账号尚未设置安全问题
	ErrSecurityQuestionsNotSet = errors.New("该账号尚未设置安全问题")
	// ErrSecurityAnswersWrong 安全问题的答案不正确
	ErrSecurityAnswersWrong = errors.New("安全问题答案不正确")
	// ErrTooManyAttempts 尝试次数过多
	ErrTooManyAttempts = errors.New("尝试次数过多，请稍后再试")
)

// isUniqueViolation 判断是否为 SQLite 唯一约束冲突。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "constraint failed") && strings.Contains(msg, "unique")
}

// uniqueField 从 SQLite 的错误信息中提取冲突字段，例如 users.username。
func uniqueField(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	const marker = "UNIQUE constraint failed:"
	i := strings.Index(msg, marker)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(msg[i+len(marker):])
	if j := strings.IndexByte(rest, ' '); j > 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}
