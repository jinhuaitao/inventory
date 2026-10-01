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

// dummyPasswordHash 是对应一个已丢弃随机值的固定 bcrypt 摘要。
var dummyPasswordHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("dummy-computation-only"), bcrypt.DefaultCost)
	if err != nil {
		panic("初始化密码校验占位摘要失败: " + err.Error())
	}
	return hash
}()

// BurnPasswordCheck 对固定摘要执行一次 bcrypt 校验，结果一定为 false。
//
// 登录时如果账号不存在就跳过 bcrypt，响应会比「密码错误」快几十毫秒，
// 攻击者可以用计时差异枚举存在的用户名。调用本函数把两条路径的
// 计算量拉平，让计时探测失去信号。
func BurnPasswordCheck(plain string) bool {
	return bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(plain)) == nil
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

// normalize 修剪并校验输入，返回的指针语义由调用方使用。
func (in *CreateUserInput) normalize() error {
	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.FullName = strings.TrimSpace(in.FullName)
	if in.Role == "" {
		in.Role = models.RoleViewer
	}
	if !in.Role.Valid() {
		return fmt.Errorf("%w：角色不合法", ErrInvalidInput)
	}
	return nil
}

// execer 抽象 *sql.DB 与 *sql.Tx 的公共写方法，便于同一份 SQL
// 既能在事务内复用，也能在单语句场景下直接使用连接池。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertUser 写入一条用户记录并返回新 ID。用户名与邮箱均不区分大小写。
func insertUser(ctx context.Context, e execer, in CreateUserInput, passwordHash string) (int64, error) {
	now := nowUTC()
	res, err := e.ExecContext(ctx, `
		INSERT INTO users (username, email, password_hash, full_name, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Username, in.Email, passwordHash, in.FullName, string(in.Role), models.UserStatusActive, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, conflictError(uniqueField(err))
		}
		return 0, fmt.Errorf("创建用户失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取新用户 ID 失败: %w", err)
	}
	return id, nil
}

// CreateUser 创建账号。
func (s *Store) CreateUser(ctx context.Context, in CreateUserInput) (*models.User, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}

	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	id, err := insertUser(ctx, s.db, in, hash)
	if err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}

// CreateUserWithQuestions 在一个事务内创建账号并写入安全问题。
//
// 早先注册流程是「CreateUser 成功后再 SetSecurityQuestions，失败则手工
// DeleteUser 补偿」：补偿本身也可能失败，留下一个没有安全问题、又已被
// 并发登录写入了会话的半成品账号。安全问题与账号必须同生同死。
func (s *Store) CreateUserWithQuestions(ctx context.Context, in CreateUserInput, questions []models.AnswerInput) (*models.User, error) {
	if err := ValidateSecurityAnswers(questions); err != nil {
		return nil, err
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}

	// bcrypt 计算刻意放在事务之外：SQLite 写事务会锁住整个数据库，
	// 不该让几十次哈希运算占着这把锁。
	userHash, err := HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(questions))
	for _, q := range questions {
		h, err := HashPassword(NormalizeAnswer(q.Answer))
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, h)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	id, err := insertUser(ctx, tx, in, userHash)
	if err != nil {
		return nil, err
	}

	now := nowUTC()
	for i, q := range questions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO security_questions (user_id, position, question, answer_hash, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id, i+1, strings.TrimSpace(q.Question), hashes[i], now, now); err != nil {
			return nil, fmt.Errorf("写入安全问题失败: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}

// UpdateProfile 更新当前用户可自行修改的资料。
func (s *Store) UpdateProfile(ctx context.Context, userID int64, fullName, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if !utils.IsValidEmail(email) {
		return fmt.Errorf("%w：邮箱格式不正确", ErrInvalidInput)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET full_name = ?, email = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(fullName), email, nowUTC(), userID)
	if err != nil {
		if isUniqueViolation(err) {
			return conflictError(uniqueField(err))
		}
		return fmt.Errorf("更新资料失败: %w", err)
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		// 用户已不存在（例如刚被管理员删除）。不检查的话这里会返回 nil，
		// 页面提示「资料已更新」，实际上一行都没改 —— 这类假成功最难排查。
		return ErrNotFound
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
//
// 「最后一个管理员检查 → 改资料 → 重置密码 → 踢会话」必须整体原子：
// 拆成多条独立语句时，检查与写入之间可以插进并发提权/降权，
// 中途失败还会留下「资料改了、密码没改」的半态。
// DSN 带 `_txlock=immediate`，写事务之间互斥，check-then-act 不再有害。
func (s *Store) UpdateUserByAdmin(ctx context.Context, userID int64, in AdminUpdateUserInput) error {
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

	// 密码摘要先算好（bcrypt 计算放在事务外，避免长时间占住写锁）
	var passwordHash string
	if strings.TrimSpace(in.Password) != "" {
		hash, err := HashPassword(in.Password)
		if err != nil {
			return err
		}
		passwordHash = hash
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var curRole, curStatus string
	err = tx.QueryRowContext(ctx, `SELECT role, status FROM users WHERE id = ?`, userID).
		Scan(&curRole, &curStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}

	// 防止把系统里最后一个可用管理员降级或禁用
	losingAdmin := curRole == string(models.RoleAdmin) && curStatus == models.UserStatusActive &&
		(in.Role != models.RoleAdmin || in.Status != models.UserStatusActive)
	if losingAdmin {
		var n int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM users WHERE role = ? AND status = ?`,
			string(models.RoleAdmin), models.UserStatusActive).Scan(&n); err != nil {
			return fmt.Errorf("统计管理员数量失败: %w", err)
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}

	now := nowUTC()
	res, err := tx.ExecContext(ctx, `
		UPDATE users SET full_name = ?, email = ?, role = ?, status = ?, updated_at = ?
		WHERE id = ?`,
		strings.TrimSpace(in.FullName), in.Email, string(in.Role), in.Status, now, userID)
	if err != nil {
		if isUniqueViolation(err) {
			return conflictError(uniqueField(err))
		}
		return fmt.Errorf("更新用户失败: %w", err)
	}
	// 目标存在性已在事务内确认，仍保留 0 行判定，让并发删除暴露成 ErrNotFound。
	if n, err := rowsAffected(res); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}

	if passwordHash != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, passwordHash, now, userID); err != nil {
			return fmt.Errorf("重置密码失败: %w", err)
		}
		// 管理员重置密码后，强制该用户重新登录
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("清理会话失败: %w", err)
		}
	}

	return tx.Commit()
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
	// 状态必须落在白名单内。这个函数会把参数原样写进 status 列，
	// 而登录鉴权只认 active —— 放任意字符串进来，一次畸形请求就能把账号
	// 改成「既不是 active 也不是 disabled」的状态，账号从此登不上，
	// 界面上的「启用」按钮也救不回来（它写的是 active，看似生效却对不上语义）。
	if status != models.UserStatusActive && status != models.UserStatusDisabled {
		return fmt.Errorf("%w：状态不合法（可选 %s / %s）",
			ErrInvalidInput, models.UserStatusActive, models.UserStatusDisabled)
	}

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
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, nowUTC(), userID)
	if err != nil {
		return fmt.Errorf("更新用户状态失败: %w", err)
	}
	if n, err := rowsAffected(res); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser 删除账号（同时级联删除其会话与重置令牌）。
