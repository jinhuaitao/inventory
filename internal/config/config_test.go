package config

import (
	"net"
	"strings"
	"testing"

	"inventory/internal/models"
)

// clearConfigEnv 把本包会读取的环境变量全部清空，让每个用例都从默认值出发。
//
// 注意用 t.Setenv 设成空串而不是删除：本包的 env()/envInt()/envBool()
// 都以「非空」为生效条件，空串等价于未设置，同时 t.Setenv 会自动在用例
// 结束后还原，不会污染其他测试。
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"INVENTORY_ENV",
		"INVENTORY_SESSION_SECRET",
		"INVENTORY_DEFAULT_ROLE",
		"INVENTORY_ALLOW_REGISTRATION",
		"INVENTORY_TRUSTED_PROXIES",
		"INVENTORY_BACKUP_KEEP",
		"INVENTORY_DATA_DIR",
		"INVENTORY_DB_PATH",
		"INVENTORY_BACKUP_DIR",
		"INVENTORY_SEED_DEMO_DATA",
	} {
		t.Setenv(k, "")
	}
}

// ---------------------------------------------------------------------------
// 默认角色：绝不能是 admin
// ---------------------------------------------------------------------------

// TestLoadRejectsAdminDefaultRole 锁定一条关键的安全配置约束。
//
// 开放注册 + 默认角色 admin 意味着「任何能访问注册页的人自助获得最高权限」。
// 这类配置陷阱一旦静默放行，后果是整站失守，因此必须让启动直接失败 ——
// 一个明确报错的启动，远好过一个「看起来正常但人人都是管理员」的系统。
func TestLoadRejectsAdminDefaultRole(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("INVENTORY_DEFAULT_ROLE", "admin")

	_, err := Load()
	if err == nil {
		t.Fatal("INVENTORY_DEFAULT_ROLE=admin 应当让启动失败，实际成功了")
	}
	if !strings.Contains(err.Error(), "admin") {
		t.Errorf("错误信息应点明 admin 这个取值，实际: %v", err)
	}
}

// TestLoadRejectsAdminDefaultRoleCaseInsensitive 大小写不能绕过校验。
func TestLoadRejectsAdminDefaultRoleCaseInsensitive(t *testing.T) {
	for _, v := range []string{"ADMIN", "Admin", " admin "} {
		t.Run(v, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("INVENTORY_DEFAULT_ROLE", v)

			if _, err := Load(); err == nil {
				t.Errorf("INVENTORY_DEFAULT_ROLE=%q 应当被拒绝", v)
			}
		})
	}
}

// TestLoadAcceptsSelfServiceRoles 自助注册允许的两个角色。
func TestLoadAcceptsSelfServiceRoles(t *testing.T) {
	for _, want := range []string{"viewer", "manager", "MANAGER"} {
		t.Run(want, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("INVENTORY_DEFAULT_ROLE", want)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load 失败: %v", err)
			}
			if cfg.DefaultRole != strings.ToLower(want) {
				t.Errorf("DefaultRole = %q, 期望 %q", cfg.DefaultRole, strings.ToLower(want))
			}
		})
	}
}

// TestLoadDefaultsToViewer 未配置时应当是最小权限。
func TestLoadDefaultsToViewer(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if cfg.DefaultRole != string(models.RoleViewer) {
		t.Errorf("默认角色 = %q, 期望 viewer（最小权限）", cfg.DefaultRole)
	}
}

// TestLoadRejectsUnknownDefaultRole 拼错的角色名不能静默降级。
func TestLoadRejectsUnknownDefaultRole(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("INVENTORY_DEFAULT_ROLE", "superuser")

	if _, err := Load(); err == nil {
		t.Fatal("未知角色应当让启动失败")
	}
}

// ---------------------------------------------------------------------------
// 可信反向代理
// ---------------------------------------------------------------------------

// TestLoadDefaultsToLoopbackTrustedProxies 默认只信任回环地址。
//
// 这是安全默认值：转发头（X-Forwarded-For / X-Real-IP）可被客户端随意伪造，
// 默认全盘采信等于让任何人都能往审计日志里写任意来源 IP。
// 默认值覆盖「Nginx 与本服务同机」这一最常见部署。
//
// 断言的是**语义**（某个地址是否落在可信网段内）而不是网段的字符串形式：
// net.ParseCIDR 会把地址掩码到网络地址（127.0.0.1/8 → 127.0.0.0/8），
// 盯着字符串写测试只会在表示形式变化时无谓地失败。
func TestLoadDefaultsToLoopbackTrustedProxies(t *testing.T) {
	clearConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if len(cfg.TrustedProxies) == 0 {
		t.Fatal("默认可信代理不应为空")
	}

	for _, ip := range []string{"127.0.0.1", "127.0.0.53", "::1"} {
		if !ipTrusted(cfg, ip) {
			t.Errorf("%s 应被视为可信代理，实际可信网段为 %v", ip, cfg.TrustedProxies)
		}
	}
	// 反向断言：公网地址绝不能默认被信任。
	for _, ip := range []string{"8.8.8.8", "203.0.113.9", "2001:db8::1"} {
		if ipTrusted(cfg, ip) {
			t.Errorf("%s 不应被默认为可信代理（转发头会被其伪造）", ip)
		}
	}
}

// ipTrusted 判断某个 IP 是否落在配置的可信代理网段内。
func ipTrusted(cfg *Config, ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range cfg.TrustedProxies {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// TestLoadParsesConfiguredTrustedProxies 显式配置应当被正确解析，
// 并同时支持 CIDR 与裸 IP。
func TestLoadParsesConfiguredTrustedProxies(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("INVENTORY_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.5, 2001:db8::/32")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	got := map[string]bool{}
	for _, n := range cfg.TrustedProxies {
		got[n.String()] = true
	}
	for _, want := range []string{"10.0.0.0/8", "192.168.1.5/32", "2001:db8::/32"} {
		if !got[want] {
			t.Errorf("可信代理应包含 %s，实际为 %v", want, got)
		}
	}
}

// TestLoadRejectsMalformedTrustedProxies 写错的网段必须让启动失败。
//
// 静默忽略会带来很隐蔽的故障：管理员以为转发头已生效，
// 实际日志里记的全是代理自己的地址，而排查时又没有任何提示。
func TestLoadRejectsMalformedTrustedProxies(t *testing.T) {
	for _, spec := range []string{"not-an-ip", "10.0.0.0/99", "10.0.0.0/8,garbage"} {
		t.Run(spec, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("INVENTORY_TRUSTED_PROXIES", spec)

			if _, err := Load(); err == nil {
				t.Errorf("INVENTORY_TRUSTED_PROXIES=%q 应当让启动失败", spec)
			}
		})
	}
}
