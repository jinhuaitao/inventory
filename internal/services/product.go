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
		like := "%" + f.Search + "%"
		where = append(where, "(p.name LIKE ? OR p.sku LIKE ? OR p.barcode LIKE ? OR p.location LIKE ?)")
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
	if f.Status != "" && f.Status != "all" {
		where = append(where, "p.status = ?")
		args = append(args, f.Status)
	} else {
		where = append(where, "p.status != 'archived'")
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

// ListProductsForExport 返回符合条件的全部商品（不分页），用于 CSV 导出。
func (s *Store) ListProductsForExport(ctx context.Context, f ProductFilter) ([]models.Product, error) {
	whereSQL, args := f.buildWhere()
	query := productSelect + ` WHERE ` + whereSQL + ` ORDER BY ` + buildProductSort(f.SortBy, f.SortDir)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询商品失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Product, 0, 256)
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, fmt.Errorf("解析商品记录失败: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
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
func (s *Store) DeleteProduct(ctx context.Context, id int64) error {
	var movements int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stock_movements WHERE product_id = ?`, id).Scan(&movements); err != nil {
		return fmt.Errorf("检查商品流水失败: %w", err)
	}
	if movements > 0 {
		return fmt.Errorf("%w：该商品已有 %d 条库存流水，请改为「归档」以保留历史记录", ErrInvalidInput, movements)
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM products WHERE id = ?`, id)
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
	return nil
}

// nullableID 把 0 或 nil 的 ID 转换为 SQL NULL，避免外键约束报错。
func nullableID(id *int64) any {
	if id == nil || *id <= 0 {
		return nil
	}
	return *id
}
