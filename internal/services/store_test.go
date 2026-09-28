package services

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"inventory/internal/database"
)

// newTestStore 创建一个基于临时文件的真实 SQLite 数据库，
// 保证事务、外键与并发行为与生产环境一致。
func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("初始化测试数据库结构失败: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(db, logger), db
}

// seedUser 创建一个用于测试的操作人账号。
func seedUser(t *testing.T, store *Store) int64 {
	t.Helper()

	user, err := store.CreateUser(context.Background(), CreateUserInput{
		Username: "tester",
		Email:    "tester@example.com",
		Password: "Test@12345",
		FullName: "测试操作员",
		Role:     "admin",
	})
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	return user.ID
}
