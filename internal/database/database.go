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
	"path/filepath"
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

// Migrate 执行建表语句，保证结构最新。
func Migrate(ctx context.Context, db *sql.DB) error {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("读取数据库结构文件失败: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(raw)); err != nil {
		return fmt.Errorf("执行数据库迁移失败: %w", err)
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

// seedDemoData 仅在数据库为空时写入一组演示数据，便于首次体验。
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
	categories := []struct{ name, desc string }{
		{"电子配件", "数据线、充电器、转接头等"},
		{"办公用品", "纸张、笔、文件夹等"},
		{"仓储耗材", "纸箱、胶带、标签等"},
		{"工具仪表", "螺丝刀、万用表、卷尺等"},
	}
	catIDs := make([]int64, 0, len(categories))
	for _, c := range categories {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO categories (name, description, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			c.name, c.desc, now, now)
		if err != nil {
			return fmt.Errorf("写入演示分类失败: %w", err)
		}
		id, _ := res.LastInsertId()
		catIDs = append(catIDs, id)
	}

	// 供应商
	suppliers := []struct{ name, contact, phone, email, addr string }{
		{"深圳市宏远电子有限公司", "张伟", "0755-88886666", "sales@hongyuan.example.com", "广东省深圳市南山区科技园"},
		{"上海办公伙伴贸易有限公司", "李娜", "021-66668888", "service@officepartner.example.com", "上海市浦东新区张江路 88 号"},
		{"杭州仓储用品制造厂", "王强", "0571-12345678", "info@hzwms.example.com", "浙江省杭州市余杭区工业园"},
	}
	supIDs := make([]int64, 0, len(suppliers))
	for _, s := range suppliers {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO suppliers (name, contact_person, phone, email, address, note, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, '', ?, ?)`,
			s.name, s.contact, s.phone, s.email, s.addr, now, now)
		if err != nil {
			return fmt.Errorf("写入演示供应商失败: %w", err)
		}
		id, _ := res.LastInsertId()
		supIDs = append(supIDs, id)
	}

	// 商品
	type demoProduct struct {
		sku, name, unit, location string
		catIdx, supIdx            int
		cost, sale                float64
		qty, safety               int
	}
	products := []demoProduct{
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

	for _, p := range products {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO products
				(sku, name, barcode, category_id, supplier_id, unit, cost_price, sale_price,
				 quantity, safety_stock, location, description, status, created_at, updated_at)
			VALUES (?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, '', 'active', ?, ?)`,
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
		logger.Info("已写入演示数据", "分类", len(categories), "供应商", len(suppliers), "商品", len(products))
	}
	return nil
}

// IsNotFound 判断错误是否为「记录不存在」。
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
