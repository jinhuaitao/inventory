package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// insertTestUser 直接往 users 表插一条账号，返回其 id。
func insertTestUser(t *testing.T, db *sql.DB, ctx context.Context, username, role string) int64 {
	t.Helper()

	now := time.Now().UTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO users (username, email, password_hash, full_name, role, status, created_at, updated_at)
		 VALUES (?, ?, 'not-a-real-hash', '', ?, 'active', ?, ?)`,
		username, username+"@example.com", role, now, now)
	if err != nil {
		t.Fatalf("插入测试用户 %s 失败: %v", username, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("获取用户 %s 的 id 失败: %v", username, err)
	}
	return id
}

// callDemoOperatorID 在事务中调用 demoOperatorID，返回其结果。
func callDemoOperatorID(t *testing.T, db *sql.DB, ctx context.Context) (int64, error) {
	t.Helper()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("开启事务失败: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	return demoOperatorID(ctx, tx)
}

// TestDemoOperatorIDReturnsZeroWhenNoUsers 空用户表时返回 0 而不是报错。
//
// 调用方据此跳过写流水。若这里返回错误，整个 Seed 会失败 ——
// 而 Seed 在启动路径上，等于服务**无法启动**。
func TestDemoOperatorIDReturnsZeroWhenNoUsers(t *testing.T) {
	db, ctx := newTestDB(t)

	id, err := callDemoOperatorID(t, db, ctx)
	if err != nil {
		t.Fatalf("空用户表时不应报错，实际: %v", err)
	}
	if id != 0 {
		t.Errorf("空用户表时应返回 0，实际 %d", id)
	}
}

// TestDemoOperatorIDPrefersAdmin 有管理员时优先选管理员。
func TestDemoOperatorIDPrefersAdmin(t *testing.T) {
	db, ctx := newTestDB(t)

	// 先插一个 viewer（id 更小），再插管理员。
	insertTestUser(t, db, ctx, "viewer1", "viewer")
	adminID := insertTestUser(t, db, ctx, "admin1", "admin")

	id, err := callDemoOperatorID(t, db, ctx)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if id != adminID {
		t.Errorf("应优先返回管理员 id %d，实际 %d", adminID, id)
	}
}

// TestDemoOperatorIDFallsBackToAnyUser 没有管理员时退回任意账号，
// 而不是返回 0 —— 演示流水同样需要挂在真实账号上。
func TestDemoOperatorIDFallsBackToAnyUser(t *testing.T) {
	db, ctx := newTestDB(t)

	viewerID := insertTestUser(t, db, ctx, "viewer1", "viewer")
	insertTestUser(t, db, ctx, "manager1", "manager")

	id, err := callDemoOperatorID(t, db, ctx)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if id != viewerID {
		t.Errorf("无管理员时应返回 id 最小的账号 %d，实际 %d", viewerID, id)
	}
}

// TestDemoOperatorIDNeverReturnsDeletedID 是本函数存在的**真正理由**。
//
// 修复前 seedDemoData 把 operator_id 写死成 1。而 stock_movements.operator_id
// 带外键约束，一旦「用户表非空但 id=1 已被删除」（清理演示数据后该账号即可
// 被删、或管理员改过账号），整个 Seed 就会因为外键失败而回滚，
// 服务**直接起不来** —— 一个纯粹的演示数据问题，却让生产服务无法启动。
//
// 这里构造的正是那个场景：删除 id 最小的账号，确认返回的是**仍然存在**的 id。
func TestDemoOperatorIDNeverReturnsDeletedID(t *testing.T) {
	db, ctx := newTestDB(t)

	deletedID := insertTestUser(t, db, ctx, "first", "admin")
	survivorID := insertTestUser(t, db, ctx, "second", "admin")

	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, deletedID); err != nil {
		t.Fatalf("删除用户失败: %v", err)
	}

	id, err := callDemoOperatorID(t, db, ctx)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if id == deletedID {
		t.Fatalf("返回了已删除的 id %d —— 外键约束会让 Seed 失败、服务无法启动", deletedID)
	}
	if id != survivorID {
		t.Errorf("应返回仍然存在的账号 %d，实际 %d", survivorID, id)
	}

	// 再确认一次：这个 id 确实能通过外键校验。
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, id).Scan(&exists); err != nil {
		t.Fatalf("校验用户是否存在失败: %v", err)
	}
	if exists != 1 {
		t.Errorf("返回的 id %d 在 users 表中不存在", id)
	}
}

// TestSeedSucceedsAfterAdminOneDeleted 端到端复现原始故障。
//
// 完整链路（与真实运维操作一致）：
//  1. 首次启动 → 创建管理员（id=1）+ 演示数据；
//  2. 管理员执行 `--purge-demo-data` 清理演示数据；
//  3. 管理员删掉了那个自动创建的管理员账号，改成用自己的账号；
//  4. 服务重启 → Seed 再次运行。
//
// 关键在第 4 步：users 表此刻为空，seedAdmin 会新建管理员，而因为主键是
// AUTOINCREMENT，新账号拿到的是 id=2 而不是 1。此时若代码写死
// operator_id=1，外键约束会让整条演示数据插入失败 → Seed 失败 →
// **服务无法启动**。一个纯粹的演示数据问题，却让生产服务起不来。
func TestSeedSucceedsAfterAdminOneDeleted(t *testing.T) {
	db, ctx := newTestDB(t)

	// ---- 1. 首次播种 ----
	seedWithDemo(t, db, ctx)
	if countRows(t, db, ctx, "products") == 0 {
		t.Fatal("首次播种后应存在演示商品")
	}

	// ---- 2. 走真实的清理路径 ----
	if _, err := PurgeDemoData(ctx, db, PurgeOptions{}, testLogger()); err != nil {
		t.Fatalf("清理演示数据失败: %v", err)
	}

	// ---- 3. 用户表清空（模拟管理员换用自己的账号） ----
	if _, err := db.ExecContext(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("清空用户失败: %v", err)
	}

	// ---- 4. 再次 Seed ----
	if err := Seed(ctx, db, SeedOptions{
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
		AdminPassword: "Admin@12345",
		WithDemoData:  true,
	}, testLogger()); err != nil {
		t.Fatalf("id=1 不存在时 Seed 仍然应当成功，实际: %v", err)
	}

	// 确认场景确实成立：新管理员不是 id=1，所以「写死 1」必然踩外键。
	var newAdminID int64
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM users WHERE role = 'admin' ORDER BY id LIMIT 1`).Scan(&newAdminID); err != nil {
		t.Fatalf("查询新建管理员失败: %v", err)
	}
	if newAdminID == 1 {
		t.Fatalf("新管理员 id 仍为 1，本用例没有构造出目标场景（AUTOINCREMENT 应使其递增）")
	}

	if countRows(t, db, ctx, "products") == 0 {
		t.Error("重新播种后应重新写入演示商品")
	}
	// 所有演示流水的操作人必须是真实存在的账号。
	var dangling int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stock_movements m
		 LEFT JOIN users u ON u.id = m.operator_id
		 WHERE u.id IS NULL`).Scan(&dangling)
	if err != nil {
		t.Fatalf("检查流水操作人失败: %v", err)
	}
	if dangling != 0 {
		t.Errorf("存在 %d 条指向不存在账号的流水", dangling)
	}
}
