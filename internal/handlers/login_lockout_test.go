package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"inventory/internal/config"
	"inventory/internal/database"
	"inventory/internal/services"
)

// newLoginTestHandler 构造一个只带 store / cfg / logger 的处理器，
// 足以驱动 loginLocked 与 store 层的计数逻辑（不需要渲染器）。
func newLoginTestHandler(t *testing.T) *Handler {
	t.Helper()

	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("初始化数据库结构失败: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Handler{
		store:  services.New(db, logger),
		cfg:    &config.Config{MaxLoginAttempts: 5, LockoutWindow: 15 * time.Minute},
		logger: logger,
	}
}

// recordFailures 连续记录 n 次同一 (标识, IP) 的登录失败。
func recordFailures(t *testing.T, h *Handler, identifier, ip string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := h.store.RecordLoginAttempt(context.Background(), identifier, ip, false); err != nil {
			t.Fatalf("记录登录失败失败: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// 定向锁死他人账号（Targeted Lockout DoS）
// ---------------------------------------------------------------------------

// TestLoginLockedIsScopedToSourceIP 是本次修复的核心用例。
//
// 修复前限流只看「标识」，且判断发生在密码校验之前。于是任何人只要知道
// 受害者的用户名，每 15 分钟发 5 次错误密码，就能让受害者**永远登不进来** ——
// 零凭据、零成本的定向拒绝服务。
//
// 修复后主力维度收敛到「标识 + 来源 IP」：攻击者打满额度只会锁住它自己，
// 受害者从自己的 IP 登录完全不受影响。
func TestLoginLockedIsScopedToSourceIP(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()
	since := time.Now().Add(-h.cfg.LockoutWindow)

	const victim = "victim"
	const attackerIP = "203.0.113.9"
	const victimIP = "198.51.100.7"

	recordFailures(t, h, victim, attackerIP, h.cfg.MaxLoginAttempts)

	// 攻击者自己应当被拦住。
	locked, err := h.loginLocked(ctx, victim, attackerIP, since)
	if err != nil {
		t.Fatalf("loginLocked 报错: %v", err)
	}
	if !locked {
		t.Errorf("同一来源连续失败 %d 次后应被限流", h.cfg.MaxLoginAttempts)
	}

	// 关键断言：受害者的正常来源不受别人的失败连累。
	locked, err = h.loginLocked(ctx, victim, victimIP, since)
	if err != nil {
		t.Fatalf("loginLocked 报错: %v", err)
	}
	if locked {
		t.Error("受害者从自己的 IP 登录被别人的失败连累 —— 定向锁死漏洞仍然存在")
	}
}

// TestLoginLockedBackstopsDistributedAttempts 确认兜底维度仍然有效。
//
// 攻击者换 IP 可以绕开「标识 + IP」维度，但所有来源的失败会累加到同一个
// 标识上；累计到 MaxLoginAttempts × identifierLockoutMultiplier 时仍会被拦住。
func TestLoginLockedBackstopsDistributedAttempts(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()
	since := time.Now().Add(-h.cfg.LockoutWindow)

	const victim = "victim"
	limit := h.cfg.MaxLoginAttempts * identifierLockoutMultiplier

	// 每个 IP 只试一次：单 IP 维度永远达不到阈值。
	for i := 0; i < limit; i++ {
		ip := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		recordFailures(t, h, victim, ip, 1)
	}

	locked, err := h.loginLocked(ctx, victim, "192.0.2.1", since)
	if err != nil {
		t.Fatalf("loginLocked 报错: %v", err)
	}
	if !locked {
		t.Errorf("跨来源失败累计达到 %d 次后应被限流（分布式爆破兜底失效）", limit)
	}
}

// TestLoginLockedAllowsNormalUse 确认阈值以下不会误伤正常用户。
func TestLoginLockedAllowsNormalUse(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()
	since := time.Now().Add(-h.cfg.LockoutWindow)

	// 打错 4 次（阈值是 5）仍然可以继续尝试。
	recordFailures(t, h, "someone", "203.0.113.9", h.cfg.MaxLoginAttempts-1)

	locked, err := h.loginLocked(ctx, "someone", "203.0.113.9", since)
	if err != nil {
		t.Fatalf("loginLocked 报错: %v", err)
	}
	if locked {
		t.Errorf("失败 %d 次（阈值 %d）不应被限流", h.cfg.MaxLoginAttempts-1, h.cfg.MaxLoginAttempts)
	}
}

// TestLoginLockedExpiresAfterWindow 确认锁定会随时间自然解除。
func TestLoginLockedExpiresAfterWindow(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()

	const victim = "victim"
	const ip = "203.0.113.9"
	recordFailures(t, h, victim, ip, h.cfg.MaxLoginAttempts)

	// 窗口内 → 锁定
	if locked, err := h.loginLocked(ctx, victim, ip, time.Now().Add(-h.cfg.LockoutWindow)); err != nil || !locked {
		t.Fatalf("锁定窗口内应被限流，locked=%v err=%v", locked, err)
	}
	// 把窗口起点推到所有失败记录之后 → 计数归零 → 解除
	if locked, err := h.loginLocked(ctx, victim, ip, time.Now().Add(time.Minute)); err != nil || locked {
		t.Errorf("窗口已过应解除限流，locked=%v err=%v", locked, err)
	}
}

// ---------------------------------------------------------------------------
// 锁定自我延长（Lockout Self-Extension）
// ---------------------------------------------------------------------------

// TestBlockedAttemptsDoNotExtendLockout 锁定另一条严重缺陷。
//
// 修复前，被限流的请求会再写一条「失败」记录。而限流判断在密码校验之前，
// 于是攻击者只要在被锁期间持续发请求，每次都会把计数窗口往后推 ——
// 锁定**永远不会过期**，每分钟一个请求即可永久锁死账号。
//
// 修复后被限流的请求记 blocked = 1，只留痕不计数，锁定窗口得以自然流逝。
func TestBlockedAttemptsDoNotExtendLockout(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()

	const victim = "victim"
	const ip = "203.0.113.9"

	recordFailures(t, h, victim, ip, 3)

	// 攻击者继续猛敲 100 次，全部记成「被限流」。
	for i := 0; i < 100; i++ {
		if err := h.store.RecordBlockedLoginAttempt(ctx, victim, ip); err != nil {
			t.Fatalf("记录被限流的尝试失败: %v", err)
		}
	}

	since := time.Now().Add(-h.cfg.LockoutWindow)
	n, err := h.store.CountFailedAttempts(ctx, victim, since)
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if n != 3 {
		t.Errorf("失败计数 = %d，期望 3 —— 被限流的尝试不应计入（否则锁定会无限延长）", n)
	}

	// 按 (标识, IP) 维度统计同样只应看到 3 次。
	n, err = h.store.CountFailedAttemptsByIdentifierAndIP(ctx, victim, ip, since)
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if n != 3 {
		t.Errorf("按来源统计的失败计数 = %d，期望 3", n)
	}
}

// TestSuccessfulLoginClearsFailures 确认成功登录后计数被清空。
func TestSuccessfulLoginClearsFailures(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()

	const victim = "victim"
	const ip = "203.0.113.9"
	recordFailures(t, h, victim, ip, 4)

	if err := h.store.RecordLoginAttempt(ctx, victim, ip, true); err != nil {
		t.Fatalf("记录成功登录失败: %v", err)
	}
	if err := h.store.ClearFailedAttempts(ctx, victim); err != nil {
		t.Fatalf("清空失败记录失败: %v", err)
	}

	since := time.Now().Add(-h.cfg.LockoutWindow)
	n, err := h.store.CountFailedAttempts(ctx, victim, since)
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if n != 0 {
		t.Errorf("成功登录后失败计数 = %d，期望 0", n)
	}
}

// ---------------------------------------------------------------------------
// 找回密码：同一类缺陷的两条链路
// ---------------------------------------------------------------------------

// recordRecoveryFailures 连续记录 n 次同一 (账号, 标识, IP) 的找回密码失败。
func recordRecoveryFailures(t *testing.T, h *Handler, userID int64, identifier, ip string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := h.store.RecordRecoveryAttempt(context.Background(), userID, identifier, ip, false); err != nil {
			t.Fatalf("记录找回密码失败失败: %v", err)
		}
	}
}

// TestRecoveryIdentifierLockedIsScopedToSourceIP 第一步（按标识查询）同样必须按来源收敛。
//
// 只按标识计数的话，攻击者只要知道受害者的用户名，随便填几次不存在的账号名
// 就能把对方锁在自助找回之外 —— 与登录限流是同一个缺陷。
func TestRecoveryIdentifierLockedIsScopedToSourceIP(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()
	since := time.Now().Add(-h.cfg.LockoutWindow)

	const victim = "victim"
	const attackerIP = "203.0.113.9"
	const victimIP = "198.51.100.7"

	recordRecoveryFailures(t, h, 0, victim, attackerIP, h.cfg.MaxLoginAttempts)

	locked, err := h.recoveryIdentifierLocked(ctx, victim, attackerIP, since)
	if err != nil {
		t.Fatalf("recoveryIdentifierLocked 报错: %v", err)
	}
	if !locked {
		t.Errorf("同一来源连续失败 %d 次后应被限流", h.cfg.MaxLoginAttempts)
	}

	locked, err = h.recoveryIdentifierLocked(ctx, victim, victimIP, since)
	if err != nil {
		t.Fatalf("recoveryIdentifierLocked 报错: %v", err)
	}
	if locked {
		t.Error("受害者从自己的 IP 走找回流程被别人的失败连累")
	}
}

// TestRecoveryAnswerLockedIsScopedToSourceIP 第二步（校验答案）同理。
func TestRecoveryAnswerLockedIsScopedToSourceIP(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()
	since := time.Now().Add(-h.cfg.LockoutWindow)

	const userID = int64(42)
	const victim = "victim"
	const attackerIP = "203.0.113.9"
	const victimIP = "198.51.100.7"

	recordRecoveryFailures(t, h, userID, victim, attackerIP, h.cfg.MaxLoginAttempts)

	locked, err := h.recoveryAnswerLocked(ctx, userID, attackerIP, since)
	if err != nil {
		t.Fatalf("recoveryAnswerLocked 报错: %v", err)
	}
	if !locked {
		t.Errorf("同一来源答错 %d 次后应被限流", h.cfg.MaxLoginAttempts)
	}

	locked, err = h.recoveryAnswerLocked(ctx, userID, victimIP, since)
	if err != nil {
		t.Fatalf("recoveryAnswerLocked 报错: %v", err)
	}
	if locked {
		t.Error("受害者从自己的 IP 答题被别人的失败连累")
	}
}

// TestBlockedRecoveryAttemptsDoNotExtendLockout 找回流程同样不能被自我延长。
func TestBlockedRecoveryAttemptsDoNotExtendLockout(t *testing.T) {
	h := newLoginTestHandler(t)
	ctx := context.Background()

	const userID = int64(42)
	const victim = "victim"
	const ip = "203.0.113.9"

	recordRecoveryFailures(t, h, userID, victim, ip, 3)

	for i := 0; i < 100; i++ {
		if err := h.store.RecordBlockedRecoveryAttempt(ctx, userID, victim, ip); err != nil {
			t.Fatalf("记录被限流的找回尝试失败: %v", err)
		}
	}

	since := time.Now().Add(-h.cfg.LockoutWindow)
	n, err := h.store.CountFailedRecovery(ctx, userID, since)
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if n != 3 {
		t.Errorf("失败计数 = %d，期望 3 —— 被限流的尝试不应计入", n)
	}

	n, err = h.store.CountFailedRecoveryByUserAndIP(ctx, userID, ip, since)
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if n != 3 {
		t.Errorf("按来源统计的失败计数 = %d，期望 3", n)
	}

	// 标识维度同样只应看到 3 次。
	n, err = h.store.CountFailedRecoveryByIdentifier(ctx, victim, since)
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if n != 3 {
		t.Errorf("按标识统计的失败计数 = %d，期望 3", n)
	}
}

// ---------------------------------------------------------------------------
// 旧库迁移
// ---------------------------------------------------------------------------

// TestMigrateAddsBlockedColumnToLegacyDB 确认旧库能平滑升级。
//
// 老版本数据库里没有 blocked 列，CREATE TABLE IF NOT EXISTS 不会补列，
// 必须靠显式 ALTER TABLE。缺列会导致记录被限流的尝试时直接报错。
func TestMigrateAddsBlockedColumnToLegacyDB(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("首次迁移失败: %v", err)
	}

	// 模拟旧库：把 blocked 列删掉。
	for _, table := range []string{"login_attempts", "recovery_attempts"} {
		if _, err := db.ExecContext(ctx, `ALTER TABLE `+table+` DROP COLUMN blocked`); err != nil {
			t.Fatalf("删除 %s.blocked 失败: %v", table, err)
		}
	}

	// 再次迁移应当把列补回来。
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}

	for _, table := range []string{"login_attempts", "recovery_attempts"} {
		if !hasColumn(t, db, ctx, table, "blocked") {
			t.Errorf("%s 迁移后仍缺少 blocked 列", table)
		}
	}
}

// hasColumn 判断表是否存在指定列。
func hasColumn(t *testing.T, db *sql.DB, ctx context.Context, table, column string) bool {
	t.Helper()

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatalf("读取 %s 表结构失败: %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &primaryKey); err != nil {
			t.Fatalf("解析 %s 表结构失败: %v", table, err)
		}
		if strings.EqualFold(name, column) {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历 %s 表结构失败: %v", table, err)
	}
	return false
}
