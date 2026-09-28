// Package database 负责 SQLite 连接、表结构初始化与种子数据。
//
// 采用纯 Go 驱动 modernc.org/sqlite，因此无需 CGO，
// 交叉编译与 GitHub Actions 构建都非常简单。
package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动

	"inventory/internal/models"
)

//go:embed schema.sql
var schemaFS embed.FS

// Open 打开（并在需要时创建）SQLite 数据库，返回连接池。
func Open(path string) (*sql.DB, error) {
	dsn := buildDSN(path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// SQLite 在 WAL 模式下允许多读单写；限制连接数可避免写锁竞争。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	return db, nil
}

// buildDSN 拼装 modernc.org/sqlite 的 DSN，开启 WAL、外键与忙等待。
func buildDSN(path string) string {
	p := filepath.ToSlash(path)
	params := []string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(1)",
		"_pragma=synchronous(NORMAL)",
		// 事务一开启就获取写锁，避免「读后升级为写」导致的 SQLITE_BUSY 死锁
		"_txlock=immediate",
	}
	sep := "?"
	if strings.Contains(p, "?") {
		sep = "&"
	}
	return "file:" + p + sep + strings.Join(params, "&")
}

// demoFlagColumn 标记由内置演示数据写入的行，使清理不必依赖名称猜测。
const demoFlagColumn = "is_demo"

// Migrate 执行建表语句，保证结构最新。
func Migrate(ctx context.Context, db *sql.DB) error {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("读取数据库结构文件失败: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(raw)); err != nil {
		return fmt.Errorf("执行数据库迁移失败: %w", err)
	}

	// 旧版本数据库没有 is_demo 列 —— CREATE TABLE IF NOT EXISTS 不会给已存在的表补列，
	// 因此这里显式检查并补齐，否则老部署无法精确识别演示数据。
	for _, table := range []string{"categories", "suppliers", "products"} {
		ddl := "ALTER TABLE " + table + " ADD COLUMN " + demoFlagColumn + " INTEGER NOT NULL DEFAULT 0"
		if err := ensureColumn(ctx, db, table, demoFlagColumn, ddl); err != nil {
			return err
		}
	}
	return nil
}

// ensureColumn 在目标列不存在时执行 ddl 将其补上；已存在则不做任何事。
func ensureColumn(ctx context.Context, db *sql.DB, table, column, ddl string) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return fmt.Errorf("读取 %s 表结构失败: %w", table, err)
	}

	exists := false
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
			rows.Close()
			return fmt.Errorf("解析 %s 表结构失败: %w", table, err)
		}
		if strings.EqualFold(name, column) {
			exists = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("读取 %s 表结构失败: %w", table, err)
	}
	if exists {
		return nil
	}

	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("为 %s 添加 %s 列失败: %w", table, column, err)
	}
	return nil
}

// SeedOptions 控制初始化数据的写入。
type SeedOptions struct {
	AdminUsername string
	AdminEmail    string
	AdminPassword string
	WithDemoData  bool
}

// Seed 在数据库为空时创建默认管理员；WithDemoData 为真时额外写入演示数据。
// 该函数幂等：重复调用不会产生重复数据。
func Seed(ctx context.Context, db *sql.DB, opts SeedOptions, logger *slog.Logger) error {
	created, err := seedAdmin(ctx, db, opts, logger)
	if err != nil {
		return err
	}

	if opts.WithDemoData {
		if err := seedDemoData(ctx, db, logger); err != nil {
			return err
		}
	}

	_ = created
	return nil
}

