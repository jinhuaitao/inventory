package services

import (
	"context"
	"fmt"
	"time"

	"inventory/internal/models"
)

// DashboardStats 汇总仪表盘所需的全部核心指标。
func (s *Store) DashboardStats(ctx context.Context) (*models.DashboardStats, error) {
	st := &models.DashboardStats{}
	db := s.db

	queries := []struct {
		sql  string
		args []any
		dest any
	}{
		{`SELECT COUNT(*) FROM products WHERE status != 'archived'`, nil, &st.TotalProducts},
		{`SELECT COUNT(*) FROM categories`, nil, &st.TotalCategories},
		{`SELECT COUNT(*) FROM suppliers`, nil, &st.TotalSuppliers},
		{`SELECT COUNT(*) FROM users WHERE status = 'active'`, nil, &st.TotalUsers},
		{`SELECT COUNT(*) FROM stock_movements`, nil, &st.TotalMovements},
		{
			`SELECT COALESCE(SUM(quantity), 0), COALESCE(SUM(quantity * cost_price), 0)
			 FROM products WHERE status != 'archived'`,
			nil, &sqlPair{&st.TotalStock, &st.StockValue},
		},
		{
			`SELECT COUNT(*) FROM products
			 WHERE status != 'archived' AND quantity > 0 AND quantity <= safety_stock`,
			nil, &st.LowStockCount,
		},
		{
			`SELECT COUNT(*) FROM products WHERE status != 'archived' AND quantity <= 0`,
			nil, &st.OutOfStockCount,
		},
	}

	for _, q := range queries {
		if p, ok := q.dest.(*sqlPair); ok {
			if err := db.QueryRowContext(ctx, q.sql, q.args...).Scan(p.a, p.b); err != nil {
				return nil, fmt.Errorf("统计仪表盘数据失败: %w", err)
			}
			continue
		}
		if err := db.QueryRowContext(ctx, q.sql, q.args...).Scan(q.dest); err != nil {
			return nil, fmt.Errorf("统计仪表盘数据失败: %w", err)
		}
	}

	now := time.Now()
	today := startOfDay(now)
	month := startOfMonth(now)

	daily := []struct {
		since time.Time
		typ   string
		dest  *int
	}{
		{today, "in", &st.TodayIn},
		{today, "out", &st.TodayOut},
		{month, "in", &st.MonthIn},
		{month, "out", &st.MonthOut},
	}
	for _, d := range daily {
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(quantity), 0) FROM stock_movements
			WHERE type = ? AND created_at >= ?`, d.typ, d.since.UTC()).Scan(d.dest); err != nil {
			return nil, fmt.Errorf("统计出入库数量失败: %w", err)
		}
	}

	return st, nil
}

// sqlPair 用于一次性扫描两个统计值。
type sqlPair struct {
	a, b any
}

// MovementTrend 返回最近 days 天（含今天）的每日出入库数量。
func (s *Store) MovementTrend(ctx context.Context, days int) ([]models.TrendPoint, error) {
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		days = 90
	}

	today := startOfDay(time.Now())
	start := today.AddDate(0, 0, -(days - 1))

	rows, err := s.db.QueryContext(ctx, `
		SELECT created_at, type, quantity
		FROM stock_movements
		WHERE created_at >= ? AND type IN ('in', 'out')`, start.UTC())
	if err != nil {
		return nil, fmt.Errorf("查询出入库趋势失败: %w", err)
	}
	defer rows.Close()

	type bucket struct{ in, out int }
	buckets := make(map[string]*bucket, days)

	for rows.Next() {
		var (
			createdAt dbTime
			typ       string
			qty       int
		)
		if err := rows.Scan(&createdAt, &typ, &qty); err != nil {
			return nil, fmt.Errorf("解析趋势数据失败: %w", err)
		}
		key := createdAt.Time.In(displayZone).Format("2006-01-02")
		b, ok := buckets[key]
		if !ok {
			b = &bucket{}
			buckets[key] = b
		}
		if typ == string(models.MovementIn) {
			b.in += qty
		} else {
			b.out += qty
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]models.TrendPoint, 0, days)
	for i := 0; i < days; i++ {
		day := start.AddDate(0, 0, i)
		key := day.In(displayZone).Format("2006-01-02")
		point := models.TrendPoint{Label: day.In(displayZone).Format("01-02")}
		if b, ok := buckets[key]; ok {
			point.In, point.Out = b.in, b.out
		}
		out = append(out, point)
	}
	return out, nil
}

// CategoryStats 返回各分类的商品数量与库存价值，用于占比图。
func (s *Store) CategoryStats(ctx context.Context, limit int) ([]models.CategoryStat, error) {
	if limit <= 0 {
		limit = 8
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(c.name, '未分类') AS category_name,
		       COUNT(p.id) AS product_count,
		       COALESCE(SUM(p.quantity * p.cost_price), 0) AS total_value
		FROM products p
		LEFT JOIN categories c ON c.id = p.category_id
		WHERE p.status != 'archived'
		GROUP BY p.category_id
		ORDER BY total_value DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("查询分类统计失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.CategoryStat, 0, limit)
	for rows.Next() {
		var cs models.CategoryStat
		if err := rows.Scan(&cs.Name, &cs.Count, &cs.Value); err != nil {
			return nil, fmt.Errorf("解析分类统计失败: %w", err)
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// TopProductsByValue 返回库存价值最高的商品。
func (s *Store) TopProductsByValue(ctx context.Context, limit int) ([]models.ProductStat, error) {
	if limit <= 0 {
		limit = 8
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, sku, quantity, quantity * cost_price AS total_value
		FROM products
		WHERE status != 'archived'
		ORDER BY total_value DESC, quantity DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("查询库存排行失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.ProductStat, 0, limit)
	for rows.Next() {
		var ps models.ProductStat
		if err := rows.Scan(&ps.Name, &ps.SKU, &ps.Quantity, &ps.Value); err != nil {
			return nil, fmt.Errorf("解析库存排行失败: %w", err)
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// TopMovingProducts 返回最近 days 天内出库量最大的商品。
func (s *Store) TopMovingProducts(ctx context.Context, days, limit int) ([]models.ProductStat, error) {
	if days <= 0 {
		days = 30
	}
	if limit <= 0 {
		limit = 8
	}
	since := startOfDay(time.Now()).AddDate(0, 0, -days)

	rows, err := s.db.QueryContext(ctx, `
		SELECT p.name, p.sku,
		       CAST(COALESCE(SUM(m.quantity), 0) AS INTEGER) AS total_qty,
		       COALESCE(SUM(m.quantity * m.unit_price), 0) AS total_value
		FROM stock_movements m
		JOIN products p ON p.id = m.product_id
		WHERE m.type = 'out' AND m.created_at >= ?
		GROUP BY m.product_id
		ORDER BY total_qty DESC
		LIMIT ?`, since.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("查询出库排行失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.ProductStat, 0, limit)
	for rows.Next() {
		var ps models.ProductStat
		if err := rows.Scan(&ps.Name, &ps.SKU, &ps.Quantity, &ps.Value); err != nil {
			return nil, fmt.Errorf("解析出库排行失败: %w", err)
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// RecentMovements 返回最近的库存操作记录。
func (s *Store) RecentMovements(ctx context.Context, limit int) ([]models.StockMovement, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx,
		movementSelect+` ORDER BY m.created_at DESC, m.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("查询最近操作失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.StockMovement, 0, limit)
	for rows.Next() {
		m, err := scanMovement(rows)
		if err != nil {
			return nil, fmt.Errorf("解析操作记录失败: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// MovementSummary 返回某时间区间内的入库 / 出库汇总，用于报表页。
type MovementSummary struct {
	InQuantity  int
	OutQuantity int
	InAmount    float64
	OutAmount   float64
}

// Summary 统计给定日期区间（本地时区）的出入库汇总。
func (s *Store) Summary(ctx context.Context, startDate, endDate string) (*MovementSummary, error) {
	sum := &MovementSummary{}

	where := []string{"1=1"}
	args := []any{}
	if start, ok := parseLocalDate(startDate); ok {
		where = append(where, "created_at >= ?")
		args = append(args, start.UTC())
	}
	if end, ok := parseLocalDate(endDate); ok {
		where = append(where, "created_at < ?")
		args = append(args, end.AddDate(0, 0, 1).UTC())
	}
	whereSQL := ""
	for i, w := range where {
		if i > 0 {
			whereSQL += " AND "
		}
		whereSQL += w
	}

	err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN type = 'in'  THEN quantity ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type = 'out' THEN quantity ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type = 'in'  THEN quantity * unit_price ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN type = 'out' THEN quantity * unit_price ELSE 0 END), 0)
		FROM stock_movements WHERE `+whereSQL, args...).
		Scan(&sum.InQuantity, &sum.OutQuantity, &sum.InAmount, &sum.OutAmount)
	if err != nil {
		return nil, fmt.Errorf("统计出入库汇总失败: %w", err)
	}
	return sum, nil
}
