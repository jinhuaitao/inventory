package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"inventory/internal/auth"
	"inventory/internal/config"
	"inventory/internal/database"
	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/utils"
	"inventory/web"
)

// newRegistrationTestHandler 构造一个带真实模板渲染器的处理器，
// 足以驱动注册开关的读写与注册页的准入判断。
//
// envDefault 模拟环境变量 INVENTORY_ALLOW_REGISTRATION 给出的默认值。
func newRegistrationTestHandler(t *testing.T, envDefault bool) *Handler {
	t.Helper()

	h := newBareRegistrationHandler(t, envDefault)
	if err := h.InitRegistration(context.Background()); err != nil {
		t.Fatalf("载入注册开关失败: %v", err)
	}
	return h
}

// newBareRegistrationHandler 同上，但**不**调用 InitRegistration，
// 用于验证「缓存尚未载入」时的回落行为。
func newBareRegistrationHandler(t *testing.T, envDefault bool) *Handler {
	t.Helper()

	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("初始化数据库结构失败: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	renderer, err := utils.NewRenderer(web.FS, true, logger)
	if err != nil {
		t.Fatalf("构造模板渲染器失败: %v", err)
	}

	h := &Handler{
		store:    services.New(db, logger),
		renderer: renderer,
		cfg: &config.Config{
			AppName:           "测试站点",
			Env:               "development",
			AllowRegistration: envDefault,
			DefaultRole:       string(models.RoleViewer),
		},
		logger: logger,
	}
	return h
}

// testUser 构造一个用于注入请求上下文的账号。
func testUser(role models.Role) *models.User {
	return &models.User{
		ID:       1,
		Username: "operator",
		Email:    "operator@example.com",
		Role:     role,
		Status:   models.UserStatusActive,
	}
}

// registrationRequest 构造一个带表单体的请求，并把指定账号放进上下文。
func registrationRequest(form url.Values, user *models.User) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/users/registration", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r.WithContext(auth.WithUser(r.Context(), user))
}

// ---------------------------------------------------------------------------
// 启动加载
// ---------------------------------------------------------------------------

// 启动时必须先读库：registrationState 的零值是「关闭」，
// 漏掉这一步会让一个开放注册的站点静默变成关闭。
func TestInitRegistrationPrefersStoredSetting(t *testing.T) {
	h := newRegistrationTestHandler(t, false)
	ctx := context.Background()

	if h.AllowRegistration() {
		t.Fatal("环境变量默认关闭、库中无记录时应为关闭")
	}

	if err := h.store.SetRegistrationEnabled(ctx, true); err != nil {
		t.Fatalf("写入注册开关失败: %v", err)
	}
	if err := h.InitRegistration(ctx); err != nil {
		t.Fatalf("重新载入注册开关失败: %v", err)
	}
	if !h.AllowRegistration() {
		t.Error("库中已设为开启，启动加载应取到开启而不是环境变量的关闭")
	}
}

// ---------------------------------------------------------------------------
// 权限
// ---------------------------------------------------------------------------

