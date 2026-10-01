package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"inventory/internal/models"
	"inventory/internal/utils"
)

// productSelect 是商品列表 / 详情共用的查询主体。
const productSelect = `
	SELECT p.id, p.sku, p.name, p.barcode, p.category_id, p.supplier_id, p.unit,
	       p.cost_price, p.sale_price, p.quantity, p.safety_stock, p.location,
	       p.description, p.status,
	       COALESCE(c.name, '') AS category_name,
	       COALESCE(s.name, '') AS supplier_name,
	       p.created_at, p.updated_at
	FROM products p
	LEFT JOIN categories c ON c.id = p.category_id
	LEFT JOIN suppliers  s ON s.id = p.supplier_id`

func scanProduct(sc scanner) (*models.Product, error) {
	var (
		p                    models.Product
		categoryID, supplier sql.NullInt64
		createdAt, updatedAt dbTime
	)
	err := sc.Scan(
		&p.ID, &p.SKU, &p.Name, &p.Barcode, &categoryID, &supplier, &p.Unit,
		&p.CostPrice, &p.SalePrice, &p.Quantity, &p.SafetyStock, &p.Location,
		&p.Description, &p.Status, &p.CategoryName, &p.SupplierName,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if categoryID.Valid {
		v := categoryID.Int64
		p.CategoryID = &v
	}
	if supplier.Valid {
		v := supplier.Int64
		p.SupplierID = &v
	}
	p.CreatedAt, p.UpdatedAt = createdAt.Time, updatedAt.Time
	return &p, nil
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// ProductFilter 商品列表筛选与排序条件。
type ProductFilter struct {
	Search       string
	CategoryID   int64
	SupplierID   int64
	Status       string
	LowStockOnly bool
	SortBy       string
	SortDir      string
	Page         int
	PerPage      int
}

// buildWhere 组装 WHERE 子句。
func (f ProductFilter) buildWhere() (string, []any) {
	where := []string{"1=1"}
	args := []any{}

	if f.Search != "" {
		like := likePattern(f.Search)
		where = append(where, "(p.name LIKE ? ESCAPE '\\' OR p.sku LIKE ? ESCAPE '\\' OR p.barcode LIKE ? ESCAPE '\\' OR p.location LIKE ? ESCAPE '\\')")
		args = append(args, like, like, like, like)
	}
	if f.CategoryID > 0 {
		where = append(where, "p.category_id = ?")
		args = append(args, f.CategoryID)
	}
	if f.SupplierID > 0 {
		where = append(where, "p.supplier_id = ?")
		args = append(args, f.SupplierID)
	}
	// 状态筛选分三种情形，不能把后两者合并：
	//   ""    —— 默认视图，隐含排除归档（归档意味着「不再参与日常管理」）；
	//   "all" —— 字面意义的「全部状态」，归档商品也必须显示出来；
	//   其他  —— 精确匹配某个状态。
	// 早先 "" 与 "all" 共用同一个 else 分支，于是下拉里选了「全部状态」
	// 反而筛不出归档商品 —— 一个叫「全部」却少给结果的筛选项，
	// 比根本没有这个选项更容易误导人。
	switch f.Status {
	case "":
		where = append(where, "p.status != 'archived'")
	case "all":
		// 不加任何状态条件
	default:
		where = append(where, "p.status = ?")
		args = append(args, f.Status)
	}
	if f.LowStockOnly {
		where = append(where, "p.quantity <= p.safety_stock")
	}
	return strings.Join(where, " AND "), args
}

// productSortColumns 是允许排序的列白名单，避免 SQL 注入。
var productSortColumns = map[string]string{
	"name":       "p.name",
	"sku":        "p.sku",
	"quantity":   "p.quantity",
	"cost_price": "p.cost_price",
	"sale_price": "p.sale_price",
	"value":      "(p.quantity * p.cost_price)",
	"category":   "category_name",
	"updated_at": "p.updated_at",
	"created_at": "p.created_at",
}

func buildProductSort(by, dir string) string {
	col, ok := productSortColumns[by]
	if !ok {
		col = "p.updated_at"
	}
	order := "DESC"
	if strings.EqualFold(dir, "asc") {
		order = "ASC"
	}
	return col + " " + order + ", p.id DESC"
}

// ListProducts 分页查询商品。
func (s *Store) ListProducts(ctx context.Context, f ProductFilter) ([]models.Product, *utils.Pagination, error) {
	whereSQL, args := f.buildWhere()

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM products p WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("统计商品数量失败: %w", err)
	}

	pg := utils.NewPagination(f.Page, f.PerPage, total)

	query := productSelect + ` WHERE ` + whereSQL +
		` ORDER BY ` + buildProductSort(f.SortBy, f.SortDir) + ` LIMIT ? OFFSET ?`
	args = append(args, pg.PerPage, pg.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("查询商品列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Product, 0, pg.PerPage)
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("解析商品记录失败: %w", err)
		}
		out = append(out, *p)
	}
	return out, pg, rows.Err()
}

// ListProductsForExport 返回符合条件的商品（不分页），用于 CSV 导出。
//
// truncated 为真表示结果被 MaxExportRows 截断，调用方**必须**把这个事实
// 明确告诉用户（写进导出文件本身），不能像以前那样静默只出前 200 条。
func (s *Store) ListProductsForExport(ctx context.Context, f ProductFilter) ([]models.Product, bool, error) {
	return s.listProductsForExport(ctx, f, MaxExportRows)
}

// listProductsForExport 是导出的实际实现，上限作为参数传入以便测试。
func (s *Store) listProductsForExport(ctx context.Context, f ProductFilter, limit int) ([]models.Product, bool, error) {
	whereSQL, args := f.buildWhere()
	query := productSelect + ` WHERE ` + whereSQL + ` ORDER BY ` + buildProductSort(f.SortBy, f.SortDir) + ` LIMIT ?`
	// 多取一行用于判断是否真的被截断：只看 len(out) == limit
	// 无法区分「恰好这么多」与「后面还有更多」。
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("查询商品失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Product, 0, 256)
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, false, fmt.Errorf("解析商品记录失败: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// GetProduct 按 ID 查询商品。
func (s *Store) GetProduct(ctx context.Context, id int64) (*models.Product, error) {
	row := s.db.QueryRowContext(ctx, productSelect+` WHERE p.id = ?`, id)
	p, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询商品失败: %w", err)
	}
	return p, nil
}

// GetProductBySKU 按 SKU 查询商品。
func (s *Store) GetProductBySKU(ctx context.Context, sku string) (*models.Product, error) {
	row := s.db.QueryRowContext(ctx, productSelect+` WHERE p.sku = ?`, utils.NormalizeSKU(sku))
	p, err := scanProduct(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询商品失败: %w", err)
	}
	return p, nil
}

// CountLowStockBreakdown 统计预警商品中「已缺货」与「低于安全库存但仍有货」各有多少。
//
// 口径与预警列表（ProductFilter.LowStockOnly）严格一致，
// 因此 outOfStock + lowStock 恒等于该列表的总条数。
//
// 必须用独立的聚合查询，不能遍历当前页的商品来数：分页之后那样数出来的
// 只是「本页」的构成，商品一超过一页，页面顶部两个数字就会莫名其妙变小，
// 而用户完全看不出原因。
func (s *Store) CountLowStockBreakdown(ctx context.Context) (outOfStock, lowStock int, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN quantity <= 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN quantity > 0 THEN 1 ELSE 0 END), 0)
		  FROM products
		 WHERE status != 'archived' AND quantity <= safety_stock`).
		Scan(&outOfStock, &lowStock)
	if err != nil {
		return 0, 0, fmt.Errorf("统计库存预警构成失败: %w", err)
	}
	return outOfStock, lowStock, nil
}

// ListLowStockProducts 返回库存低于或等于安全库存的商品，按缺口从大到小排序。
func (s *Store) ListLowStockProducts(ctx context.Context, limit int) ([]models.Product, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, productSelect+
		` WHERE p.status != 'archived' AND p.quantity <= p.safety_stock
		  ORDER BY (p.safety_stock - p.quantity) DESC, p.quantity ASC
		  LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("查询低库存商品失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Product, 0, limit)
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("解析商品记录失败: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// 写入
// ---------------------------------------------------------------------------

// ProductInput 商品表单字段。
type ProductInput struct {
	SKU         string
	Name        string
	Barcode     string
	CategoryID  *int64
	SupplierID  *int64
	Unit        string
	CostPrice   float64
	SalePrice   float64
	Quantity    int // 仅创建时作为期初库存生效
	SafetyStock int
	Location    string
	Description string
	Status      string
}

func (in *ProductInput) normalize() error {
	in.SKU = utils.NormalizeSKU(in.SKU)
	in.Name = strings.TrimSpace(in.Name)
	in.Barcode = strings.TrimSpace(in.Barcode)
	in.Unit = utils.DefaultString(strings.TrimSpace(in.Unit), "件")
	in.Location = strings.TrimSpace(in.Location)
	in.Description = strings.TrimSpace(in.Description)
	if in.Status == "" {
		in.Status = models.ProductStatusActive
	}

	if in.SKU == "" {
		return fmt.Errorf("%w：SKU 不能为空", ErrInvalidInput)
	}
	if len(in.SKU) > 64 {
		return fmt.Errorf("%w：SKU 不能超过 64 个字符", ErrInvalidInput)
	}
	if in.Name == "" {
		return fmt.Errorf("%w：商品名称不能为空", ErrInvalidInput)
	}
	if len([]rune(in.Name)) > 200 {
		return fmt.Errorf("%w：商品名称不能超过 200 个字符", ErrInvalidInput)
	}
	if in.CostPrice < 0 || in.SalePrice < 0 {
		return fmt.Errorf("%w：价格不能为负数", ErrInvalidInput)
	}
	if in.SafetyStock < 0 {
		return fmt.Errorf("%w：安全库存不能为负数", ErrInvalidInput)
	}
	if in.Quantity < 0 {
		return fmt.Errorf("%w：库存数量不能为负数", ErrInvalidInput)
	}
	if in.Quantity > MaxQuantity {
		return fmt.Errorf("%w：期初库存不能超过 %d", ErrInvalidInput, MaxQuantity)
	}
	if in.Status != models.ProductStatusActive && in.Status != models.ProductStatusArchived {
		return fmt.Errorf("%w：商品状态不合法", ErrInvalidInput)
	}
	return nil
}

// CreateProduct 新建商品；若期初库存大于 0，会同步生成一条「期初建账」流水。
func (s *Store) CreateProduct(ctx context.Context, in ProductInput, operatorID int64) (*models.Product, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	// 操作人必须有效。期初库存大于 0 时 operatorID 会写进流水的外键列，
	// 而该列是 NOT NULL REFERENCES users(id)：不在这里拦下的话，
	// 用户看到的会是一句裸的「constraint failed」，
	// 整笔建商品操作回滚，却不知道究竟哪里填错了。
	if operatorID <= 0 {
		return nil, fmt.Errorf("%w：缺少操作人信息", ErrInvalidInput)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := nowUTC()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO products
			(sku, name, barcode, category_id, supplier_id, unit, cost_price, sale_price,
			 quantity, safety_stock, location, description, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.SKU, in.Name, in.Barcode, nullableID(in.CategoryID), nullableID(in.SupplierID),
		in.Unit, in.CostPrice, in.SalePrice, in.Quantity, in.SafetyStock,
		in.Location, in.Description, in.Status, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w：SKU「%s」已存在", ErrConflict, in.SKU)
		}
		return nil, fmt.Errorf("创建商品失败: %w", err)
	}

	productID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	if in.Quantity > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO stock_movements
				(product_id, type, quantity, delta, unit_price, before_qty, after_qty,
				 ref_no, supplier_id, operator_id, note, created_at)
			VALUES (?, 'init', ?, ?, ?, 0, ?, '', ?, ?, '新建商品期初建账', ?)`,
			productID, in.Quantity, in.Quantity, in.CostPrice, in.Quantity,
			nullableID(in.SupplierID), operatorID, now); err != nil {
			return nil, fmt.Errorf("写入期初库存流水失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetProduct(ctx, productID)
}

// UpdateProduct 修改商品资料。库存数量不在此处修改，必须通过出入库 / 盘点完成。
func (s *Store) UpdateProduct(ctx context.Context, id int64, in ProductInput) error {
	if err := in.normalize(); err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE products SET
			sku = ?, name = ?, barcode = ?, category_id = ?, supplier_id = ?, unit = ?,
			cost_price = ?, sale_price = ?, safety_stock = ?, location = ?,
			description = ?, status = ?, updated_at = ?
		WHERE id = ?`,
		in.SKU, in.Name, in.Barcode, nullableID(in.CategoryID), nullableID(in.SupplierID), in.Unit,
		in.CostPrice, in.SalePrice, in.SafetyStock, in.Location,
		in.Description, in.Status, nowUTC(), id)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w：SKU「%s」已存在", ErrConflict, in.SKU)
		}
		return fmt.Errorf("更新商品失败: %w", err)
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetProductStatus 归档 / 恢复商品。
func (s *Store) SetProductStatus(ctx context.Context, id int64, status string) error {
	if status != models.ProductStatusActive && status != models.ProductStatusArchived {
		return fmt.Errorf("%w：商品状态不合法", ErrInvalidInput)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE products SET status = ?, updated_at = ? WHERE id = ?`, status, nowUTC(), id)
	if err != nil {
		return fmt.Errorf("更新商品状态失败: %w", err)
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProduct 删除商品。存在库存流水时拒绝删除，以保留审计记录。
//
// 「查流水」与「删商品」必须在同一个事务里完成：拆成两条独立语句的话，
// 两次查询之间只要有人对该商品做了一次出入库，DELETE 就会把它刚写入的
// 流水一起带走（stock_movements.product_id 早期甚至是 ON DELETE CASCADE）。
// 事务加上 DSN 里的 `_txlock=immediate`，保证这中间不会被插进别的写操作。
//
// 数据库侧的外键已收紧为 RESTRICT（见 schema.sql 与 enforceMovementForeignKeys），
// 即便将来有人绕过这一层，也会被数据库直接拒绝。
func (s *Store) DeleteProduct(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	var movements int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stock_movements WHERE product_id = ?`, id).Scan(&movements); err != nil {
		return fmt.Errorf("检查商品流水失败: %w", err)
	}
	if movements > 0 {
		return fmt.Errorf("%w：该商品已有 %d 条库存流水，请改为「归档」以保留历史记录", ErrInvalidInput, movements)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM products WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除商品失败: %w", err)
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// nullableID 把 0 或 nil 的 ID 转换为 SQL NULL，避免外键约束报错。
func nullableID(id *int64) any {
	if id == nil || *id <= 0 {
		return nil
	}
	return *id
}