// seedAdmin 若系统中尚无任何用户，则创建默认管理员账号。
func seedAdmin(ctx context.Context, db *sql.DB, opts SeedOptions, logger *slog.Logger) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, fmt.Errorf("统计用户数量失败: %w", err)
	}
	if count > 0 {
		return false, nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(opts.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return false, fmt.Errorf("生成管理员密码失败: %w", err)
	}

	now := time.Now().UTC()
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (username, email, password_hash, full_name, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		opts.AdminUsername, opts.AdminEmail, string(hash), "系统管理员",
		string(models.RoleAdmin), models.UserStatusActive, now, now,
	)
	if err != nil {
		return false, fmt.Errorf("创建默认管理员失败: %w", err)
	}

	if logger != nil {
		logger.Info("已创建默认管理员账号",
			"username", opts.AdminUsername,
			"email", opts.AdminEmail,
			"提示", "请登录后立即修改默认密码",
		)
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// 内置演示数据
//
// 下面三份列表是演示数据的**唯一来源**：写入（Seed）与清理（PurgeDemoData）
// 都从这里取值。任何一处新增演示条目，两边都会同步，不会出现「写了清不掉」。
// ---------------------------------------------------------------------------

type demoCategory struct{ name, desc string }

type demoSupplier struct{ name, contact, phone, email, addr string }

type demoProduct struct {
	sku, name, unit, location string
	catIdx, supIdx            int
	cost, sale                float64
	qty, safety               int
}

var demoCategories = []demoCategory{
	{"电子配件", "数据线、充电器、转接头等"},
	{"办公用品", "纸张、笔、文件夹等"},
	{"仓储耗材", "纸箱、胶带、标签等"},
	{"工具仪表", "螺丝刀、万用表、卷尺等"},
}

var demoSuppliers = []demoSupplier{
	{"深圳市宏远电子有限公司", "张伟", "0755-88886666", "sales@hongyuan.example.com", "广东省深圳市南山区科技园"},
	{"上海办公伙伴贸易有限公司", "李娜", "021-66668888", "service@officepartner.example.com", "上海市浦东新区张江路 88 号"},
	{"杭州仓储用品制造厂", "王强", "0571-12345678", "info@hzwms.example.com", "浙江省杭州市余杭区工业园"},
}

var demoProducts = []demoProduct{
	{"SKU-1001", "Type-C 数据线 1m", "条", "A-01-01", 0, 0, 8.50, 19.90, 240, 50},
	{"SKU-1002", "65W 氮化镓充电器", "个", "A-01-02", 0, 0, 62.00, 129.00, 36, 20},
	{"SKU-1003", "HDMI 2.1 高清线 2m", "条", "A-01-03", 0, 0, 25.00, 59.00, 12, 20},
	{"SKU-2001", "A4 复印纸 70g（500张）", "包", "B-02-01", 1, 1, 16.80, 25.00, 380, 100},
	{"SKU-2002", "中性签字笔 0.5mm（12支）", "盒", "B-02-02", 1, 1, 9.60, 18.00, 64, 30},
	{"SKU-2003", "A4 牛皮纸档案袋", "个", "B-02-03", 1, 1, 0.85, 2.00, 1200, 300},
	{"SKU-3001", "五层瓦楞纸箱 60×40×40", "个", "C-03-01", 2, 2, 3.20, 6.50, 150, 200},
	{"SKU-3002", "透明封箱胶带 4.5cm", "卷", "C-03-02", 2, 2, 4.10, 9.00, 420, 100},
	{"SKU-3003", "热敏标签纸 100×100", "卷", "C-03-03", 2, 2, 12.00, 26.00, 8, 40},
	{"SKU-4001", "十字螺丝刀套装", "套", "D-04-01", 3, 0, 22.00, 49.00, 55, 15},
	{"SKU-4002", "数字万用表 UT136B+", "台", "D-04-02", 3, 0, 118.00, 199.00, 14, 5},
	{"SKU-4003", "5 米钢卷尺", "把", "D-04-03", 3, 2, 6.80, 15.00, 0, 10},
}

// DemoSkus 返回内置演示商品使用的 SKU 列表。
func DemoSkus() []string {
	out := make([]string, 0, len(demoProducts))
	for _, p := range demoProducts {
		out = append(out, p.sku)
	}
	return out
}

// demoCategoryNames 返回内置演示分类的名称列表。
func demoCategoryNames() []string {
	out := make([]string, 0, len(demoCategories))
	for _, c := range demoCategories {
		out = append(out, c.name)
	}
	return out
}

// demoSupplierNames 返回内置演示供应商的名称列表。
func demoSupplierNames() []string {
	out := make([]string, 0, len(demoSuppliers))
	for _, s := range demoSuppliers {
		out = append(out, s.name)
	}
	return out
}

// seedDemoData 仅在数据库为空时写入一组演示数据，便于首次体验。
// 所有写入的行都会打上 is_demo 标记，方便日后用 PurgeDemoData 精确清理。
func seedDemoData(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM products`).Scan(&count); err != nil {
		return fmt.Errorf("统计商品数量失败: %w", err)
	}
	if count > 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()

	// 分类
	catIDs := make([]int64, 0, len(demoCategories))
	for _, c := range demoCategories {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO categories (name, description, `+demoFlagColumn+`, created_at, updated_at)
			 VALUES (?, ?, 1, ?, ?)`,
			c.name, c.desc, now, now)
		if err != nil {
			return fmt.Errorf("写入演示分类失败: %w", err)
		}
		id, _ := res.LastInsertId()
		catIDs = append(catIDs, id)
	}

	// 供应商
	supIDs := make([]int64, 0, len(demoSuppliers))
	for _, s := range demoSuppliers {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO suppliers (name, contact_person, phone, email, address, note, `+demoFlagColumn+`, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, '', 1, ?, ?)`,
			s.name, s.contact, s.phone, s.email, s.addr, now, now)
		if err != nil {
			return fmt.Errorf("写入演示供应商失败: %w", err)
		}
		id, _ := res.LastInsertId()
		supIDs = append(supIDs, id)
	}

	// 商品
	for _, p := range demoProducts {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO products
				(sku, name, barcode, category_id, supplier_id, unit, cost_price, sale_price,
				 quantity, safety_stock, location, description, status, `+demoFlagColumn+`, created_at, updated_at)
			VALUES (?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, '', 'active', 1, ?, ?)`,
			p.sku, p.name, catIDs[p.catIdx], supIDs[p.supIdx], p.unit,
			p.cost, p.sale, p.qty, p.safety, p.location, now, now)
		if err != nil {
			return fmt.Errorf("写入演示商品失败: %w", err)
		}
		pid, _ := res.LastInsertId()

		// 为期初库存生成一条流水
		if p.qty > 0 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO stock_movements
					(product_id, type, quantity, delta, unit_price, before_qty, after_qty,
					 ref_no, supplier_id, operator_id, note, created_at)
				VALUES (?, 'init', ?, ?, ?, 0, ?, '', ?, 1, '系统演示数据期初建账', ?)`,
				pid, p.qty, p.qty, p.cost, p.qty, supIDs[p.supIdx], now); err != nil {
				return fmt.Errorf("写入演示流水失败: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if logger != nil {
		logger.Info("已写入演示数据",
			"分类", len(demoCategories),
			"供应商", len(demoSuppliers),
			"商品", len(demoProducts),
			"提示", "生产环境请用 INVENTORY_SEED_DEMO_DATA=false 关闭，或用 --purge-demo-data 清理",
		)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 演示数据清理
// ---------------------------------------------------------------------------

// PurgeOptions 控制演示数据的清理行为。
type PurgeOptions struct {
	// DryRun 为真时只统计将被删除的数据，不产生任何实际改动。
	DryRun bool
}

// PurgeResult 汇总一次清理的结果。
type PurgeResult struct {
	Products   int // 删除的商品数
	Movements  int // 删除的库存流水数
	Categories int // 删除的分类数
	Suppliers  int // 删除的供应商数

	// 因仍被非演示商品引用而保留下来的名称，避免误删正在使用的数据。
	SkippedCategories []string
	SkippedSuppliers  []string
}

// Empty 判断本次清理是否没有改动任何数据。
func (r *PurgeResult) Empty() bool {
	return r.Products == 0 && r.Movements == 0 && r.Categories == 0 && r.Suppliers == 0
}

// PurgeDemoData 删除内置演示数据。
//
// 判定规则（宁可漏删也不误删）：
//   - products   : is_demo=1，或 SKU 命中内置演示 SKU（兼容加标记之前写入的旧库）
//   - stock_movements : 仅删除挂在上述商品下的流水
//   - categories / suppliers : is_demo=1 或名称命中内置演示清单，**且已无商品引用**
//
// DryRun 为真时在事务内完成同样的统计后回滚，数据库保持不变。
func PurgeDemoData(ctx context.Context, db *sql.DB, opts PurgeOptions, logger *slog.Logger) (*PurgeResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // 提交成功后回滚是无害的空操作

	res := &PurgeResult{}

	// ---------- 1. 演示商品与其库存流水 ----------
	skuArgs := make([]any, 0, len(demoProducts))
	for _, p := range demoProducts {
		skuArgs = append(skuArgs, p.sku)
	}
	productWhere := fmt.Sprintf("%s = 1 OR sku IN (%s)", demoFlagColumn, placeholders(len(skuArgs)))

	productIDs, err := selectIDs(ctx, tx, `SELECT id FROM products WHERE `+productWhere, skuArgs...)
	if err != nil {
		return nil, err
	}
	res.Products = len(productIDs)

	if len(productIDs) > 0 {
		args := toAny(productIDs)
		in := placeholders(len(args))

		if res.Movements, err = deleteRows(ctx, tx,
			`DELETE FROM stock_movements WHERE product_id IN (`+in+`)`, args...); err != nil {
			return nil, fmt.Errorf("删除演示库存流水失败: %w", err)
		}
		if _, err = deleteRows(ctx, tx,
			`DELETE FROM products WHERE id IN (`+in+`)`, args...); err != nil {
			return nil, fmt.Errorf("删除演示商品失败: %w", err)
		}
	}

	// ---------- 2. 演示分类 / 供应商（仅删除已无商品引用的） ----------
	if res.Categories, res.SkippedCategories, err = purgeMasterData(
		ctx, tx, "categories", "category_id", demoCategoryNames()); err != nil {
		return nil, err
	}
	if res.Suppliers, res.SkippedSuppliers, err = purgeMasterData(
		ctx, tx, "suppliers", "supplier_id", demoSupplierNames()); err != nil {
		return nil, err
	}

	if opts.DryRun {
		// defer 中的 Rollback 会撤销上面全部改动
		return res, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交清理事务失败: %w", err)
	}

	if logger != nil {
		logger.Info("已清理演示数据",
			"商品", res.Products, "流水", res.Movements,
			"分类", res.Categories, "供应商", res.Suppliers,
			"保留分类", res.SkippedCategories, "保留供应商", res.SkippedSuppliers)
	}
	return res, nil
}

// purgeMasterData 按名称删除主数据表中由演示数据创建、且已无商品引用的行。
// table 为主数据表名，fk 为 products 指向它的外键列名。
func purgeMasterData(ctx context.Context, tx *sql.Tx, table, fk string, names []string) (int, []string, error) {
	var (
		deleted int
		skipped []string
	)

	for _, name := range names {
		var refs int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM products p JOIN `+table+` m ON p.`+fk+` = m.id WHERE m.name = ?`,
			name).Scan(&refs); err != nil {
			return deleted, skipped, fmt.Errorf("检查 %s %q 的引用失败: %w", table, name, err)
		}
		if refs > 0 {
			skipped = append(skipped, name)
			continue
		}

		n, err := deleteRows(ctx, tx, `DELETE FROM `+table+` WHERE name = ?`, name)
		if err != nil {
			return deleted, skipped, fmt.Errorf("删除 %s %q 失败: %w", table, name, err)
		}
		deleted += n
	}
	return deleted, skipped, nil
}

