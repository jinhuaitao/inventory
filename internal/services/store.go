// Package services 承载全部业务逻辑与数据访问。
//
// 设计上刻意保持轻量：直接使用 database/sql 手写 SQL，
// 不引入 ORM，以便精确控制事务、索引与查询计划。
package services

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"inventory/internal/utils"
)

// displayZone 与界面展示时区保持一致。
var displayZone = utils.DisplayZone

// scanner 抽象 *sql.Row 与 *sql.Rows 的公共方法，便于复用扫描逻辑。
type scanner interface {
	Scan(dest ...any) error
}

// Store 是全部数据访问方法的集合。
type Store struct {
	db     *sql.DB
	logger *slog.Logger
}

// New 创建 Store。
func New(db *sql.DB, logger *slog.Logger) *Store {
	return &Store{db: db, logger: logger}
}

// DB 暴露底层连接，供健康检查等场景使用。
func (s *Store) DB() *sql.DB { return s.db }

// Cleanup 清理过期会话、陈旧的登录记录与找回密码记录。
// 由后台定时任务周期调用。
func (s *Store) Cleanup(ctx context.Context) (map[string]int64, error) {
	result := map[string]int64{}

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	sessions, err := s.CleanupExpiredSessions(ctx2)
	if err != nil {
		return result, err
	}
	result["sessions"] = sessions

	attempts, err := s.CleanupLoginAttempts(ctx2, nowUTC().Add(-24*time.Hour))
	if err != nil {
		return result, err
	}
	result["login_attempts"] = attempts

	recoveries, err := s.CleanupRecoveryAttempts(ctx2, nowUTC().Add(-24*time.Hour))
	if err != nil {
		return result, err
	}
	result["recovery_attempts"] = recoveries

	return result, nil
}
