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

const categoryColumns = `id, name, description, created_at, updated_at`

func scanCategory(sc scanner) (*models.Category, error) {
	var (
		c                    models.Category
		createdAt, updatedAt dbTime
	)
	if err := sc.Scan(&c.ID, &c.Name, &c.Description, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	c.CreatedAt, c.UpdatedAt = createdAt.Time, updatedAt.Time
	return &c, nil
}

// ListAllCategories 返回全部分类（带商品数量），用于下拉框与筛选。
func (s *Store) ListAllCategories(ctx context.Context) ([]models.Category, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.description, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM products p WHERE p.category_id = c.id AND p.status != 'archived') AS product_count
		FROM categories c
		ORDER BY c.name COLLATE NOCASE ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询分类失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Category, 0, 16)
	for rows.Next() {
		var (
			c                    models.Category
			createdAt, updatedAt dbTime
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &createdAt, &updatedAt, &c.ProductCount); err != nil {
			return nil, fmt.Errorf("解析分类记录失败: %w", err)
		}
		c.CreatedAt, c.UpdatedAt = createdAt.Time, updatedAt.Time
		out = append(out, c)
	}
	return out, rows.Err()
}

// CategoryFilter 分类列表筛选条件。
type CategoryFilter struct {
	Search  string
	Page    int
	PerPage int
}

// ListCategories 分页查询分类。
func (s *Store) ListCategories(ctx context.Context, f CategoryFilter) ([]models.Category, *utils.Pagination, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Search != "" {
		like := likePattern(f.Search)
		where = append(where, "(c.name LIKE ? ESCAPE '\\' OR c.description LIKE ? ESCAPE '\\')")
		args = append(args, like, like)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM categories c WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("统计分类数量失败: %w", err)
	}

	pg := utils.NewPagination(f.Page, f.PerPage, total)

	query := `SELECT c.id, c.name, c.description, c.created_at, c.updated_at,
	                 (SELECT COUNT(*) FROM products p WHERE p.category_id = c.id AND p.status != 'archived') AS product_count
	          FROM categories c
	          WHERE ` + whereSQL + `
	          ORDER BY c.name COLLATE NOCASE ASC
	          LIMIT ? OFFSET ?`
	args = append(args, pg.PerPage, pg.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("查询分类列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Category, 0, pg.PerPage)
	for rows.Next() {
		var (
			c                    models.Category
			createdAt, updatedAt dbTime
		)
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &createdAt, &updatedAt, &c.ProductCount); err != nil {
			return nil, nil, fmt.Errorf("解析分类记录失败: %w", err)
		}
		c.CreatedAt, c.UpdatedAt = createdAt.Time, updatedAt.Time
		out = append(out, c)
	}
	return out, pg, rows.Err()
}

// GetCategory 按 ID 查询分类。
func (s *Store) GetCategory(ctx context.Context, id int64) (*models.Category, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+categoryColumns+` FROM categories WHERE id = ?`, id)
	c, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询分类失败: %w", err)
	}
	return c, nil
}

// CreateCategory 新建分类。
func (s *Store) CreateCategory(ctx context.Context, name, description string) (*models.Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w：分类名称不能为空", ErrInvalidInput)
	}
	if len([]rune(name)) > 50 {
		return nil, fmt.Errorf("%w：分类名称不能超过 50 个字符", ErrInvalidInput)
	}

	now := nowUTC()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO categories (name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?)`, name, strings.TrimSpace(description), now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w：分类「%s」已存在", ErrConflict, name)
		}
		return nil, fmt.Errorf("创建分类失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetCategory(ctx, id)
}

// UpdateCategory 修改分类。长度与空值规则必须与 CreateCategory 一致，
// 否则「编辑」就是一条绕过校验的通道（超长名称正是从这里写进去的）。
func (s *Store) UpdateCategory(ctx context.Context, id int64, name, description string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w：分类名称不能为空", ErrInvalidInput)
	}
	if len([]rune(name)) > 50 {
		return fmt.Errorf("%w：分类名称不能超过 50 个字符", ErrInvalidInput)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE categories SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		name, strings.TrimSpace(description), nowUTC(), id)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w：分类「%s」已存在", ErrConflict, name)
		}
		return fmt.Errorf("更新分类失败: %w", err)
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

// DeleteCategory 删除分类；关联商品的分类字段会被置空（外键 ON DELETE SET NULL）。
//
// 统计与删除放在同一事务里，否则返回的「受影响商品数」可能是删除前的旧值，
// 界面据此提示用户的数字就对不上。
func (s *Store) DeleteCategory(ctx context.Context, id int64) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	var affected int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM products WHERE category_id = ?`, id).Scan(&affected); err != nil {
		return 0, fmt.Errorf("统计关联商品失败: %w", err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return 0, fmt.Errorf("删除分类失败: %w", err)
	}
	n, err := rowsAffected(res)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交事务失败: %w", err)
	}
	return affected, nil
}
