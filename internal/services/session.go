package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"inventory/internal/models"
)

// ---------------------------------------------------------------------------
// 会话
// ---------------------------------------------------------------------------

// CreateSession 写入一条会话记录。
func (s *Store) CreateSession(ctx context.Context, sess *models.Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (user_id, token_hash, csrf_token, expires_at, created_at, user_agent, ip)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.UserID, sess.TokenHash, sess.CSRFToken,
		sess.ExpiresAt.UTC(), nowUTC(), sess.UserAgent, sess.IP)
	if err != nil {
		return fmt.Errorf("创建会话失败: %w", err)
	}
	return nil
}

// GetSessionUser 通过令牌摘要一次性加载会话与关联用户。
func (s *Store) GetSessionUser(ctx context.Context, tokenHash string) (*models.Session, *models.User, error) {
	var (
		sess                     models.Session
		sessExpires, sessCreated dbTime
		u                        models.User
		lastLogin                nullTime
		userCreated, userUpdated dbTime
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.token_hash, s.csrf_token, s.expires_at, s.created_at, s.user_agent, s.ip,
		       u.id, u.username, u.email, u.password_hash, u.full_name, u.role, u.status,
		       u.last_login_at, u.created_at, u.updated_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ?`, tokenHash).Scan(
		&sess.ID, &sess.UserID, &sess.TokenHash, &sess.CSRFToken,
		&sessExpires, &sessCreated, &sess.UserAgent, &sess.IP,
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.FullName, &u.Role, &u.Status,
		&lastLogin, &userCreated, &userUpdated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("查询会话失败: %w", err)
	}

	sess.ExpiresAt = sessExpires.Time
	sess.CreatedAt = sessCreated.Time
	u.LastLoginAt = lastLogin.Ptr()
	u.CreatedAt = userCreated.Time
	u.UpdatedAt = userUpdated.Time

	return &sess, &u, nil
}

// DeleteSession 删除单个会话（退出登录）。
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteSessionsByUser 删除某用户的全部会话；exceptTokenHash 非空时保留该会话。
func (s *Store) DeleteSessionsByUser(ctx context.Context, userID int64, exceptTokenHash string) (int64, error) {
	var (
		res sql.Result
		err error
	)
	if exceptTokenHash == "" {
		res, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	} else {
		res, err = s.db.ExecContext(ctx,
			`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, exceptTokenHash)
	}
	if err != nil {
		return 0, fmt.Errorf("删除会话失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DeleteSessionByID 删除指定用户的一条会话（用于「退出其他设备」）。
// userID 参与条件判断，确保用户只能操作自己的会话。
func (s *Store) DeleteSessionByID(ctx context.Context, id, userID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CleanupExpiredSessions 清理已过期会话，返回删除条数。
func (s *Store) CleanupExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("清理过期会话失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ListUserSessions 列出某用户当前有效的会话（用于「登录设备」展示）。
func (s *Store) ListUserSessions(ctx context.Context, userID int64) ([]models.Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, token_hash, csrf_token, expires_at, created_at, user_agent, ip
		FROM sessions WHERE user_id = ? AND expires_at > ?
		ORDER BY created_at DESC`, userID, nowUTC())
	if err != nil {
		return nil, fmt.Errorf("查询会话列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]models.Session, 0, 8)
	for rows.Next() {
		var (
			sess                     models.Session
			sessExpires, sessCreated dbTime
		)
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.CSRFToken,
			&sessExpires, &sessCreated, &sess.UserAgent, &sess.IP); err != nil {
			return nil, fmt.Errorf("解析会话记录失败: %w", err)
		}
		sess.ExpiresAt = sessExpires.Time
		sess.CreatedAt = sessCreated.Time
		out = append(out, sess)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// 找回密码尝试（用于限制安全问题答题的暴力破解）
// ---------------------------------------------------------------------------

// RecordRecoveryAttempt 记录一次找回密码尝试。
// identifier 允许为空，userID 为 0 表示账号不存在——这两种情况也要留痕，
// 以便对「猜账号」行为做频率限制。
func (s *Store) RecordRecoveryAttempt(ctx context.Context, userID int64, identifier, ip string, success bool) error {
	ok := 0
	if success {
		ok = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO recovery_attempts (user_id, identifier, ip, success, created_at)
		VALUES (?, ?, ?, ?, ?)`, userID, identifier, ip, ok, nowUTC())
	if err != nil {
		return fmt.Errorf("记录找回密码尝试失败: %w", err)
	}
	return nil
}

// CountFailedRecovery 统计某账号在给定时间之后的失败次数。
func (s *Store) CountFailedRecovery(ctx context.Context, userID int64, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM recovery_attempts
		WHERE user_id = ? AND success = 0 AND created_at >= ?`,
		userID, since.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计找回密码失败次数失败: %w", err)
	}
	return n, nil
}

// CountFailedRecoveryByIdentifier 统计某标识（用户名或邮箱）在给定时间之后的失败次数。
// 用于账号尚不存在时的频率限制。
func (s *Store) CountFailedRecoveryByIdentifier(ctx context.Context, identifier string, since time.Time) (int, error) {
	if identifier == "" {
		return 0, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM recovery_attempts
		WHERE identifier = ? AND success = 0 AND created_at >= ?`,
		identifier, since.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计找回密码失败次数失败: %w", err)
	}
	return n, nil
}

// ClearFailedRecovery 在成功重置密码后清空该账号的失败记录。
func (s *Store) ClearFailedRecovery(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM recovery_attempts WHERE user_id = ? AND success = 0`, userID)
	return err
}

// CleanupRecoveryAttempts 清理指定时间之前的找回密码记录。
func (s *Store) CleanupRecoveryAttempts(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM recovery_attempts WHERE created_at < ?`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("清理找回密码记录失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ---------------------------------------------------------------------------
// 登录尝试（用于失败次数限制）
// ---------------------------------------------------------------------------

// RecordLoginAttempt 记录一次登录尝试。
func (s *Store) RecordLoginAttempt(ctx context.Context, identifier, ip string, success bool) error {
	ok := 0
	if success {
		ok = 1
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO login_attempts (identifier, ip, success, created_at)
		VALUES (?, ?, ?, ?)`, identifier, ip, ok, nowUTC())
	if err != nil {
		return fmt.Errorf("记录登录尝试失败: %w", err)
	}
	return nil
}

// CountFailedAttempts 统计某标识在给定时间之后的失败次数。
func (s *Store) CountFailedAttempts(ctx context.Context, identifier string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM login_attempts
		WHERE identifier = ? AND success = 0 AND created_at >= ?`,
		identifier, since.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计登录失败次数失败: %w", err)
	}
	return n, nil
}

// ClearFailedAttempts 在成功登录后清空该标识的失败记录。
func (s *Store) ClearFailedAttempts(ctx context.Context, identifier string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM login_attempts WHERE identifier = ? AND success = 0`, identifier)
	return err
}

// CleanupLoginAttempts 清理指定时间之前的登录记录。
func (s *Store) CleanupLoginAttempts(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM login_attempts WHERE created_at < ?`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("清理登录记录失败: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