// placeholders 返回 n 个以逗号分隔的 SQL 占位符，例如 "?,?,?"。
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// selectIDs 执行只返回单列 id 的查询。
func selectIDs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询待清理记录失败: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("读取待清理记录失败: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历待清理记录失败: %w", err)
	}
	return ids, nil
}

// deleteRows 执行删除语句并返回受影响行数。
func deleteRows(ctx context.Context, tx *sql.Tx, query string, args ...any) (int, error) {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		// 驱动不支持时按 0 计，不影响删除本身是否成功
		return 0, nil
	}
	return int(n), nil
}

// toAny 把 []int64 转成 database/sql 需要的 []any。
func toAny(ids []int64) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

// ---------------------------------------------------------------------------
// 备份与迁移
//
// SQLite 开启了 WAL 模式，最新提交的事务可能还停留在 inventory.db-wal 里，
// 尚未合并进主文件。**只拷贝 inventory.db 会丢掉这部分数据** ——
// 这是换机器/做备份时最容易踩的坑。
//
// 正确做法是用 VACUUM INTO：它在读事务里取一份快照，产出一个已经整理过、
// WAL 已合并的单文件数据库，直接拷走即可，而且**服务运行中执行也是安全的**。
// ---------------------------------------------------------------------------

// backupFilePrefix / backupFileLayout 定义自动生成备份文件名时的格式。
const (
	backupFilePrefix = "inventory-"
	backupFileLayout = "20060102-150405"
)

