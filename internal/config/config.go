// Package config 负责集中加载与校验应用配置。
//
// 配置优先级：环境变量 > 内置默认值。
// 所有环境变量均以 INVENTORY_ 为前缀，便于在容器 / CI 中注入。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"inventory/internal/models"
)

// Config 保存应用的全部运行时配置。
type Config struct {
	AppName  string
	Env      string // development | production
	Addr     string
	BaseURL  string
	DataDir  string
	DBPath   string
	LogLevel string

	// 会话
	SessionSecret    string
	SessionLifetime  time.Duration
	RememberLifetime time.Duration

	// 在线更新（对接 GitHub Releases）
	UpdateEnabled  bool
	UpdateRepo     string // 形如 owner/repo
	UpdateInterval time.Duration
	UpdateToken    string // 可选，访问私有仓库或规避 API 限流

	// 首次启动时自动创建的管理员账号
	SeedAdminUsername string
	SeedAdminEmail    string
	SeedAdminPassword string

	// 登录安全
	MaxLoginAttempts int
	LockoutWindow    time.Duration

	// 是否允许注册（关闭后只能由管理员创建账号）
	AllowRegistration bool

	// 自助注册用户的默认角色：viewer（只读）/ manager（仓管员）/ admin
	DefaultRole string
}

// DefaultUpdateRepo 是编译时注入的默认更新仓库（owner/repo）。
// 由 cmd/server 通过 -ldflags 设置，便于官方构建的二进制开箱即用。
var DefaultUpdateRepo = ""

// IsProduction 返回当前是否运行在生产模式。
func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production")
}

// UpdateConfigured 判断在线更新功能是否可用。
func (c *Config) UpdateConfigured() bool {
	return c.UpdateEnabled && strings.Contains(c.UpdateRepo, "/")
}

// Load 从环境变量读取配置并返回，同时做必要的默认值填充与合法性校验。
func Load() (*Config, error) {
	c := &Config{
		AppName:  env("INVENTORY_APP_NAME", "库存管理系统"),
		Env:      env("INVENTORY_ENV", "development"),
		Addr:     env("INVENTORY_ADDR", ":8080"),
		BaseURL:  strings.TrimRight(env("INVENTORY_BASE_URL", "http://localhost:8080"), "/"),
		DataDir:  env("INVENTORY_DATA_DIR", "data"),
		LogLevel: env("INVENTORY_LOG_LEVEL", "info"),

		SessionSecret:    env("INVENTORY_SESSION_SECRET", ""),
		SessionLifetime:  envDuration("INVENTORY_SESSION_LIFETIME", 12*time.Hour),
		RememberLifetime: envDuration("INVENTORY_REMEMBER_LIFETIME", 30*24*time.Hour),

		UpdateEnabled:  envBool("INVENTORY_UPDATE_ENABLED", true),
		UpdateRepo:     strings.TrimSpace(env("INVENTORY_UPDATE_REPO", DefaultUpdateRepo)),
		UpdateInterval: envDuration("INVENTORY_UPDATE_INTERVAL", 6*time.Hour),
		UpdateToken:    env("INVENTORY_UPDATE_TOKEN", ""),

		SeedAdminUsername: env("INVENTORY_ADMIN_USERNAME", "admin"),
		SeedAdminEmail:    env("INVENTORY_ADMIN_EMAIL", "admin@example.com"),
		SeedAdminPassword: env("INVENTORY_ADMIN_PASSWORD", ""),

		MaxLoginAttempts: envInt("INVENTORY_MAX_LOGIN_ATTEMPTS", 5),
		LockoutWindow:    envDuration("INVENTORY_LOCKOUT_WINDOW", 15*time.Minute),

		AllowRegistration: envBool("INVENTORY_ALLOW_REGISTRATION", true),
		DefaultRole:       strings.ToLower(env("INVENTORY_DEFAULT_ROLE", "viewer")),
	}

	switch c.DefaultRole {
	case string(models.RoleAdmin), string(models.RoleManager), string(models.RoleViewer):
	default:
		return nil, fmt.Errorf("INVENTORY_DEFAULT_ROLE 取值不合法：%q（可选 viewer / manager / admin）", c.DefaultRole)
	}

	c.DBPath = env("INVENTORY_DB_PATH", filepath.Join(c.DataDir, "inventory.db"))

	// 会话密钥：生产环境必须显式提供，开发环境自动生成一个临时密钥。
	if c.SessionSecret == "" {
		if c.IsProduction() {
			return nil, fmt.Errorf("生产环境必须设置 INVENTORY_SESSION_SECRET（至少 32 个字符的随机字符串）")
		}
		secret, err := randomSecret(32)
		if err != nil {
			return nil, fmt.Errorf("生成临时会话密钥失败: %w", err)
		}
		c.SessionSecret = secret
	}

	if len(c.SessionSecret) < 16 {
		return nil, fmt.Errorf("INVENTORY_SESSION_SECRET 太短，请使用至少 16 个字符的随机字符串")
	}

	if c.SeedAdminPassword == "" {
		c.SeedAdminPassword = "Admin@12345"
	}

	return c, nil
}

// EnsureDirs 确保数据目录存在。
func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录 %s 失败: %w", c.DataDir, err)
	}
	if dir := filepath.Dir(c.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建数据库目录 %s 失败: %w", dir, err)
		}
	}
	return nil
}

// randomSecret 生成 n 字节的随机十六进制字符串。
func randomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
