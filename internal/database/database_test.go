package database

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestDB 打开一个临时文件数据库并完成建表。
// 刻意不用 :memory: —— 连接池有多个连接，内存库之间互不可见。
func newTestDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()

	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db, ctx
}

// seedWithDemo 写入管理员与演示数据。
func seedWithDemo(t *testing.T, db *sql.DB, ctx context.Context) {
	t.Helper()

	err := Seed(ctx, db, SeedOptions{
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
		AdminPassword: "Admin@12345",
		WithDemoData:  true,
	}, testLogger())
	if err != nil {
		t.Fatalf("写入种子数据失败: %v", err)
	}
}

// countRows 统计表的行数。
func countRows(t *testing.T, db *sql.DB, ctx context.Context, table string) int {
	t.Helper()

	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("统计 %s 失败: %v", table, err)
	}
	return n
}

// ---------------------------------------------------------------------------
// 演示数据写入
// ---------------------------------------------------------------------------

func TestSeedWithDemoData(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	t.Run("写入预期数量", func(t *testing.T) {
		if got := countRows(t, db, ctx, "categories"); got != len(demoCategories) {
			t.Errorf("分类数 = %d, 期望 %d", got, len(demoCategories))
		}
		if got := countRows(t, db, ctx, "suppliers"); got != len(demoSuppliers) {
			t.Errorf("供应商数 = %d, 期望 %d", got, len(demoSuppliers))
		}
		if got := countRows(t, db, ctx, "products"); got != len(demoProducts) {
			t.Errorf("商品数 = %d, 期望 %d", got, len(demoProducts))
		}
	})

	t.Run("流水数量等于期初库存不为零的商品数", func(t *testing.T) {
		want := 0
		for _, p := range demoProducts {
			if p.qty > 0 {
				want++
			}
		}
		if got := countRows(t, db, ctx, "stock_movements"); got != want {
			t.Errorf("流水数 = %d, 期望 %d", got, want)
		}
	})

	t.Run("所有写入的行都带 is_demo 标记", func(t *testing.T) {
		for _, table := range []string{"categories", "suppliers", "products"} {
			var unmarked int
			if err := db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM `+table+` WHERE `+demoFlagColumn+` <> 1`).Scan(&unmarked); err != nil {
				t.Fatalf("统计未标记行失败: %v", err)
			}
			if unmarked != 0 {
				t.Errorf("%s 中有 %d 行未标记为演示数据", table, unmarked)
			}
		}
	})

	t.Run("重复调用不产生重复数据", func(t *testing.T) {
		seedWithDemo(t, db, ctx)
		if got := countRows(t, db, ctx, "products"); got != len(demoProducts) {
			t.Errorf("重复播种后商品数 = %d, 期望 %d", got, len(demoProducts))
		}
	})
}

func TestSeedWithoutDemoData(t *testing.T) {
	db, ctx := newTestDB(t)

	err := Seed(ctx, db, SeedOptions{
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
		AdminPassword: "Admin@12345",
		WithDemoData:  false,
	}, testLogger())
	if err != nil {
		t.Fatalf("写入种子数据失败: %v", err)
	}

	if got := countRows(t, db, ctx, "products"); got != 0 {
		t.Errorf("关闭演示数据后商品数应为 0，实际 %d", got)
	}
	if got := countRows(t, db, ctx, "categories"); got != 0 {
		t.Errorf("关闭演示数据后分类数应为 0，实际 %d", got)
	}
	if got := countRows(t, db, ctx, "users"); got != 1 {
		t.Errorf("管理员账号应照常创建，用户数 = %d", got)
	}
}

// ---------------------------------------------------------------------------
// 演示数据清理
// ---------------------------------------------------------------------------

func TestPurgeDemoData(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	res, err := PurgeDemoData(ctx, db, PurgeOptions{}, testLogger())
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	if res.Products != len(demoProducts) {
		t.Errorf("删除商品数 = %d, 期望 %d", res.Products, len(demoProducts))
	}
	if res.Categories != len(demoCategories) {
		t.Errorf("删除分类数 = %d, 期望 %d", res.Categories, len(demoCategories))
	}
	if res.Suppliers != len(demoSuppliers) {
		t.Errorf("删除供应商数 = %d, 期望 %d", res.Suppliers, len(demoSuppliers))
	}
	if res.Movements == 0 {
		t.Error("库存流水应被一并删除")
	}
	if len(res.SkippedCategories) != 0 || len(res.SkippedSuppliers) != 0 {
		t.Errorf("无外部引用时不应有保留项: %v / %v", res.SkippedCategories, res.SkippedSuppliers)
	}

	for _, table := range []string{"categories", "suppliers", "products", "stock_movements"} {
		if got := countRows(t, db, ctx, table); got != 0 {
			t.Errorf("清理后 %s 仍有 %d 行", table, got)
		}
	}
	if got := countRows(t, db, ctx, "users"); got != 1 {
		t.Errorf("清理不应删除管理员账号，用户数 = %d", got)
	}
}

func TestPurgeDemoDataDryRun(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	before := countRows(t, db, ctx, "products")

	res, err := PurgeDemoData(ctx, db, PurgeOptions{DryRun: true}, testLogger())
	if err != nil {
		t.Fatalf("预演清理失败: %v", err)
	}
	if res.Products != len(demoProducts) {
		t.Errorf("预演应统计出 %d 个商品，实际 %d", len(demoProducts), res.Products)
	}

	if after := countRows(t, db, ctx, "products"); after != before {
		t.Errorf("--dry-run 不应改动数据：商品数由 %d 变为 %d", before, after)
	}
	if got := countRows(t, db, ctx, "categories"); got != len(demoCategories) {
		t.Errorf("--dry-run 不应删除分类，实际剩 %d", got)
	}
	if got := countRows(t, db, ctx, "stock_movements"); got == 0 {
		t.Error("--dry-run 不应删除库存流水")
	}
}

func TestPurgeDemoDataIsIdempotent(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	if _, err := PurgeDemoData(ctx, db, PurgeOptions{}, testLogger()); err != nil {
		t.Fatalf("首次清理失败: %v", err)
	}

	res, err := PurgeDemoData(ctx, db, PurgeOptions{}, testLogger())
	if err != nil {
		t.Fatalf("二次清理失败: %v", err)
	}
	if !res.Empty() {
		t.Errorf("二次清理应无改动，实际 %+v", res)
	}
}

// ---------------------------------------------------------------------------
// 安全性：不能误删用户自己的数据
// ---------------------------------------------------------------------------

// insertUserData 写入一份用户自建的主数据与商品。
func insertUserData(t *testing.T, db *sql.DB, ctx context.Context) (catID, supID, prodID int64) {
	t.Helper()

	now := "2026-01-01 00:00:00"

	res, err := db.ExecContext(ctx,
		`INSERT INTO categories (name, description, `+demoFlagColumn+`, created_at, updated_at)
		 VALUES ('自有分类', '用户自建', 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("写入自有分类失败: %v", err)
	}
	catID, _ = res.LastInsertId()

	res, err = db.ExecContext(ctx,
		`INSERT INTO suppliers (name, contact_person, phone, email, address, note, `+demoFlagColumn+`, created_at, updated_at)
		 VALUES ('自有供应商', '', '', '', '', '', 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatalf("写入自有供应商失败: %v", err)
	}
	supID, _ = res.LastInsertId()

	res, err = db.ExecContext(ctx, `
		INSERT INTO products
			(sku, name, barcode, category_id, supplier_id, unit, cost_price, sale_price,
			 quantity, safety_stock, location, description, status, `+demoFlagColumn+`, created_at, updated_at)
		VALUES ('REAL-0001', '用户自有商品', '', ?, ?, '件', 1, 2, 10, 1, 'Z-01-01', '', 'active', 0, ?, ?)`,
		catID, supID, now, now)
	if err != nil {
		t.Fatalf("写入自有商品失败: %v", err)
	}
	prodID, _ = res.LastInsertId()
	return catID, supID, prodID
}

func TestPurgeDemoDataKeepsUserData(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	catID, supID, prodID := insertUserData(t, db, ctx)

	res, err := PurgeDemoData(ctx, db, PurgeOptions{}, testLogger())
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	var got int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM products WHERE id = ?`, prodID).Scan(&got); err != nil {
		t.Fatalf("查询自有商品失败: %v", err)
	}
	if got != 1 {
		t.Error("用户自建商品被误删")
	}
	for _, tc := range []struct {
		table string
		id    int64
		name  string
	}{
		{"categories", catID, "分类"},
		{"suppliers", supID, "供应商"},
	} {
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+tc.table+` WHERE id = ?`, tc.id).Scan(&got); err != nil {
			t.Fatalf("查询自有%s失败: %v", tc.name, err)
		}
		if got != 1 {
			t.Errorf("用户自建%s被误删", tc.name)
		}
	}

	if res.Products != len(demoProducts) {
		t.Errorf("删除商品数 = %d, 期望 %d", res.Products, len(demoProducts))
	}
}

// TestPurgeDemoDataKeepsReferencedMasterData 覆盖「用户把自建商品挂到了演示分类/供应商上」的场景：
// 此时分类与供应商仍在使用，必须保留，只在结果中报告。
func TestPurgeDemoDataKeepsReferencedMasterData(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	now := "2026-01-01 00:00:00"
	demoCat := demoCategoryNames()[0]
	demoSup := demoSupplierNames()[0]

	var catID, supID int64
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM categories WHERE name = ?`, demoCat).Scan(&catID); err != nil {
		t.Fatalf("查询演示分类失败: %v", err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM suppliers WHERE name = ?`, demoSup).Scan(&supID); err != nil {
		t.Fatalf("查询演示供应商失败: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO products
			(sku, name, barcode, category_id, supplier_id, unit, cost_price, sale_price,
			 quantity, safety_stock, location, description, status, `+demoFlagColumn+`, created_at, updated_at)
		VALUES ('REAL-0002', '挂在演示分类下的自有商品', '', ?, ?, '件', 1, 2, 1, 1, '', '', 'active', 0, ?, ?)`,
		catID, supID, now, now); err != nil {
		t.Fatalf("写入自有商品失败: %v", err)
	}

	res, err := PurgeDemoData(ctx, db, PurgeOptions{}, testLogger())
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	var got int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM categories WHERE id = ?`, catID).Scan(&got); err != nil {
		t.Fatalf("查询分类失败: %v", err)
	}
	if got != 1 {
		t.Error("仍被商品引用的演示分类不应被删除")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suppliers WHERE id = ?`, supID).Scan(&got); err != nil {
		t.Fatalf("查询供应商失败: %v", err)
	}
	if got != 1 {
		t.Error("仍被商品引用的演示供应商不应被删除")
	}

	if !contains(res.SkippedCategories, demoCat) {
		t.Errorf("应在 SkippedCategories 中报告 %q，实际 %v", demoCat, res.SkippedCategories)
	}
	if !contains(res.SkippedSuppliers, demoSup) {
		t.Errorf("应在 SkippedSuppliers 中报告 %q，实际 %v", demoSup, res.SkippedSuppliers)
	}
	if res.Categories != len(demoCategories)-1 {
		t.Errorf("删除分类数 = %d, 期望 %d", res.Categories, len(demoCategories)-1)
	}
}

// TestPurgeDemoDataLegacyRowsWithoutFlag 覆盖「加 is_demo 标记之前写入的旧库」：
// 标记全为 0，仍应能按内置 SKU 与名称清理干净。
func TestPurgeDemoDataLegacyRowsWithoutFlag(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	// 模拟旧库：没有任何 is_demo 标记
	for _, table := range []string{"categories", "suppliers", "products"} {
		if _, err := db.ExecContext(ctx, `UPDATE `+table+` SET `+demoFlagColumn+` = 0`); err != nil {
			t.Fatalf("清除 %s 标记失败: %v", table, err)
		}
	}

	res, err := PurgeDemoData(ctx, db, PurgeOptions{}, testLogger())
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if res.Products != len(demoProducts) {
		t.Errorf("旧库商品应全部清掉，实际删除 %d / %d", res.Products, len(demoProducts))
	}
	if res.Categories != len(demoCategories) {
		t.Errorf("旧库分类应全部清掉，实际删除 %d / %d", res.Categories, len(demoCategories))
	}
	if got := countRows(t, db, ctx, "products"); got != 0 {
		t.Errorf("清理后仍有 %d 个商品", got)
	}
}

// ---------------------------------------------------------------------------
// 迁移
// ---------------------------------------------------------------------------

func TestMigrateIsIdempotent(t *testing.T) {
	db, ctx := newTestDB(t)

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("第三次迁移失败: %v", err)
	}
}

// TestMigrateAddsDemoColumnToLegacyDB 模拟旧版本数据库：先建好库，再删掉 is_demo 列，
// 然后重新迁移，验证列会被自动补回来。
func TestMigrateAddsDemoColumnToLegacyDB(t *testing.T) {
	db, ctx := newTestDB(t)

	for _, table := range []string{"categories", "suppliers", "products"} {
		if _, err := db.ExecContext(ctx, `ALTER TABLE `+table+` DROP COLUMN `+demoFlagColumn); err != nil {
			t.Skipf("当前 SQLite 驱动不支持 DROP COLUMN，跳过: %v", err)
		}
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("补齐列失败: %v", err)
	}

	for _, table := range []string{"categories", "suppliers", "products"} {
		if !hasColumn(t, db, ctx, table, demoFlagColumn) {
			t.Errorf("%s 迁移后仍缺少 %s 列", table, demoFlagColumn)
		}
	}
}

// hasColumn 判断表中是否存在指定列。
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
		if name == column {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func contains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func TestPlaceholders(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, ""},
		{-1, ""},
		{1, "?"},
		{2, "?,?"},
		{3, "?,?,?"},
	}
	for _, tc := range tests {
		if got := placeholders(tc.n); got != tc.want {
			t.Errorf("placeholders(%d) = %q, 期望 %q", tc.n, got, tc.want)
		}
	}
}

func TestDemoSkus(t *testing.T) {
	skus := DemoSkus()
	if len(skus) != len(demoProducts) {
		t.Fatalf("SKU 数量 = %d, 期望 %d", len(skus), len(demoProducts))
	}
	seen := make(map[string]bool, len(skus))
	for _, s := range skus {
		if s == "" {
			t.Error("SKU 不应为空")
		}
		if seen[s] {
			t.Errorf("SKU %q 重复", s)
		}
		seen[s] = true
	}
}

// ---------------------------------------------------------------------------
// 备份与迁移
// ---------------------------------------------------------------------------

func TestBackupCreatesUsableCopy(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	dest := filepath.Join(t.TempDir(), "backup.db")
	path, err := Backup(ctx, db, dest, false)
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	if path != dest {
		t.Errorf("备份路径 = %q, 期望 %q", path, dest)
	}

	sum, err := InspectBackup(ctx, path)
	if err != nil {
		t.Fatalf("校验备份失败: %v", err)
	}
	if !sum.IntegrityOK {
		t.Error("备份文件完整性检查未通过")
	}
	if sum.Size == 0 {
		t.Error("备份文件大小为 0")
	}
	for _, table := range []string{"users", "categories", "suppliers", "products"} {
		want := countRows(t, db, ctx, table)
		if got := sum.Counts[table]; got != want {
			t.Errorf("%s 行数 = %d, 期望 %d", table, got, want)
		}
	}
}

// TestBackupIncludesUncheckpointedWAL 是本组测试的核心：
// SQLite 处于 WAL 模式时，最新提交的数据可能还留在 inventory.db-wal 里，
// 尚未合并进主文件。VACUUM INTO 走的是读事务快照，必须能读到这部分数据 ——
// 这正是「直接 cp inventory.db 会丢数据」的根源。
func TestBackupIncludesUncheckpointedWAL(t *testing.T) {
	db, ctx := newTestDB(t)

	// 写入一条数据，不做 checkpoint
	if _, err := db.ExecContext(ctx, `
		INSERT INTO categories (name, description, `+demoFlagColumn+`, created_at, updated_at)
		VALUES ('WAL 测试分类', '', 0, '2026-01-01 00:00:00', '2026-01-01 00:00:00')`); err != nil {
		t.Fatalf("写入测试数据失败: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "wal.db")
	path, err := Backup(ctx, db, dest, false)
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}

	sum, err := InspectBackup(ctx, path)
	if err != nil {
		t.Fatalf("校验备份失败: %v", err)
	}
	if got := sum.Counts["categories"]; got != 1 {
		t.Errorf("备份应包含尚未 checkpoint 的数据，categories = %d, 期望 1", got)
	}
}

func TestBackupRefusesToOverwrite(t *testing.T) {
	db, ctx := newTestDB(t)
	seedWithDemo(t, db, ctx)

	dest := filepath.Join(t.TempDir(), "dup.db")

	if _, err := Backup(ctx, db, dest, false); err != nil {
		t.Fatalf("首次备份失败: %v", err)
	}

	if _, err := Backup(ctx, db, dest, false); err == nil {
		t.Error("目标已存在时应拒绝覆盖")
	}

	if _, err := Backup(ctx, db, dest, true); err != nil {
		t.Errorf("--force 时应允许覆盖，实际 %v", err)
	}
}

func TestBackupTargetInterpretation(t *testing.T) {
	db, ctx := newTestDB(t)

	t.Run("不带扩展名的路径视为目录并自动命名", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "backups") // 目录尚不存在
		path, err := Backup(ctx, db, dir, false)
		if err != nil {
			t.Fatalf("备份失败: %v", err)
		}
		if filepath.Dir(path) != dir {
			t.Errorf("备份文件应落在 %q 下，实际 %q", dir, path)
		}
		if !isAutoBackupName(filepath.Base(path)) {
			t.Errorf("文件名 %q 不符合自动命名规则", filepath.Base(path))
		}
	})

	t.Run("以分隔符结尾视为目录", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "backups") + string(os.PathSeparator)
		path, err := Backup(ctx, db, dir, false)
		if err != nil {
			t.Fatalf("备份失败: %v", err)
		}
		if !isAutoBackupName(filepath.Base(path)) {
			t.Errorf("文件名 %q 不符合自动命名规则", filepath.Base(path))
		}
	})

	t.Run("带扩展名的路径视为文件", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "manual.db")
		path, err := Backup(ctx, db, dest, false)
		if err != nil {
			t.Fatalf("备份失败: %v", err)
		}
		if path != dest {
			t.Errorf("备份路径 = %q, 期望 %q", path, dest)
		}
	})

	t.Run("空路径报错", func(t *testing.T) {
		if _, err := Backup(ctx, db, "  ", false); err == nil {
			t.Error("空路径应报错")
		}
	})
}

func TestInspectBackupRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.db")
	if err := os.WriteFile(path, []byte("这不是一个 SQLite 文件"), 0o644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if _, err := InspectBackup(context.Background(), path); err == nil {
		t.Error("损坏的备份文件应当报错")
	}
}

func TestIsAutoBackupName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"inventory-20260928-115500.db", true},
		{"inventory-19990101-000000.db", true},
		{"inventory-20260928-11550.db", false},   // 位数不足
		{"inventory-20260928-1155000.db", false}, // 位数过多
		{"inventory-2026ab28-115500.db", false},  // 非数字
		{"inventory-20260928115500.db", false},   // 缺分隔符
		{"inventory-manual.db", false},
		{"mybackup.db", false},
		{"inventory-20260928-115500.db.bak", false},
	}

	for _, tc := range tests {
		if got := isAutoBackupName(tc.name); got != tc.want {
			t.Errorf("isAutoBackupName(%q) = %v, 期望 %v", tc.name, got, tc.want)
		}
	}
}

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()

	// 5 份自动命名备份，时间递增
	auto := []string{
		"inventory-20260101-000000.db",
		"inventory-20260102-000000.db",
		"inventory-20260103-000000.db",
		"inventory-20260104-000000.db",
		"inventory-20260105-000000.db",
	}
	// 不应被清理的文件
	keep := []string{"mybackup.db", "inventory-manual.db", "notes.txt"}
	for _, name := range append(append([]string{}, auto...), keep...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", name, err)
		}
	}

	t.Run("keep<=0 不清理", func(t *testing.T) {
		removed, err := PruneBackups(dir, 0)
		if err != nil {
			t.Fatalf("清理失败: %v", err)
		}
		if len(removed) != 0 {
			t.Errorf("不应删除任何文件，实际 %v", removed)
		}
	})

	removed, err := PruneBackups(dir, 3)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	wantRemoved := []string{auto[1], auto[0]} // 最新的 3 份保留，删掉最旧的 2 份
	if len(removed) != len(wantRemoved) {
		t.Fatalf("删除 %d 份，期望 %d 份：%v", len(removed), len(wantRemoved), removed)
	}
	for i, name := range wantRemoved {
		if removed[i] != name {
			t.Errorf("第 %d 个被删文件 = %q, 期望 %q", i, removed[i], name)
		}
	}

	// 最新的 3 份与手工文件都还在
	for _, name := range append([]string{auto[2], auto[3], auto[4]}, keep...) {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s 不应被删除: %v", name, err)
		}
	}
	// 最旧的 2 份已删除
	for _, name := range wantRemoved {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s 应已被删除", name)
		}
	}

	t.Run("数量不足时不动", func(t *testing.T) {
		removed, err := PruneBackups(dir, 10)
		if err != nil {
			t.Fatalf("清理失败: %v", err)
		}
		if len(removed) != 0 {
			t.Errorf("备份数少于保留数时不应删除，实际 %v", removed)
		}
	})
}

// ---------------------------------------------------------------------------
// 备份目录管理
// ---------------------------------------------------------------------------

func TestListBackups(t *testing.T) {
	t.Run("目录不存在返回空列表", func(t *testing.T) {
		list, err := ListBackups(filepath.Join(t.TempDir(), "nope"))
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("期望空列表，实际 %v", list)
		}
	})

	dir := t.TempDir()
	write := func(name string, size int) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", name, err)
		}
	}
	write("inventory-20260101-000000.db", 10)
	write("inventory-20260103-000000.db", 30)
	write("manual.db", 20)
	// 非 .db 与子目录都应被忽略
	write("notes.txt", 5)
	if err := os.Mkdir(filepath.Join(dir, "sub.db"), 0o755); err != nil {
		t.Fatalf("创建子目录失败: %v", err)
	}

	list, err := ListBackups(dir)
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("期望 3 个备份，实际 %d：%v", len(list), list)
	}

	// 自动命名标记
	auto := map[string]bool{}
	for _, f := range list {
		auto[f.Name] = f.Auto
	}
	if !auto["inventory-20260101-000000.db"] || !auto["inventory-20260103-000000.db"] {
		t.Error("自动命名的备份 Auto 应为 true")
	}
	if auto["manual.db"] {
		t.Error("手工命名的备份 Auto 应为 false")
	}

	// 大小被正确读出
	for _, f := range list {
		if f.Name == "manual.db" && f.Size != 20 {
			t.Errorf("manual.db 大小 = %d, 期望 20", f.Size)
		}
	}

	// 时间倒序：刚刚写入的三个文件时间相近，但至少应满足「不报错且稳定」
	t.Run("顺序稳定", func(t *testing.T) {
		again, err := ListBackups(dir)
		if err != nil {
			t.Fatalf("列目录失败: %v", err)
		}
		for i := range again {
			if again[i].Name != list[i].Name {
				t.Fatalf("两次结果顺序不一致：%v vs %v", list, again)
			}
		}
	})
}

func TestResolveBackupPath(t *testing.T) {
	dir := filepath.Join(string(os.PathSeparator), "var", "backups", "inventory")

	bad := []struct {
		name string
		in   string
	}{
		{"空名字", ""},
		{"纯空白", "   "},
		{"上级目录", "../inventory.db"},
		{"多级穿越", "../../etc/passwd"},
		{"绝对路径", "/etc/passwd"},
		{"含子目录", "sub/a.db"},
		{"反斜杠穿越", `..\..\a.db`},
		{"扩展名不符", "notes.txt"},
		{"无扩展名", "inventory"},
		{"伪装成 db 的穿越", "../x.db"},
	}
	for _, tc := range bad {
		t.Run("拒绝/"+tc.name, func(t *testing.T) {
			if got, err := ResolveBackupPath(dir, tc.in); err == nil {
				t.Errorf("应被拒绝，却返回 %q", got)
			}
		})
	}

	t.Run("接受正常文件名", func(t *testing.T) {
		got, err := ResolveBackupPath(dir, "inventory-20260101-000000.db")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		want := filepath.Join(dir, "inventory-20260101-000000.db")
		if got != want {
			t.Errorf("得到 %q, 期望 %q", got, want)
		}
	})

	t.Run("目录带尾部分隔符也能解析", func(t *testing.T) {
		got, err := ResolveBackupPath(dir+string(os.PathSeparator), "a.db")
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if filepath.Dir(got) != filepath.Clean(dir) {
			t.Errorf("解析结果越出目录：%q", got)
		}
	})
}

func TestDeleteBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "inventory-20260101-000000.db")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if err := DeleteBackup(dir, "inventory-20260101-000000.db"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Error("文件应已被删除")
	}

	t.Run("越界名字被拒绝", func(t *testing.T) {
		if err := DeleteBackup(dir, "../x.db"); err == nil {
			t.Error("应拒绝越界名字")
		}
	})

	t.Run("不存在时报错", func(t *testing.T) {
		if err := DeleteBackup(dir, "missing.db"); err == nil {
			t.Error("应报告文件不存在")
		}
	})
}

// ---------------------------------------------------------------------------
// 恢复：暂存与启动时换入
// ---------------------------------------------------------------------------

// makeRestorableBackup 生成一份可用的备份文件，返回其路径与数据库路径。
func makeRestorableBackup(t *testing.T) (backupPath, dbPath string) {
	t.Helper()

	dir := t.TempDir()
	dbPath = filepath.Join(dir, "inventory.db")

	ctx := context.Background()
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	seedWithDemo(t, db, ctx)

	backupPath, err = Backup(ctx, db, filepath.Join(dir, "snapshot.db"), false)
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	return backupPath, dbPath
}

func TestStageRestoreRejectsInvalidFile(t *testing.T) {
	_, dbPath := makeRestorableBackup(t)
	ctx := context.Background()

	garbage := filepath.Join(t.TempDir(), "garbage.db")
	if err := os.WriteFile(garbage, []byte("这根本不是数据库"), 0o644); err != nil {
		t.Fatalf("写入垃圾文件失败: %v", err)
	}

	if _, err := StageRestore(ctx, dbPath, garbage); err == nil {
		t.Fatal("垃圾文件应被拒绝")
	}

	// 关键：拒绝之后不能留下待恢复文件，否则重启时会把坏文件换进去
	if _, err := os.Stat(PendingRestorePath(dbPath)); !errors.Is(err, os.ErrNotExist) {
		t.Error("拒绝后不应残留待恢复文件")
	}
}

func TestStageAndDiscardRestore(t *testing.T) {
	backupPath, dbPath := makeRestorableBackup(t)
	ctx := context.Background()

	sum, err := StageRestore(ctx, dbPath, backupPath)
	if err != nil {
		t.Fatalf("暂存失败: %v", err)
	}
	if !sum.IntegrityOK {
		t.Error("暂存的备份应通过完整性检查")
	}
	if sum.Counts["products"] == 0 {
		t.Error("暂存的备份应包含演示商品")
	}

	pending, err := PendingRestore(ctx, dbPath)
	if err != nil {
		t.Fatalf("读取待恢复文件失败: %v", err)
	}
	if pending == nil {
		t.Fatal("应能读到待恢复文件")
	}
	if pending.Counts["products"] != sum.Counts["products"] {
		t.Errorf("待恢复文件商品数 = %d, 期望 %d",
			pending.Counts["products"], sum.Counts["products"])
	}

	if err := DiscardPendingRestore(dbPath); err != nil {
		t.Fatalf("放弃失败: %v", err)
	}
	if _, err := os.Stat(PendingRestorePath(dbPath)); !errors.Is(err, os.ErrNotExist) {
		t.Error("放弃后不应残留待恢复文件")
	}

	// 幂等：再放弃一次不应报错
	if err := DiscardPendingRestore(dbPath); err != nil {
		t.Errorf("重复放弃不应报错: %v", err)
	}
}

func TestApplyPendingRestoreNoPending(t *testing.T) {
	_, dbPath := makeRestorableBackup(t)

	result, err := ApplyPendingRestore(context.Background(), dbPath, testLogger())
	if err != nil {
		t.Fatalf("没有待恢复文件时不应报错: %v", err)
	}
	if result != nil {
		t.Errorf("没有待恢复文件时应返回 nil，实际 %+v", result)
	}
}

func TestApplyPendingRestoreSwapsDatabase(t *testing.T) {
	ctx := context.Background()
	backupPath, dbPath := makeRestorableBackup(t)

	// 暂存后把当前库改成「另一份数据」，用来验证换入确实生效
	if _, err := StageRestore(ctx, dbPath, backupPath); err != nil {
		t.Fatalf("暂存失败: %v", err)
	}

	live, err := Open(dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	// 清空商品，模拟「当前库和备份不一样」
	if _, err := live.ExecContext(ctx, `DELETE FROM stock_movements`); err != nil {
		t.Fatalf("清理流水失败: %v", err)
	}
	if _, err := live.ExecContext(ctx, `DELETE FROM products`); err != nil {
		t.Fatalf("清理商品失败: %v", err)
	}
	before := countRows(t, live, ctx, "products")
	if before != 0 {
		t.Fatalf("前置条件不成立：当前库商品数应为 0，实际 %d", before)
	}
	if err := live.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}

	result, err := ApplyPendingRestore(ctx, dbPath, testLogger())
	if err != nil {
		t.Fatalf("应用恢复失败: %v", err)
	}
	if result == nil || !result.Applied {
		t.Fatal("应报告已应用恢复")
	}
	if result.Archived == "" {
		t.Error("原库存在时应给出归档路径")
	}
	if _, err := os.Stat(result.Archived); err != nil {
		t.Errorf("归档文件应存在: %v", err)
	}

	// 待恢复文件应已消失
	if _, err := os.Stat(PendingRestorePath(dbPath)); !errors.Is(err, os.ErrNotExist) {
		t.Error("换入后不应残留待恢复文件")
	}

	// 换入后的库应恢复出演示商品
	restored, err := Open(dbPath)
	if err != nil {
		t.Fatalf("打开恢复后的数据库失败: %v", err)
	}
	defer restored.Close()

	if got := countRows(t, restored, ctx, "products"); got != result.Counts["products"] {
		t.Errorf("恢复后商品数 = %d, 期望 %d", got, result.Counts["products"])
	}
	if got := countRows(t, restored, ctx, "users"); got == 0 {
		t.Error("恢复后应至少有一个用户")
	}
}

func TestApplyPendingRestoreRemovesStaleWAL(t *testing.T) {
	ctx := context.Background()
	backupPath, dbPath := makeRestorableBackup(t)

	if _, err := StageRestore(ctx, dbPath, backupPath); err != nil {
		t.Fatalf("暂存失败: %v", err)
	}

	// 伪造一份「旧库遗留」的 WAL / SHM。
	// 换入新库时若不清掉它们，SQLite 会把旧日志应用到新库上。
	stale := []string{dbPath + "-wal", dbPath + "-shm"}
	for _, p := range stale {
		if err := os.WriteFile(p, []byte("stale"), 0o600); err != nil {
			t.Fatalf("伪造 %s 失败: %v", p, err)
		}
	}

	if _, err := ApplyPendingRestore(ctx, dbPath, testLogger()); err != nil {
		t.Fatalf("应用恢复失败: %v", err)
	}

	for _, p := range stale {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s 应被清理掉", filepath.Base(p))
		}
	}
}

func TestApplyPendingRestoreQuarantinesCorruptFile(t *testing.T) {
	_, dbPath := makeRestorableBackup(t)
	ctx := context.Background()

	// 绕过 StageRestore 的校验，直接放一个坏文件，模拟磁盘损坏或人为篡改
	pending := PendingRestorePath(dbPath)
	if err := os.WriteFile(pending, []byte("坏掉的数据库"), 0o600); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if _, err := ApplyPendingRestore(ctx, dbPath, testLogger()); err == nil {
		t.Fatal("损坏的待恢复文件应导致报错")
	}

	// 改名保留证据，且不能留在原位 —— 否则每次重启都会重复失败
	if _, err := os.Stat(pending); !errors.Is(err, os.ErrNotExist) {
		t.Error("损坏文件应从待恢复位置移走")
	}
	if _, err := os.Stat(dbPath + restoreFailedSuffix); err != nil {
		t.Errorf("损坏文件应改名为 %s 保留: %v", restoreFailedSuffix, err)
	}
	// 原库必须还在，不能被损坏的文件顶掉
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("原数据库应保持不动: %v", err)
	}
}
