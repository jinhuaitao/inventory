// Package config 负责集中加载与校验应用配置。
//
// 配置优先级：环境变量 > 内置默认值。
// 所有环境变量均以 INVENTORY_ 为前缀，便于在容器 / CI 中注入。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"inventory/internal/models"
	"inventory/internal/utils"
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

	// 数据库备份。
	//
	// BackupDir 未设置时取 <DataDir>/backups；部署脚本会显式设为
	// /var/backups/inventory，让 Web 界面与每日定时任务看到同一个目录。
	// BackupKeep 是备份目录中自动命名备份的保留份数（0 表示不自动清理）。
	BackupDir  string
	BackupKeep int

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

	// 首次启动且数据库为空时，是否写入内置演示数据（分类 / 供应商 / 商品）。
	// 未显式设置 INVENTORY_SEED_DEMO_DATA 时：生产环境关闭，其他环境开启。
	// 已经写入的演示数据可用 `--purge-demo-data` 清理。
	SeedDemoData bool

	// 登录安全
	MaxLoginAttempts int
	LockoutWindow    time.Duration

	// TrustedProxies 是允许采信 X-Forwarded-For / X-Real-IP 的网段。
	//
	// 为空表示**不信任任何**转发头，一律使用 TCP 层的对端地址 ——
	// 这是安全默认值：转发头可被客户端随意伪造，只有确实来自反向代理
	// 时才该采信。默认只信任回环地址，覆盖「Nginx 与本服务同机」这一
	// 最常见部署；代理在别的机器 / 容器里时请显式配置。
	TrustedProxies []*net.IPNet

	// 是否允许注册（关闭后只能由管理员创建账号）
	AllowRegistration bool

	// 自助注册用户的默认角色：viewer（只读）/ manager（仓管员）。
	// 刻意不允许 admin —— 那等于「任何人自助获得管理员」。
	DefaultRole string
}

// DefaultUpdateRepo 是编译时注入的默认更新仓库（owner/repo）。
// 由 cmd/server 通过 -ldflags 设置，便于官方构建的二进制开箱即用。
var DefaultUpdateRepo = ""

// IsProduction 返回当前是否运行在生产模式。
func (c *Config) IsProduction() bool {
	return strings.EqualFold(c.Env, "production")
}

// LoadStorageOnly 只填充定位数据文件所需的字段（DataDir / DBPath），
// 不做会话密钥、默认角色等合法性校验。
//
// 供 `--purge-demo-data` 这类离线维护命令使用：这类命令不启动 HTTP 服务，
// 即便生产环境尚未配置 INVENTORY_SESSION_SECRET 也应能正常执行。
func LoadStorageOnly() *Config {
	c := &Config{
		DataDir:  env("INVENTORY_DATA_DIR", "data"),
		Env:      env("INVENTORY_ENV", "development"),
		LogLevel: env("INVENTORY_LOG_LEVEL", "info"),

		BackupDir:  env("INVENTORY_BACKUP_DIR", ""),
		BackupKeep: envInt("INVENTORY_BACKUP_KEEP", defaultBackupKeep),
	}
	c.DBPath = env("INVENTORY_DB_PATH", filepath.Join(c.DataDir, "inventory.db"))
	c.BackupDir = resolveBackupDir(c.BackupDir, c.DataDir)
	return c
}

// defaultBackupKeep 是备份目录默认保留的份数。
const defaultBackupKeep = 14

// defaultTrustedProxies 只信任回环地址。
//
// 覆盖「Nginx / Caddy 与本服务装在同一台机器」这一最常见部署；
// 代理在别的机器或容器里时，必须显式配置对应网段，
// 否则转发头会被忽略（日志里看到的将是代理的地址）。
const defaultTrustedProxies = "127.0.0.1/8,::1/128"

// resolveBackupDir 在未显式配置时把备份目录放到数据目录下的 backups/。
func resolveBackupDir(configured, dataDir string) string {
	if configured != "" {
		return configured
	}
	return filepath.Join(dataDir, "backups")
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
		// 先 trim 再小写：环境变量里带空格是常见笔误，
		// 若不处理会落到 default 分支报「取值不合法」，
		// 掩盖掉「你配了 admin」这个真正重要的问题。
		DefaultRole: strings.ToLower(strings.TrimSpace(env("INVENTORY_DEFAULT_ROLE", "viewer"))),

		BackupDir:  env("INVENTORY_BACKUP_DIR", ""),
		BackupKeep: envInt("INVENTORY_BACKUP_KEEP", defaultBackupKeep),
	}

	switch c.DefaultRole {
	case string(models.RoleManager), string(models.RoleViewer):
	case string(models.RoleAdmin):
		// 开放注册 + 默认管理员 = 任何人自助拿到最高权限。这是配置陷阱，
		// 宁可启动失败也不要静默放行；确实需要时请注册后再由管理员提权。
		return nil, fmt.Errorf(
			"INVENTORY_DEFAULT_ROLE 不能是 admin：自助注册者会直接获得管理员权限。" +
				"请改为 viewer / manager，或关闭自助注册（INVENTORY_ALLOW_REGISTRATION=false）")
	default:
		return nil, fmt.Errorf("INVENTORY_DEFAULT_ROLE 取值不合法：%q（可选 viewer / manager）", c.DefaultRole)
	}

	proxies, err := utils.ParseTrustedProxies(env("INVENTORY_TRUSTED_PROXIES", defaultTrustedProxies))
	if err != nil {
		return nil, fmt.Errorf("INVENTORY_TRUSTED_PROXIES 解析失败：%w", err)
	}
	c.TrustedProxies = proxies

	c.DBPath = env("INVENTORY_DB_PATH", filepath.Join(c.DataDir, "inventory.db"))
	c.BackupDir = resolveBackupDir(c.BackupDir, c.DataDir)

	if c.BackupKeep < 0 {
		return nil, fmt.Errorf("INVENTORY_BACKUP_KEEP 不能为负数（当前 %d）", c.BackupKeep)
	}

	// 演示数据默认跟随环境：生产环境不写入，开发/测试环境写入便于试用。
	c.SeedDemoData = envBool("INVENTORY_SEED_DEMO_DATA", !c.IsProduction())

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

// EnsureDirs 确保数据目录与备份目录存在。
func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录 %s 失败: %w", c.DataDir, err)
	}
	if dir := filepath.Dir(c.DBPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建数据库目录 %s 失败: %w", dir, err)
		}
	}
	// 备份目录按 0750 创建：里面是完整数据库副本，不该对同机其他用户开放。
	if c.BackupDir != "" {
		if err := os.MkdirAll(c.BackupDir, 0o750); err != nil {
			return fmt.Errorf("创建备份目录 %s 失败: %w", c.BackupDir, err)
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
