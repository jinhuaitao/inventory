package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"inventory/internal/models"
	"inventory/internal/utils"
)

const userColumns = `id, username, email, password_hash, full_name, role, status,
	last_login_at, created_at, updated_at`

// ---------------------------------------------------------------------------
// 密码工具
// ---------------------------------------------------------------------------

// HashPassword 使用 bcrypt 生成密码摘要。
func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("加密密码失败: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword 校验明文密码与摘要是否匹配。
func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ---------------------------------------------------------------------------
// 扫描
// ---------------------------------------------------------------------------

func scanUser(sc scanner) (*models.User, error) {
	var (
		u         models.User
		lastLogin nullTime
		createdAt dbTime
		updatedAt dbTime
	)
	err := sc.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.FullName,
		&u.Role, &u.Status, &lastLogin, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.LastLoginAt = lastLogin.Ptr()
	u.CreatedAt = createdAt.Time
	u.UpdatedAt = updatedAt.Time
	return &u, nil
}

// conflictError 把数据库唯一约束冲突翻译成对用户友好的提示。
func conflictError(field string) error {
	switch {
	case strings.Contains(field, "username"):
		return fmt.Errorf("%w：该用户名已被注册", ErrConflict)
	case strings.Contains(field, "email"):
		return fmt.Errorf("%w：该邮箱已被注册", ErrConflict)
	case strings.Contains(field, "sku"):
		return fmt.Errorf("%w：该 SKU 已存在", ErrConflict)
	case strings.Contains(field, "name"):
		return fmt.Errorf("%w：该名称已存在", ErrConflict)
	default:
		return ErrConflict
	}
}

// ---------------------------------------------------------------------------
// 查询
// ---------------------------------------------------------------------------

// GetUserByID 按主键查询用户。
func (s *Store) GetUserByID(ctx context.Context, id int64) (*models.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return u, nil
}

// GetUserByIdentifier 支持用用户名或邮箱登录。
func (s *Store) GetUserByIdentifier(ctx context.Context, identifier string) (*models.User, error) {
	identifier = strings.TrimSpace(identifier)
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = ? OR email = ? LIMIT 1`,
		identifier, strings.ToLower(identifier))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return u, nil
}

// GetUserByEmail 按邮箱精确查询。
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? LIMIT 1`, strings.ToLower(strings.TrimSpace(email)))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return u, nil
}

// CountUsers 统计用户总数。
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CountActiveAdmins 统计处于启用状态的管理员数量。
func (s *Store) CountActiveAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users WHERE role = ? AND status = ?`,
		string(models.RoleAdmin), models.UserStatusActive).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// 写入
// ---------------------------------------------------------------------------

// CreateUserInput 创建用户所需的字段。
type CreateUserInput struct {
	Username string
	Email    string
	Password string
	FullName string
	Role     models.Role
}

// CreateUser 创建账号，用户名与邮箱均不区分大小写。
func (s *Store) CreateUser(ctx context.Context, in CreateUserInput) (*models.User, error) {
	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.FullName = strings.TrimSpace(in.FullName)
	if in.Role == "" {
		in.Role = models.RoleViewer
	}
	if !in.Role.Valid() {
		return nil, fmt.Errorf("%w：角色不合法", ErrInvalidInput)
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, email, password_hash, full_name, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Username, in.Email, hash, in.FullName, string(in.Role), models.UserStatusActive, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, conflictError(uniqueField(err))
		}
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("获取新用户 ID 失败: %w", err)
	}
	return s.GetUserByID(ctx, id)
}

// UpdateProfile 更新当前用户可自行修改的资料。
func (s *Store) UpdateProfile(ctx context.Context, userID int64, fullName, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !utils.IsValidEmail(email) {
		return fmt.Errorf("%w：邮箱格式不正确", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET full_name = ?, email = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(fullName), email, nowUTC(), userID)
	if err != nil {
		if isUniqueViolation(err) {
			return conflictError(uniqueField(err))
		}
		return fmt.Errorf("更新资料失败: %w", err)
	}
	return nil
}

// AdminUpdateUserInput 管理员修改用户时可调整的字段。
type AdminUpdateUserInput struct {
	FullName string
	Email    string
	Role     models.Role
	Status   string
	Password string // 非空时重置密码
}

// UpdateUserByAdmin 由管理员修改用户资料、角色与状态。
func (s *Store) UpdateUserByAdmin(ctx context.Context, userID int64, in AdminUpdateUserInput) error {
	target, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	if !in.Role.Valid() {
		return fmt.Errorf("%w：角色不合法", ErrInvalidInput)
	}
	if in.Status != models.UserStatusActive && in.Status != models.UserStatusDisabled {
		return fmt.Errorf("%w：状态不合法", ErrInvalidInput)
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !utils.IsValidEmail(in.Email) {
		return fmt.Errorf("%w：邮箱格式不正确", ErrInvalidInput)
	}

	// 防止把系统里最后一个可用管理员降级或禁用
	losingAdmin := target.Role == models.RoleAdmin && target.Status == models.UserStatusActive &&
		(in.Role != models.RoleAdmin || in.Status != models.UserStatusActive)
	if losingAdmin {
		n, err := s.CountActiveAdmins(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}

	now := nowUTC()
	_, err = s.db.ExecContext(ctx, `
		UPDATE users SET full_name = ?, email = ?, role = ?, status = ?, updated_at = ?
		WHERE id = ?`,
		strings.TrimSpace(in.FullName), in.Email, string(in.Role), in.Status, now, userID)
	if err != nil {
		if isUniqueViolation(err) {
			return conflictError(uniqueField(err))
		}
		return fmt.Errorf("更新用户失败: %w", err)
	}

	if strings.TrimSpace(in.Password) != "" {
		if err := s.SetPassword(ctx, userID, in.Password); err != nil {
			return err
		}
		// 管理员重置密码后，强制该用户重新登录
		if _, err := s.DeleteSessionsByUser(ctx, userID, ""); err != nil {
			return err
		}
	}
	return nil
}

// SetPassword 直接写入新的密码摘要（调用方负责校验强度）。
func (s *Store) SetPassword(ctx context.Context, userID int64, newPassword string) error {
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, nowUTC(), userID)
	if err != nil {
		return fmt.Errorf("更新密码失败: %w", err)
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

// ChangePassword 校验旧密码后更新为新密码。
func (s *Store) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string) error {
	u, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !VerifyPassword(u.PasswordHash, currentPassword) {
		return fmt.Errorf("%w：当前密码不正确", ErrInvalidCredentials)
	}
	return s.SetPassword(ctx, userID, newPassword)
}

// TouchLastLogin 记录登录时间。
func (s *Store) TouchLastLogin(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET last_login_at = ? WHERE id = ?`, nowUTC(), userID)
	return err
}