// Backup 把当前数据库完整备份到 dest，返回实际写入的文件路径。
//
// dest 的解释规则（便于配合定时任务使用）：
//   - 以路径分隔符结尾、或已存在且是目录、或**不带扩展名** → 视为目录，
//     自动生成 inventory-YYYYMMDD-HHMMSS.db 形式的文件名；
//   - 其余情况视为文件路径。
//
// 想明确指定文件名时请带上扩展名，例如 /var/backups/inventory/manual.db。
//
// overwrite 为 false 时，目标文件已存在会直接报错，避免误覆盖历史备份。
func Backup(ctx context.Context, db *sql.DB, dest string, overwrite bool) (string, error) {
	if strings.TrimSpace(dest) == "" {
		return "", errors.New("未指定备份目标路径")
	}

	if isBackupDir(dest) {
		dest = filepath.Join(dest, backupFilePrefix+time.Now().Format(backupFileLayout)+".db")
	}

	if dir := filepath.Dir(dest); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", fmt.Errorf("创建备份目录 %s 失败: %w", dir, err)
		}
	}

	switch _, err := os.Stat(dest); {
	case err == nil:
		if !overwrite {
			return "", fmt.Errorf("备份文件已存在：%s（加 --force 可覆盖）", dest)
		}
		if err := os.Remove(dest); err != nil {
			return "", fmt.Errorf("覆盖前删除旧备份失败: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("检查备份路径失败: %w", err)
	}

	// VACUUM 语句不接受绑定参数，这里把路径拼成 SQL 字符串字面量，
	// 单引号翻倍转义，避免路径里的引号破坏语句。
	quoted := "'" + strings.ReplaceAll(dest, "'", "''") + "'"
	if _, err := db.ExecContext(ctx, "VACUUM INTO "+quoted); err != nil {
		return "", fmt.Errorf("执行备份失败: %w", err)
	}
	return dest, nil
}

// BackupSummary 描述一份备份文件的概况。
type BackupSummary struct {
	Path        string
	Size        int64
	IntegrityOK bool
	Counts      map[string]int
}

// backupCountTables 是备份校验时统计行数的表。
var backupCountTables = []string{"users", "categories", "suppliers", "products", "stock_movements"}

// InspectBackup 以只读方式打开备份文件，做完整性检查并统计关键表行数。
// 用来确认「备份出来的文件真的能用」，而不是只看到一个文件躺在磁盘上。
func InspectBackup(ctx context.Context, path string) (*BackupSummary, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("打开备份文件失败: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("备份文件无法读取: %w", err)
	}

	sum := &BackupSummary{Path: path, Counts: make(map[string]int, len(backupCountTables))}

	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return nil, fmt.Errorf("完整性检查失败: %w", err)
	}
	sum.IntegrityOK = strings.EqualFold(strings.TrimSpace(integrity), "ok")

	for _, table := range backupCountTables {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			return nil, fmt.Errorf("统计 %s 失败: %w", table, err)
		}
		sum.Counts[table] = n
	}

	if info, err := os.Stat(path); err == nil {
		sum.Size = info.Size()
	}
	return sum, nil
}