// 只有管理员能改这个开关。仓管员与只读用户即使直接 POST 也必须被挡住，
// 且不能对数据库产生任何副作用。
func TestUserRegistrationToggleRequiresAdmin(t *testing.T) {
	for _, role := range []models.Role{models.RoleViewer, models.RoleManager} {
		t.Run(string(role), func(t *testing.T) {
			h := newRegistrationTestHandler(t, false)
			ctx := context.Background()

			rec := httptest.NewRecorder()
			h.UserRegistrationToggle(rec, registrationRequest(
				url.Values{"enabled": {"true"}}, testUser(role)))

			if rec.Code != http.StatusSeeOther {
				t.Errorf("越权请求应被拒绝（重定向回首页），实际状态码 %d", rec.Code)
			}
			if h.AllowRegistration() {
				t.Error("越权请求不应改动内存中的开关")
			}

			st, err := h.store.RegistrationState(ctx, false)
			if err != nil {
				t.Fatalf("读取注册开关失败: %v", err)
			}
			if st.Explicit {
				t.Error("越权请求不应在数据库里留下设置")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 开关读写
// ---------------------------------------------------------------------------

func TestUserRegistrationTogglePersistsState(t *testing.T) {
	h := newRegistrationTestHandler(t, false)
	ctx := context.Background()
	admin := testUser(models.RoleAdmin)

	// 开启
	rec := httptest.NewRecorder()
	h.UserRegistrationToggle(rec, registrationRequest(url.Values{"enabled": {"true"}}, admin))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("开启注册应重定向，实际状态码 %d", rec.Code)
	}
	if !h.AllowRegistration() {
		t.Error("开启后内存中的开关应为开启")
	}

	st, err := h.store.RegistrationState(ctx, false)
	if err != nil {
		t.Fatalf("读取注册开关失败: %v", err)
	}
	if !st.Enabled || !st.Explicit {
		t.Errorf("开启后应持久化为显式设置，实际 Enabled=%v Explicit=%v", st.Enabled, st.Explicit)
	}

	// 关闭
	rec = httptest.NewRecorder()
	h.UserRegistrationToggle(rec, registrationRequest(url.Values{"enabled": {"false"}}, admin))
	if h.AllowRegistration() {
		t.Error("关闭后内存中的开关应为关闭")
	}
	if st, err = h.store.RegistrationState(ctx, true); err != nil {
		t.Fatalf("读取注册开关失败: %v", err)
	}
	if st.Enabled {
		t.Error("关闭后应覆盖环境变量的开启，实际仍为开启")
	}
}

// 恢复默认应当清除页面设置，让开关重新由环境变量决定。
func TestUserRegistrationToggleResetRestoresEnvDefault(t *testing.T) {
	h := newRegistrationTestHandler(t, true) // 环境变量默认开启
	ctx := context.Background()
	admin := testUser(models.RoleAdmin)

	// 先在页面上关闭
	h.UserRegistrationToggle(httptest.NewRecorder(),
		registrationRequest(url.Values{"enabled": {"false"}}, admin))
	if h.AllowRegistration() {
		t.Fatal("页面上关闭后应为关闭")
	}

	// 再恢复默认
	rec := httptest.NewRecorder()
	h.UserRegistrationToggle(rec, registrationRequest(url.Values{"action": {"reset"}}, admin))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("恢复默认应重定向，实际状态码 %d", rec.Code)
	}
	if !h.AllowRegistration() {
		t.Error("恢复默认后应回到环境变量取值（开启）")
	}
	if _, explicit := h.RegistrationStatus(); explicit {
		t.Error("恢复默认后不应再标记为「页面设置」")
	}
	if st, err := h.store.RegistrationState(ctx, false); err != nil {
		t.Fatalf("读取注册开关失败: %v", err)
	} else if st.Explicit {
		t.Error("恢复默认后数据库中不应还留着设置记录")
	}
}

// ---------------------------------------------------------------------------
// 注册入口的准入
// ---------------------------------------------------------------------------

// 关闭注册后，GET /register 必须立刻拒绝 —— 不能等下次重启才生效。
func TestRegisterPageFollowsRuntimeSwitch(t *testing.T) {
	h := newRegistrationTestHandler(t, true)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	h.RegisterPage(rec, httptest.NewRequest(http.MethodGet, "/register", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("注册开放时 /register 应返回 200，实际 %d", rec.Code)
	}

	if err := h.store.SetRegistrationEnabled(ctx, false); err != nil {
		t.Fatalf("关闭注册失败: %v", err)
	}
	if err := h.InitRegistration(ctx); err != nil {
		t.Fatalf("重新载入注册开关失败: %v", err)
	}

	rec = httptest.NewRecorder()
	h.RegisterPage(rec, httptest.NewRequest(http.MethodGet, "/register", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("注册关闭后 /register 应返回 403，实际 %d", rec.Code)
	}
}

// 绕过页面直接 POST /register 也必须被挡住。
func TestRegisterSubmitRejectedWhenDisabled(t *testing.T) {
	h := newRegistrationTestHandler(t, false)

	form := url.Values{
		"username":         {"intruder"},
		"email":            {"intruder@example.com"},
		"password":         {"Passw0rd@123"},
		"password_confirm": {"Passw0rd@123"},
	}
	body := strings.NewReader(form.Encode())
	r := httptest.NewRequest(http.MethodPost, "/register", body)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	h.RegisterSubmit(rec, r)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("注册关闭时提交应被重定向，实际状态码 %d", rec.Code)
	}

	n, err := h.store.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("统计用户数失败: %v", err)
	}
	if n != 0 {
		t.Errorf("注册关闭时不应创建任何账号，实际新增 %d 个", n)
	}
}

// 登录页的「立即注册」入口必须跟着运行期开关走。
func TestLoginPageHidesRegisterEntryWhenDisabled(t *testing.T) {
	h := newRegistrationTestHandler(t, true)
	ctx := context.Background()

	rec := httptest.NewRecorder()
	h.LoginPage(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if !strings.Contains(rec.Body.String(), "/register") {
		t.Fatal("注册开放时登录页应显示注册入口")
	}

	if err := h.store.SetRegistrationEnabled(ctx, false); err != nil {
		t.Fatalf("关闭注册失败: %v", err)
	}
	if err := h.InitRegistration(ctx); err != nil {
		t.Fatalf("重新载入注册开关失败: %v", err)
	}

	rec = httptest.NewRecorder()
	h.LoginPage(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if strings.Contains(rec.Body.String(), "立即注册") {
		t.Error("注册关闭后登录页不应再显示注册入口")
	}
}

// ---------------------------------------------------------------------------
// 缓存未载入 / 数据库不可用时的回落
// ---------------------------------------------------------------------------

// 直接构造 Handler 却漏调 InitRegistration（将来重构很容易出现这种情况）时，
// 缓存是零值。若把零值当成真实取值，一个默认开放的站点会**静默**变成关闭。
func TestRegistrationFallsBackToConfigWhenCacheNotLoaded(t *testing.T) {
	h := newBareRegistrationHandler(t, true)

	if !h.AllowRegistration() {
		t.Error("缓存未载入时应回落到配置默认值 true，而不是结构体零值 false")
	}
	if enabled, explicit := h.RegistrationStatus(); !enabled || explicit {
		t.Errorf("回落值不应被标记为「页面设置」，实际 enabled=%v explicit=%v", enabled, explicit)
	}
}

// 数据库不可用时，渲染路径不能让页面崩掉，也不该把开关翻成关闭 ——
// 500 页面恰恰是在数据库出问题时才要渲染的。
func TestRegistrationFallsBackToConfigWhenDatabaseUnavailable(t *testing.T) {
	h := newBareRegistrationHandler(t, true)

	if err := h.store.DB().Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}

	if !h.AllowRegistration() {
		t.Error("数据库不可用时应回落到配置默认值，而不是零值")
	}

	// 回源必须如实报错，交由后台循环记日志，而不是静默吞掉。
	if err := h.RefreshRegistration(context.Background()); err == nil {
		t.Error("数据库不可用时 RefreshRegistration 应返回错误")
	}
}

// ---------------------------------------------------------------------------
// 畸形请求
// ---------------------------------------------------------------------------

// 参数缺失 / 取值非法 / 动作未知都必须被拒绝，绝不能因为「读不到就当作 false」
// 而把一次畸形请求变成「静默关闭注册」。
func TestUserRegistrationToggleRejectsMalformedPayload(t *testing.T) {
	cases := []struct {
		name string
		form url.Values
	}{
		{"缺少 enabled", url.Values{"action": {"set"}}},
		{"enabled 取值非法", url.Values{"action": {"set"}, "enabled": {"yes"}}},
		{"enabled 为空", url.Values{"action": {"set"}, "enabled": {""}}},
		{"动作未知", url.Values{"action": {"delete"}, "enabled": {"true"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRegistrationTestHandler(t, false) // 环境变量默认关闭
			ctx := context.Background()
			admin := testUser(models.RoleAdmin)

			// 先正常开启，好确认后面的畸形请求确实没把它改掉
			h.UserRegistrationToggle(httptest.NewRecorder(),
				registrationRequest(url.Values{"enabled": {"true"}}, admin))
			if !h.AllowRegistration() {
				t.Fatal("前置条件失败：开启未生效")
			}

			rec := httptest.NewRecorder()
			h.UserRegistrationToggle(rec, registrationRequest(tc.form, admin))

			if rec.Code != http.StatusSeeOther {
				t.Errorf("畸形请求应被拒绝并重定向，实际状态码 %d", rec.Code)
			}
			if !h.AllowRegistration() {
				t.Error("畸形请求把开关改成了关闭 —— 安全相关的开关不该有这种失败方式")
			}

			st, err := h.store.RegistrationState(ctx, false)
			if err != nil {
				t.Fatalf("读取注册开关失败: %v", err)
			}
			if !st.Enabled || !st.Explicit {
				t.Errorf("畸形请求不应改动数据库，实际 Enabled=%v Explicit=%v", st.Enabled, st.Explicit)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 外部改动的收敛
// ---------------------------------------------------------------------------

// 绕过处理器直接改库（等价于「另一个实例」或手工 UPDATE settings），
// 回源后缓存必须跟上。
func TestRefreshRegistrationPicksUpExternalChange(t *testing.T) {
	h := newRegistrationTestHandler(t, false)
	ctx := context.Background()

	if h.AllowRegistration() {
		t.Fatal("前置条件失败：初始应为关闭")
	}

	if err := h.store.SetRegistrationEnabled(ctx, true); err != nil {
		t.Fatalf("写入注册开关失败: %v", err)
	}
	if h.AllowRegistration() {
		t.Error("回源之前缓存不应自行改变")
	}

	if err := h.RefreshRegistration(ctx); err != nil {
		t.Fatalf("回源失败: %v", err)
	}
	if !h.AllowRegistration() {
		t.Error("回源后应跟上外部改动")
	}
	if _, explicit := h.RegistrationStatus(); !explicit {
		t.Error("回源后应识别出这是页面上的显式设置")
	}
}

// 后台同步循环必须能在无人操作的情况下把外部改动吸收进来，
// 否则「另一个进程改了开关，这个进程永远不生效」会成为一个无从解释的现象。
func TestRunRegistrationRefreshSyncsPeriodically(t *testing.T) {
	h := newRegistrationTestHandler(t, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.RunRegistrationRefresh(ctx, 5*time.Millisecond)

	if err := h.store.SetRegistrationEnabled(context.Background(), true); err != nil {
		t.Fatalf("写入注册开关失败: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.AllowRegistration() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("后台同步循环未在 3 秒内跟上外部改动")
}
