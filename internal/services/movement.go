package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"inventory/internal/models"
	"inventory/internal/utils"
)

const movementSelect = `
	SELECT m.id, m.product_id, m.type, m.quantity, m.delta, m.unit_price,
	       m.before_qty, m.after_qty, m.ref_no, m.supplier_id, m.operator_id, m.note, m.created_at,
	       COALESCE(p.name, '') AS product_name,
	       COALESCE(p.sku, '')  AS product_sku,
	       COALESCE(p.unit, '') AS product_unit,
	       CASE WHEN u.full_name IS NULL OR u.full_name = ''
	            THEN COALESCE(u.username, '已删除用户')
	            ELSE u.full_name END AS operator_name,
	       COALESCE(s.name, '') AS supplier_name
	FROM stock_movements m
	LEFT JOIN products  p ON p.id = m.product_id
	LEFT JOIN users     u ON u.id = m.operator_id
	LEFT JOIN suppliers s ON s.id = m.supplier_id`

func scanMovement(sc scanner) (*models.StockMovement, error) {
	var (
		m          models.StockMovement
		supplierID sql.NullInt64
		createdAt  dbTime
	)
	err := sc.Scan(
		&m.ID, &m.ProductID, &m.Type, &m.Quantity, &m.Delta, &m.UnitPrice,
		&m.BeforeQty, &m.AfterQty, &m.RefNo, &supplierID, &m.OperatorID, &m.Note, &createdAt,
		&m.ProductName, &m.ProductSKU, &m.ProductUnit, &m.OperatorName, &m.SupplierName,
	)
	if err != nil {
		return nil, err
	}
	if supplierID.Valid {
		v := supplierID.Int64
		m.SupplierID = &v
	}
	m.CreatedAt = createdAt.Time
	return &m, nil
}

// ---------------------------------------------------------------------------
// 核心：库存变动（事务）
// ---------------------------------------------------------------------------

// MovementInput 一次出入库操作所需的参数。
type MovementInput struct {
	ProductID  int64
	Quantity   int // 正数，表示变动数量
	UnitPrice  float64
	RefNo      string
	SupplierID *int64
	OperatorID int64
	Note       string
}