// PruneBackups 在 dir 中只保留最新的 keep 份自动命名的备份，其余删除，返回被删列表。
//
// 只处理严格匹配 inventory-YYYYMMDD-HHMMSS.db 的文件，手工命名或改名的文件一律不动，
// 避免把用户自己放进去的东西删掉。keep <= 0 时不做任何清理。
func PruneBackups(dir string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取备份目录失败: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || !isAutoBackupName(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}

	// 文件名里是零填充的时间戳，按字符串降序即时间降序
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) <= keep {
		return nil, nil
	}

	var removed []string
	for _, name := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("删除旧备份 %s 失败: %w", name, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// isAutoBackupName 判断文件名是否为 Backup 自动生成的格式。
func isAutoBackupName(name string) bool {
	if !strings.HasPrefix(name, backupFilePrefix) || !strings.HasSuffix(name, ".db") {
		return false
	}
	// inventory-YYYYMMDD-HHMMSS.db → 中间固定 15 个字符
	mid := strings.TrimSuffix(strings.TrimPrefix(name, backupFilePrefix), ".db")
	if len(mid) != len(backupFileLayout) {
		return false
	}
	for i, r := range mid {
		switch i {
		case 8:
			if r != '-' {
				return false
			}
		default:
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// isDir 判断路径是否为一个已存在的目录。
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isBackupDir 判断备份目标应当被当作目录处理。
//
// 除了「已存在的目录」与「以分隔符结尾」这两种明确情况，
// 还把**不带扩展名的路径**当作目录 —— 这样 `--backup-db /var/backups/inventory`
// 在目录尚未创建时也能按预期工作，而不是意外生成一个名叫 inventory 的文件。
func isBackupDir(dest string) bool {
	if isDir(dest) || strings.HasSuffix(dest, string(os.PathSeparator)) {
		return true
	}
	return filepath.Ext(dest) == ""
}

// IsNotFound 判断错误是否为「记录不存在」。
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
