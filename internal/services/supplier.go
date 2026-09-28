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

const supplierColumns = `id, name, contact_person, phone, email, address, note, created_at, updated_at`

func scanSupplier(sc scanner) (*models.Supplier, error) {
	var (
		sp                   models.Supplier
		createdAt, updatedAt dbTime
	)
	if err := sc.Scan(&sp.ID, &sp.Name, &sp.ContactPerson, &sp.Phone, &sp.Email,
		&sp.Address, &sp.Note, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	sp.CreatedAt, sp.UpdatedAt = createdAt.Time, updatedAt.Time
	return &sp, nil
}

// ListAllSuppliers 返回全部供应商，用于下拉框。
func (s *Store) ListAllSuppliers(ctx context.Context) ([]models.Supplier, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+supplierColumns+`,
		       (SELECT COUNT(*) FROM products p WHERE p.supplier_id = suppliers.id AND p.status != 'archived') AS product_count
		FROM suppliers
		ORDER BY name COLLATE NOCASE ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询供应商失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Supplier, 0, 16)
	for rows.Next() {
		var (
			sp                   models.Supplier
			createdAt, updatedAt dbTime
		)
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.ContactPerson, &sp.Phone, &sp.Email,
			&sp.Address, &sp.Note, &createdAt, &updatedAt, &sp.ProductCount); err != nil {
			return nil, fmt.Errorf("解析供应商记录失败: %w", err)
		}
		sp.CreatedAt, sp.UpdatedAt = createdAt.Time, updatedAt.Time
		out = append(out, sp)
	}
	return out, rows.Err()
}

// SupplierFilter 供应商列表筛选条件。
type SupplierFilter struct {
	Search  string
	Page    int
	PerPage int
}

// ListSuppliers 分页查询供应商。
func (s *Store) ListSuppliers(ctx context.Context, f SupplierFilter) ([]models.Supplier, *utils.Pagination, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		where = append(where, "(name LIKE ? OR contact_person LIKE ? OR phone LIKE ? OR email LIKE ?)")
		args = append(args, like, like, like, like)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM suppliers WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("统计供应商数量失败: %w", err)
	}

	pg := utils.NewPagination(f.Page, f.PerPage, total)

	query := `SELECT ` + supplierColumns + `,
	                 (SELECT COUNT(*) FROM products p WHERE p.supplier_id = suppliers.id AND p.status != 'archived') AS product_count
	          FROM suppliers
	          WHERE ` + whereSQL + `
	          ORDER BY name COLLATE NOCASE ASC
	          LIMIT ? OFFSET ?`
	args = append(args, pg.PerPage, pg.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("查询供应商列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Supplier, 0, pg.PerPage)
	for rows.Next() {
		var (
			sp                   models.Supplier
			createdAt, updatedAt dbTime
		)
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.ContactPerson, &sp.Phone, &sp.Email,
			&sp.Address, &sp.Note, &createdAt, &updatedAt, &sp.ProductCount); err != nil {
			return nil, nil, fmt.Errorf("解析供应商记录失败: %w", err)
		}
		sp.CreatedAt, sp.UpdatedAt = createdAt.Time, updatedAt.Time
		out = append(out, sp)
	}
	return out, pg, rows.Err()
}

// GetSupplier 按 ID 查询供应商。
func (s *Store) GetSupplier(ctx context.Context, id int64) (*models.Supplier, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+supplierColumns+` FROM suppliers WHERE id = ?`, id)
	sp, err := scanSupplier(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询供应商失败: %w", err)
	}
	return sp, nil
}

// SupplierInput 供应商表单字段。
type SupplierInput struct {
	Name          string
	ContactPerson string
	Phone         string
	Email         string
	Address       string
	Note          string
}

// validate 做基础校验。
func (in *SupplierInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	in.ContactPerson = strings.TrimSpace(in.ContactPerson)
	in.Phone = strings.TrimSpace(in.Phone)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Address = strings.TrimSpace(in.Address)
	in.Note = strings.TrimSpace(in.Note)

	if in.Name == "" {
		return fmt.Errorf("%w：供应商名称不能为空", ErrInvalidInput)
	}
	if len([]rune(in.Name)) > 100 {
		return fmt.Errorf("%w：供应商名称不能超过 100 个字符", ErrInvalidInput)
	}
	if in.Email != "" && !utils.IsValidEmail(in.Email) {
		return fmt.Errorf("%w：邮箱格式不正确", ErrInvalidInput)
	}
	return nil
}

// CreateSupplier 新建供应商。
func (s *Store) CreateSupplier(ctx context.Context, in SupplierInput) (*models.Supplier, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	now := nowUTC()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO suppliers (name, contact_person, phone, email, address, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Name, in.ContactPerson, in.Phone, in.Email, in.Address, in.Note, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w：供应商「%s」已存在", ErrConflict, in.Name)
		}
		return nil, fmt.Errorf("创建供应商失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetSupplier(ctx, id)
}

// UpdateSupplier 修改供应商。
func (s *Store) UpdateSupplier(ctx context.Context, id int64, in SupplierInput) error {
	if err := in.normalize(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE suppliers
		SET name = ?, contact_person = ?, phone = ?, email = ?, address = ?, note = ?, updated_at = ?
		WHERE id = ?`,
		in.Name, in.ContactPerson, in.Phone, in.Email, in.Address, in.Note, nowUTC(), id)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w：供应商「%s」已存在", ErrConflict, in.Name)
		}
		return fmt.Errorf("更新供应商失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSupplier 删除供应商，返回受影响商品数（商品的供应商字段会被置空）。
func (s *Store) DeleteSupplier(ctx context.Context, id int64) (int64, error) {
	var affected int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM products WHERE supplier_id = ?`, id).Scan(&affected); err != nil {
		return 0, fmt.Errorf("统计关联商品失败: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM suppliers WHERE id = ?`, id)
	if err != nil {
		return 0, fmt.Errorf("删除供应商失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	return affected, nil
}