//
// 「最后一个管理员 / 是否有流水」两项检查与 DELETE 必须在同一个事务里：
// 拆开执行时，两次检查之间只要有人用该账号做了一次出入库，
// DELETE 就会把带审计记录的账号删掉 —— 与 DeleteProduct 是同一类竞态。
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	var role, status string
	err = tx.QueryRowContext(ctx, `SELECT role, status FROM users WHERE id = ?`, userID).
		Scan(&role, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("查询用户失败: %w", err)
	}
	if role == string(models.RoleAdmin) && status == models.UserStatusActive {
		var n int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM users WHERE role = ? AND status = ?`,
			string(models.RoleAdmin), models.UserStatusActive).Scan(&n); err != nil {
			return fmt.Errorf("统计管理员数量失败: %w", err)
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}

	// 该用户若产生过库存流水，则保留账号以维持审计完整性
	var movements int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stock_movements WHERE operator_id = ?`, userID).Scan(&movements); err != nil {
		return fmt.Errorf("检查用户流水失败: %w", err)
	}
	if movements > 0 {
		return fmt.Errorf("%w：该用户已有 %d 条库存操作记录，建议改为「禁用」而非删除", ErrInvalidInput, movements)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
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
	return tx.Commit()
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
		like := likePattern(f.Search)
		where = append(where, "(username LIKE ? ESCAPE '\\' OR email LIKE ? ESCAPE '\\' OR full_name LIKE ? ESCAPE '\\')")
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