// ApplyMovement 在一个事务中完成「校验 → 更新库存 → 写入流水」。
//
// 该方法是全部库存变动的唯一入口，保证商品库存与流水永远一致。
func (s *Store) ApplyMovement(ctx context.Context, typ models.MovementType, in MovementInput) (*models.StockMovement, error) {
	if !typ.Valid() {
		return nil, fmt.Errorf("%w：库存变动类型不合法", ErrInvalidInput)
	}
	if typ == models.MovementInit || typ == models.MovementAdjust {
		return nil, fmt.Errorf("%w：期初建账与盘点请使用专用接口", ErrInvalidInput)
	}
	if in.ProductID <= 0 {
		return nil, fmt.Errorf("%w：请选择商品", ErrInvalidInput)
	}
	if in.Quantity <= 0 {
		return nil, fmt.Errorf("%w：数量必须大于 0", ErrInvalidInput)
	}
	if in.Quantity > 100_000_000 {
		return nil, fmt.Errorf("%w：单次数量过大", ErrInvalidInput)
	}
	if in.UnitPrice < 0 {
		return nil, fmt.Errorf("%w：单价不能为负数", ErrInvalidInput)
	}
	if in.OperatorID <= 0 {
		return nil, fmt.Errorf("%w：缺少操作人信息", ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	// 锁定并读取当前库存
	var (
		before   int
		name     string
		prodUnit string
	)
	err = tx.QueryRowContext(ctx,
		`SELECT quantity, name, unit FROM products WHERE id = ?`, in.ProductID).
		Scan(&before, &name, &prodUnit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取商品库存失败: %w", err)
	}

	delta := in.Quantity * typ.Direction()
	after := before + delta
	if after < 0 {
		return nil, fmt.Errorf("%w：当前库存为 %d %s，无法出库 %d %s",
			ErrInsufficientStock, before, prodUnit, in.Quantity, prodUnit)
	}

	now := nowUTC()
	if _, err := tx.ExecContext(ctx,
		`UPDATE products SET quantity = ?, updated_at = ? WHERE id = ?`, after, now, in.ProductID); err != nil {
		return nil, fmt.Errorf("更新库存失败: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO stock_movements
			(product_id, type, quantity, delta, unit_price, before_qty, after_qty,
			 ref_no, supplier_id, operator_id, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.ProductID, string(typ), in.Quantity, delta, in.UnitPrice,
		before, after, strings.TrimSpace(in.RefNo), nullableID(in.SupplierID),
		in.OperatorID, strings.TrimSpace(in.Note), now)
	if err != nil {
		return nil, fmt.Errorf("写入库存流水失败: %w", err)
	}

	movementID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	return &models.StockMovement{
		ID: movementID, ProductID: in.ProductID, Type: typ,
		Quantity: in.Quantity, Delta: delta, UnitPrice: in.UnitPrice,
		BeforeQty: before, AfterQty: after, RefNo: strings.TrimSpace(in.RefNo),
		SupplierID: in.SupplierID, OperatorID: in.OperatorID,
		Note: strings.TrimSpace(in.Note), CreatedAt: now,
		ProductName: name, ProductUnit: prodUnit,
	}, nil
}

// AdjustStock 盘点：把商品库存直接设置为实际数量，并记录差异。
func (s *Store) AdjustStock(ctx context.Context, productID int64, targetQty int, note string, operatorID int64) (*models.StockMovement, error) {
	if productID <= 0 {
		return nil, fmt.Errorf("%w：请选择商品", ErrInvalidInput)
	}
	if targetQty < 0 {
		return nil, fmt.Errorf("%w：实际库存不能为负数", ErrInvalidInput)
	}
	if operatorID <= 0 {
		return nil, fmt.Errorf("%w：缺少操作人信息", ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	var (
		before   int
		name     string
		prodUnit string
		cost     float64
	)
	err = tx.QueryRowContext(ctx,
		`SELECT quantity, name, unit, cost_price FROM products WHERE id = ?`, productID).
		Scan(&before, &name, &prodUnit, &cost)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取商品库存失败: %w", err)
	}

	delta := targetQty - before
	now := nowUTC()

	if _, err := tx.ExecContext(ctx,
		`UPDATE products SET quantity = ?, updated_at = ? WHERE id = ?`, targetQty, now, productID); err != nil {
		return nil, fmt.Errorf("更新库存失败: %w", err)
	}

	if strings.TrimSpace(note) == "" {
		note = "库存盘点调整"
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO stock_movements
			(product_id, type, quantity, delta, unit_price, before_qty, after_qty,
			 ref_no, supplier_id, operator_id, note, created_at)
		VALUES (?, 'adjust', ?, ?, ?, ?, ?, '', NULL, ?, ?, ?)`,
		productID, absInt(delta), delta, cost, before, targetQty, operatorID, strings.TrimSpace(note), now)
	if err != nil {
		return nil, fmt.Errorf("写入盘点流水失败: %w", err)
	}

	movementID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	return &models.StockMovement{
		ID: movementID, ProductID: productID, Type: models.MovementAdjust,
		Quantity: absInt(delta), Delta: delta, UnitPrice: cost,
		BeforeQty: before, AfterQty: targetQty, OperatorID: operatorID,
		Note: strings.TrimSpace(note), CreatedAt: now,
		ProductName: name, ProductUnit: prodUnit,
	}, nil
}

// ---------------------------------------------------------------------------
// 流水查询
// ---------------------------------------------------------------------------

// MovementFilter 流水列表筛选条件。
type MovementFilter struct {
	Keyword    string // 商品名 / SKU / 单号
	Type       string
	ProductID  int64
	OperatorID int64
	StartDate  string // YYYY-MM-DD（本地时区）
	EndDate    string // YYYY-MM-DD（本地时区，含当天）
	Page       int
	PerPage    int
}

func (f MovementFilter) buildWhere() (string, []any) {
	where := []string{"1=1"}
	args := []any{}

	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		where = append(where, "(p.name LIKE ? OR p.sku LIKE ? OR m.ref_no LIKE ? OR m.note LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if f.Type != "" && f.Type != "all" {
		where = append(where, "m.type = ?")
		args = append(args, f.Type)
	}
	if f.ProductID > 0 {
		where = append(where, "m.product_id = ?")
		args = append(args, f.ProductID)
	}
	if f.OperatorID > 0 {
		where = append(where, "m.operator_id = ?")
		args = append(args, f.OperatorID)
	}
	if start, ok := parseLocalDate(f.StartDate); ok {
		where = append(where, "m.created_at >= ?")
		args = append(args, start.UTC())
	}
	if end, ok := parseLocalDate(f.EndDate); ok {
		where = append(where, "m.created_at < ?")
		args = append(args, end.AddDate(0, 0, 1).UTC())
	}
	return strings.Join(where, " AND "), args
}

// ListMovements 分页查询库存流水。
func (s *Store) ListMovements(ctx context.Context, f MovementFilter) ([]models.StockMovement, *utils.Pagination, error) {
	whereSQL, args := f.buildWhere()

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stock_movements m LEFT JOIN products p ON p.id = m.product_id WHERE `+whereSQL,
		args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("统计流水数量失败: %w", err)
	}

	pg := utils.NewPagination(f.Page, f.PerPage, total)

	query := movementSelect + ` WHERE ` + whereSQL + ` ORDER BY m.created_at DESC, m.id DESC LIMIT ? OFFSET ?`
	args = append(args, pg.PerPage, pg.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("查询流水列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.StockMovement, 0, pg.PerPage)
	for rows.Next() {
		m, err := scanMovement(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("解析流水记录失败: %w", err)
		}
		out = append(out, *m)
	}
	return out, pg, rows.Err()
}

// ListMovementsForExport 返回符合条件的全部流水（不分页）。
func (s *Store) ListMovementsForExport(ctx context.Context, f MovementFilter) ([]models.StockMovement, error) {
	whereSQL, args := f.buildWhere()
	query := movementSelect + ` WHERE ` + whereSQL + ` ORDER BY m.created_at DESC, m.id DESC LIMIT 50000`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询流水失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.StockMovement, 0, 512)
	for rows.Next() {
		m, err := scanMovement(rows)
		if err != nil {
			return nil, fmt.Errorf("解析流水记录失败: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// ListProductMovements 查询单个商品的最近流水。
func (s *Store) ListProductMovements(ctx context.Context, productID int64, limit int) ([]models.StockMovement, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		movementSelect+` WHERE m.product_id = ? ORDER BY m.created_at DESC, m.id DESC LIMIT ?`,
		productID, limit)
	if err != nil {
		return nil, fmt.Errorf("查询商品流水失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.StockMovement, 0, limit)
	for rows.Next() {
		m, err := scanMovement(rows)
		if err != nil {
			return nil, fmt.Errorf("解析流水记录失败: %w", err)
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// parseLocalDate 解析 YYYY-MM-DD 为本地时区（UTC+8）的零点。
func parseLocalDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006-01-02", s, displayZone)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// absInt 返回绝对值。
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
