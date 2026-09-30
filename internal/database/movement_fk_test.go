package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// legacyStockMovementsDDL 是早期版本中 stock_movements 的定义。
//
// product_id 用 CASCADE、supplier_id 用 SET NULL —— 两者都会在删除主表行时
// 改写历史流水，正是本次要收紧的对象。
const legacyStockMovementsDDL = `CREATE TABLE stock_movements (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    type        TEXT    NOT NULL,
    quantity    INTEGER NOT NULL,
    delta       INTEGER NOT NULL,
    unit_price  REAL    NOT NULL DEFAULT 0,
    before_qty  INTEGER NOT NULL DEFAULT 0,
    after_qty   INTEGER NOT NULL DEFAULT 0,
    ref_no      TEXT    NOT NULL DEFAULT '',
    supplier_id INTEGER REFERENCES suppliers(id) ON DELETE SET NULL,
    operator_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    note        TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL
);`

// openLegacyDB 造一个「老库」：其余表都是当前结构，
// 只有 stock_movements 带着早期的危险外键定义。
//
// 先跑一次 Migrate 建出完整标准库再换掉这一张表，比手工拼一套简化表更稳 ——
// 后者得跟着 schema.sql 里每个 NOT NULL 列和索引走，改一处就崩一处。
func openLegacyDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()

	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("建库失败: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE stock_movements`); err != nil {
		t.Fatalf("删除标准流水表失败: %v", err)
	}
	if _, err := db.ExecContext(ctx, legacyStockMovementsDDL); err != nil {
		t.Fatalf("建旧版流水表失败: %v", err)
	}

	const now = "2026-01-01 00:00:00"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (username, email, password_hash, created_at, updated_at)
		VALUES ('tester', 'tester@example.com', 'x', ?, ?);
		INSERT INTO suppliers (name, created_at, updated_at) VALUES ('测试供应商', ?, ?);
		INSERT INTO products (sku, name, created_at, updated_at) VALUES ('SKU-1', '测试商品', ?, ?);
		INSERT INTO stock_movements
			(product_id, type, quantity, delta, unit_price, before_qty, after_qty,
			 ref_no, supplier_id, operator_id, note, created_at)
		VALUES (1, 'in', 10, 10, 1.5, 0, 10, 'RK-001', 1, 1, '旧库遗留', ?)`,
		now, now, now, now, now, now, now,
	); err != nil {
		t.Fatalf("写入旧版数据失败: %v", err)
	}
	return db, ctx
}

// movementOnDelete 读取 stock_movements 指定列的 ON DELETE 行为。
func movementOnDelete(t *testing.T, db *sql.DB, column string) string {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), `PRAGMA foreign_key_list(stock_movements)`)
	if err != nil {
		t.Fatalf("读取外键定义失败: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id, seq                   int
			table, from, to           sql.NullString
			onUpdate, onDelete, match sql.NullString
		)
		if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("解析外键定义失败: %v", err)
		}
		if from.String == column {
			return strings.ToUpper(onDelete.String)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历外键定义失败: %v", err)
	}
	return ""
}

// TestMigrateTightensMovementForeignKeys 验证旧库升级后外键被收紧为 RESTRICT。
//
// 这是本次修复的关键：**只改 schema.sql 对已存在的表无效** ——
// CREATE TABLE IF NOT EXISTS 会直接跳过，老库会一直带着危险的外键定义。
// 必须有显式的迁移把它们改过来，否则「有流水的商品不许删」这条约定
// 在老库上等于没有兜底。
func TestMigrateTightensMovementForeignKeys(t *testing.T) {
	db, ctx := openLegacyDB(t)

	if got := movementOnDelete(t, db, "product_id"); got != "CASCADE" {
		t.Fatalf("迁移前 product_id 的 ON DELETE 应为 CASCADE（旧定义），实际 %q", got)
	}
	if got := movementOnDelete(t, db, "supplier_id"); got != "SET NULL" {
		t.Fatalf("迁移前 supplier_id 的 ON DELETE 应为 SET NULL（旧定义），实际 %q", got)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	if got := movementOnDelete(t, db, "product_id"); got != "RESTRICT" {
		t.Errorf("迁移后 product_id 的 ON DELETE 应为 RESTRICT，实际 %q", got)
	}
	if got := movementOnDelete(t, db, "supplier_id"); got != "RESTRICT" {
		t.Errorf("迁移后 supplier_id 的 ON DELETE 应为 RESTRICT，实际 %q", got)
	}
}

// TestMigratePreservesMovementRows 重建表不能丢数据，也不能打乱自增计数。
//
// 重建表走的是「建新表 → 拷贝 → 删旧表 → 改名」，任何一步写错都会丢流水；
// 而 DROP TABLE 会连 sqlite_sequence 里的计数一起清掉，
// 不补回去的话下一条流水可能拿到一个**已被用过**的 id。
func TestMigratePreservesMovementRows(t *testing.T) {
	db, ctx := openLegacyDB(t)

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	var (
		count  int
		refNo  string
		note   string
		before int
		after  int
	)
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(ref_no), MAX(note), MAX(before_qty), MAX(after_qty)
		  FROM stock_movements`).Scan(&count, &refNo, &note, &before, &after)
	if err != nil {
		t.Fatalf("查询迁移后的流水失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("迁移后流水条数 = %d，期望 1（原样保留）", count)
	}
	if refNo != "RK-001" || note != "旧库遗留" || before != 0 || after != 10 {
		t.Errorf("流水内容在迁移后发生了变化: ref_no=%q note=%q before=%d after=%d",
			refNo, note, before, after)
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO stock_movements
			(product_id, type, quantity, delta, unit_price, before_qty, after_qty,
			 ref_no, supplier_id, operator_id, note, created_at)
		VALUES (1, 'out', 3, -3, 1.5, 10, 7, 'RK-002', 1, 1, '迁移后新增', '2026-01-02 00:00:00')`)
	if err != nil {
		t.Fatalf("迁移后写入流水失败: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("读取自增 id 失败: %v", err)
	}
	if id != 2 {
		t.Errorf("迁移后新流水的 id = %d，期望 2（自增计数未被清零）", id)
	}
}

// TestMigrateIsIdempotentForMovementForeignKeys 迁移重复执行不应再次重建表。
func TestMigrateIsIdempotentForMovementForeignKeys(t *testing.T) {
	db, ctx := openLegacyDB(t)

	for i := 0; i < 3; i++ {
		if err := Migrate(ctx, db); err != nil {
			t.Fatalf("第 %d 次迁移失败: %v", i+1, err)
		}
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stock_movements`).Scan(&count); err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if count != 1 {
		t.Errorf("重复迁移后流水条数 = %d，期望仍为 1", count)
	}
	if got := movementOnDelete(t, db, "product_id"); got != "RESTRICT" {
		t.Errorf("重复迁移后 product_id 的 ON DELETE = %q，期望 RESTRICT", got)
	}
}