// SetUserStatus 启用 / 禁用账号。
func (s *Store) SetUserStatus(ctx context.Context, userID int64, status string) error {
	u, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if status == models.UserStatusDisabled && u.Role == models.RoleAdmin && u.Status == models.UserStatusActive {
		n, err := s.CountActiveAdmins(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, nowUTC(), userID)
	if err != nil {
		return fmt.Errorf("更新用户状态失败: %w", err)
	}
	return nil
}

// DeleteUser 删除账号（同时级联删除其会话与重置令牌）。
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	u, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Role == models.RoleAdmin && u.Status == models.UserStatusActive {
		n, err := s.CountActiveAdmins(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}

	// 该用户若产生过库存流水，则保留账号以维持审计完整性
	var movements int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stock_movements WHERE operator_id = ?`, userID).Scan(&movements); err != nil {
		return fmt.Errorf("检查用户流水失败: %w", err)
	}
	if movements > 0 {
		return fmt.Errorf("%w：该用户已有 %d 条库存操作记录，建议改为「禁用」而非删除", ErrInvalidInput, movements)
	}

	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return fmt.Errorf("删除用户失败: %w", err)
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

// ---------------------------------------------------------------------------
// 列表
// ---------------------------------------------------------------------------

// UserFilter 用户列表筛选条件。
type UserFilter struct {
	Search  string
	Role    string
	Status  string
	Page    int
	PerPage int
}

// ListUsers 分页查询用户。
func (s *Store) ListUsers(ctx context.Context, f UserFilter) ([]models.User, *utils.Pagination, error) {
	where := []string{"1=1"}
	args := []any{}

	if f.Search != "" {
		like := "%" + f.Search + "%"
		where = append(where, "(username LIKE ? OR email LIKE ? OR full_name LIKE ?)")
		args = append(args, like, like, like)
	}
	if f.Role != "" {
		where = append(where, "role = ?")
		args = append(args, f.Role)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, nil, fmt.Errorf("统计用户数量失败: %w", err)
	}

	pg := utils.NewPagination(f.Page, f.PerPage, total)

	query := `SELECT ` + userColumns + ` FROM users WHERE ` + whereSQL +
		` ORDER BY CASE role WHEN 'admin' THEN 0 WHEN 'manager' THEN 1 ELSE 2 END, id ASC LIMIT ? OFFSET ?`
	args = append(args, pg.PerPage, pg.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("查询用户列表失败: %w", err)
	}
	defer rows.Close()

	users := make([]models.User, 0, pg.PerPage)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("解析用户记录失败: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return users, pg, nil
}