// TestRebuildRefusesWhenOrphanMovementsExist 孤儿流水必须让迁移停下来而不是静默丢数据。
//
// 重建会逐行校验外键，孤儿行会让它失败。此时正确的做法是明确报错、
// 把问题交给管理员判断，而不是自作主张把流水删掉。
func TestRebuildRefusesWhenOrphanMovementsExist(t *testing.T) {
	db, ctx := openLegacyDB(t)

	// 直接把商品删掉，制造一条指向不存在商品的流水。
	// 旧定义是 CASCADE，本该连带删掉流水 —— 这里连外键一起绕过，
	// 模拟「外键曾被关闭期间产生的历史脏数据」。
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatalf("关闭外键失败: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM products WHERE id = 1`); err != nil {
		t.Fatalf("删除商品失败: %v", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("重新开启外键失败: %v", err)
	}

	err := enforceMovementForeignKeys(ctx, db)
	if err == nil {
		t.Fatal("存在孤儿流水时，收紧外键应当报错而不是默默跳过")
	}
	if !strings.Contains(err.Error(), "库存流水指向已不存在") {
		t.Errorf("错误信息应说明是孤儿流水导致，实际: %v", err)
	}
}

// TestMovementForeignKeysRejectDeletingReferencedRows 数据库层的兜底真的生效。
//
// 应用层已经做了检查，但那终究是「查」和「删」两条语句。
// 这里绕过应用层直接删，验证数据库会拒绝 —— 这才是最后一道保险。
func TestMovementForeignKeysRejectDeletingReferencedRows(t *testing.T) {
	db, ctx := openLegacyDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM products WHERE id = 1`); err == nil {
		t.Error("删除有流水的商品应被数据库拒绝（RESTRICT 未生效）")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM suppliers WHERE id = 1`); err == nil {
		t.Error("删除有流水的供应商应被数据库拒绝（RESTRICT 未生效）")
	}

	// 而没有任何流水的记录仍可正常删除
	if _, err := db.ExecContext(ctx, `
		INSERT INTO products (sku, name, created_at, updated_at)
		VALUES ('SKU-2', '无流水商品', '2026-01-01 00:00:00', '2026-01-01 00:00:00')`); err != nil {
		t.Fatalf("写入无流水商品失败: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM products WHERE sku = 'SKU-2'`); err != nil {
		t.Errorf("删除无流水的商品不应被拒绝: %v", err)
	}
}
