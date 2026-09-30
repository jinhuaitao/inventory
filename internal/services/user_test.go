package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"inventory/internal/models"
)

func TestCreateUserAndPassword(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "Alice",
		Email:    "Alice@Example.COM",
		Password: "Secret@123",
		FullName: "爱丽丝",
		Role:     models.RoleManager,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	if user.Username != "Alice" {
		t.Errorf("用户名 = %q, 期望 Alice", user.Username)
	}
	// 邮箱应被规范化为小写
	if user.Email != "alice@example.com" {
		t.Errorf("邮箱 = %q, 期望小写形式", user.Email)
	}
	if user.Role != models.RoleManager {
		t.Errorf("角色 = %q", user.Role)
	}
	if user.Status != models.UserStatusActive {
		t.Errorf("新用户状态应为 active，实际 %q", user.Status)
	}

	// 密码不应以明文存储
	if user.PasswordHash == "Secret@123" {
		t.Fatal("密码不能明文存储")
	}
	if !VerifyPassword(user.PasswordHash, "Secret@123") {
		t.Error("正确密码应校验通过")
	}
	if VerifyPassword(user.PasswordHash, "WrongPass") {
		t.Error("错误密码不应通过校验")
	}
}

func TestCreateUserDuplicate(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	_, err := store.CreateUser(ctx, CreateUserInput{
		Username: "bob", Email: "bob@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}

	// 重复用户名
	_, err = store.CreateUser(ctx, CreateUserInput{
		Username: "bob", Email: "other@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("重复用户名应返回 ErrConflict，实际: %v", err)
	}

	// 重复邮箱（大小写不敏感）
	_, err = store.CreateUser(ctx, CreateUserInput{
		Username: "bob2", Email: "BOB@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("重复邮箱应返回 ErrConflict，实际: %v", err)
	}

	// 非法角色
	_, err = store.CreateUser(ctx, CreateUserInput{
		Username: "bob3", Email: "bob3@example.com",
		Password: "Secret@123", Role: models.Role("superuser"),
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("非法角色应返回 ErrInvalidInput，实际: %v", err)
	}
}

func TestGetUserByIdentifier(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := store.CreateUser(ctx, CreateUserInput{
		Username: "carol", Email: "carol@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	}); err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	// 用用户名登录（大小写不敏感）
	if _, err := store.GetUserByIdentifier(ctx, "carol"); err != nil {
		t.Errorf("按用户名查询失败: %v", err)
	}
	if _, err := store.GetUserByIdentifier(ctx, "CAROL"); err != nil {
		t.Errorf("按用户名查询应大小写不敏感: %v", err)
	}
	// 用邮箱登录
	if _, err := store.GetUserByIdentifier(ctx, "carol@example.com"); err != nil {
		t.Errorf("按邮箱查询失败: %v", err)
	}
	// 不存在的用户
	if _, err := store.GetUserByIdentifier(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的用户应返回 ErrNotFound，实际: %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "dave", Email: "dave@example.com",
		Password: "OldPass@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	// 错误的当前密码
	err = store.ChangePassword(ctx, user.ID, "WrongOld@123", "NewPass@456")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("错误的当前密码应返回 ErrInvalidCredentials，实际: %v", err)
	}

	// 正确的修改
	if err := store.ChangePassword(ctx, user.ID, "OldPass@123", "NewPass@456"); err != nil {
		t.Fatalf("修改密码失败: %v", err)
	}

	updated, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("查询用户失败: %v", err)
	}
	if !VerifyPassword(updated.PasswordHash, "NewPass@456") {
		t.Error("新密码应可校验通过")
	}
	if VerifyPassword(updated.PasswordHash, "OldPass@123") {
		t.Error("旧密码应失效")
	}
}

func TestLastAdminProtection(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	admin, err := store.CreateUser(ctx, CreateUserInput{
		Username: "admin1", Email: "admin1@example.com",
		Password: "Admin@12345", Role: models.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}

	// 唯一管理员不能被降级
	err = store.UpdateUserByAdmin(ctx, admin.ID, AdminUpdateUserInput{
		FullName: "管理员", Email: "admin1@example.com",
		Role: models.RoleViewer, Status: models.UserStatusActive,
	})
	if !errors.Is(err, ErrLastAdmin) {
		t.Errorf("降级最后一个管理员应返回 ErrLastAdmin，实际: %v", err)
	}

	// 唯一管理员不能被禁用
	if err := store.SetUserStatus(ctx, admin.ID, models.UserStatusDisabled); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("禁用最后一个管理员应返回 ErrLastAdmin，实际: %v", err)
	}

	// 唯一管理员不能被删除
	if err := store.DeleteUser(ctx, admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("删除最后一个管理员应返回 ErrLastAdmin，实际: %v", err)
	}

	// 增加第二个管理员后即可降级第一个
	if _, err := store.CreateUser(ctx, CreateUserInput{
		Username: "admin2", Email: "admin2@example.com",
		Password: "Admin@12345", Role: models.RoleAdmin,
	}); err != nil {
		t.Fatalf("创建第二个管理员失败: %v", err)
	}

	if err := store.UpdateUserByAdmin(ctx, admin.ID, AdminUpdateUserInput{
		FullName: "管理员", Email: "admin1@example.com",
		Role: models.RoleViewer, Status: models.UserStatusActive,
	}); err != nil {
		t.Errorf("存在多个管理员时应允许降级，实际: %v", err)
	}
}

func TestUpdateProfile(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "erin", Email: "erin@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	if err := store.UpdateProfile(ctx, user.ID, "艾琳", "erin.new@example.com"); err != nil {
		t.Fatalf("更新资料失败: %v", err)
	}

	updated, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("查询用户失败: %v", err)
	}
	if updated.FullName != "艾琳" {
		t.Errorf("姓名 = %q", updated.FullName)
	}
	if updated.Email != "erin.new@example.com" {
		t.Errorf("邮箱 = %q", updated.Email)
	}

	// 非法邮箱
	if err := store.UpdateProfile(ctx, user.ID, "艾琳", "bad-email"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("非法邮箱应返回 ErrInvalidInput，实际: %v", err)
	}
}

func TestLoginAttemptTracking(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	identifier := "target@example.com"

	count, err := store.CountFailedAttempts(ctx, identifier, nowUTC().AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if count != 0 {
		t.Errorf("初始失败次数应为 0，实际 %d", count)
	}

	for i := 0; i < 3; i++ {
		if err := store.RecordLoginAttempt(ctx, identifier, "127.0.0.1", false); err != nil {
			t.Fatalf("记录登录尝试失败: %v", err)
		}
	}
	if err := store.RecordLoginAttempt(ctx, identifier, "127.0.0.1", true); err != nil {
		t.Fatalf("记录登录尝试失败: %v", err)
	}

	count, err = store.CountFailedAttempts(ctx, identifier, nowUTC().AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if count != 3 {
		t.Errorf("失败次数 = %d, 期望 3（成功的尝试不计入）", count)
	}

	// 成功登录后清空失败记录
	if err := store.ClearFailedAttempts(ctx, identifier); err != nil {
		t.Fatalf("清空失败记录出错: %v", err)
	}
	count, err = store.CountFailedAttempts(ctx, identifier, nowUTC().AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("统计失败次数出错: %v", err)
	}
	if count != 0 {
		t.Errorf("清空后失败次数应为 0，实际 %d", count)
	}
}

func TestRecoveryAttemptThrottle(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "frank", Email: "frank@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	identifier := "frank"
	since := nowUTC().AddDate(0, 0, -1)

	// 初始没有失败记录
	if n, err := store.CountFailedRecovery(ctx, user.ID, since); err != nil || n != 0 {
		t.Fatalf("初始失败次数应为 0，实际 %d（错误 %v）", n, err)
	}

	// 连续记录 3 次失败
	for i := 0; i < 3; i++ {
		if err := store.RecordRecoveryAttempt(ctx, user.ID, identifier, "127.0.0.1", false); err != nil {
			t.Fatalf("记录失败尝试出错: %v", err)
		}
	}
	if n, err := store.CountFailedRecovery(ctx, user.ID, since); err != nil || n != 3 {
		t.Fatalf("失败次数应为 3，实际 %d（错误 %v）", n, err)
	}

	// 成功一次不影响失败计数
	if err := store.RecordRecoveryAttempt(ctx, user.ID, identifier, "127.0.0.1", true); err != nil {
		t.Fatalf("记录成功尝试出错: %v", err)
	}
	if n, err := store.CountFailedRecovery(ctx, user.ID, since); err != nil || n != 3 {
		t.Fatalf("成功记录不应计入失败次数，实际 %d", n)
	}

	// 按标识统计（账号不存在时用于限流）
	if n, err := store.CountFailedRecoveryByIdentifier(ctx, identifier, since); err != nil || n != 3 {
		t.Fatalf("按标识统计失败次数应为 3，实际 %d（错误 %v）", n, err)
	}
	if n, err := store.CountFailedRecoveryByIdentifier(ctx, "", since); err != nil || n != 0 {
		t.Fatalf("空标识应返回 0，实际 %d", n)
	}

	// 清空后归零
	if err := store.ClearFailedRecovery(ctx, user.ID); err != nil {
		t.Fatalf("清空失败记录出错: %v", err)
	}
	if n, err := store.CountFailedRecovery(ctx, user.ID, since); err != nil || n != 0 {
		t.Fatalf("清空后失败次数应为 0，实际 %d", n)
	}

	// 清理早于时间点的记录
	if _, err := store.CleanupRecoveryAttempts(ctx, nowUTC().Add(time.Hour)); err != nil {
		t.Fatalf("清理找回记录出错: %v", err)
	}
	if n, err := store.CountFailedRecoveryByIdentifier(ctx, identifier, since); err != nil || n != 0 {
		t.Fatalf("清理后应无记录，实际 %d", n)
	}
}

func TestSessionLifecycle(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	user, err := store.CreateUser(ctx, CreateUserInput{
		Username: "grace", Email: "grace@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	sess := &models.Session{
		UserID:    user.ID,
		TokenHash: "session-token-hash",
		CSRFToken: "csrf-token-value",
		ExpiresAt: nowUTC().AddDate(0, 0, 1),
		UserAgent: "test-agent",
		IP:        "127.0.0.1",
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	// 加载会话与用户
	loaded, loadedUser, err := store.GetSessionUser(ctx, "session-token-hash")
	if err != nil {
		t.Fatalf("加载会话失败: %v", err)
	}
	if loaded.CSRFToken != "csrf-token-value" {
		t.Errorf("CSRF 令牌 = %q", loaded.CSRFToken)
	}
	if loadedUser.ID != user.ID || loadedUser.Username != "grace" {
		t.Error("会话关联的用户信息不正确")
	}

	// 不存在的令牌
	if _, _, err := store.GetSessionUser(ctx, "no-such-token"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的会话应返回 ErrNotFound，实际: %v", err)
	}

	// 列出会话
	sessions, err := store.ListUserSessions(ctx, user.ID)
	if err != nil {
		t.Fatalf("列出会话失败: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("会话数 = %d, 期望 1", len(sessions))
	}

	// 删除会话
	if err := store.DeleteSession(ctx, "session-token-hash"); err != nil {
		t.Fatalf("删除会话失败: %v", err)
	}
	if _, _, err := store.GetSessionUser(ctx, "session-token-hash"); !errors.Is(err, ErrNotFound) {
		t.Error("会话删除后应查询不到")
	}
}

func TestCategoryAndSupplierCRUD(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// ---- 分类 ----
	cat, err := store.CreateCategory(ctx, "电子配件", "数据线与充电器")
	if err != nil {
		t.Fatalf("创建分类失败: %v", err)
	}
	if cat.Name != "电子配件" {
		t.Errorf("分类名 = %q", cat.Name)
	}

	if _, err := store.CreateCategory(ctx, "电子配件", ""); !errors.Is(err, ErrConflict) {
		t.Error("重名分类应被拒绝")
	}
	if _, err := store.CreateCategory(ctx, "", ""); !errors.Is(err, ErrInvalidInput) {
		t.Error("空名称分类应被拒绝")
	}

	if err := store.UpdateCategory(ctx, cat.ID, "电子配件（新）", "已更新"); err != nil {
		t.Fatalf("更新分类失败: %v", err)
	}
	updated, err := store.GetCategory(ctx, cat.ID)
	if err != nil {
		t.Fatalf("查询分类失败: %v", err)
	}
	if updated.Name != "电子配件（新）" {
		t.Errorf("分类名 = %q", updated.Name)
	}

	// ---- 供应商 ----
	sup, err := store.CreateSupplier(ctx, SupplierInput{
		Name: "深圳宏远", ContactPerson: "张伟", Phone: "0755-88886666",
		Email: "sales@example.com", Address: "深圳市南山区",
	})
	if err != nil {
		t.Fatalf("创建供应商失败: %v", err)
	}
	if sup.Name != "深圳宏远" {
		t.Errorf("供应商名 = %q", sup.Name)
	}

	if _, err := store.CreateSupplier(ctx, SupplierInput{Name: "深圳宏远"}); !errors.Is(err, ErrConflict) {
		t.Error("重名供应商应被拒绝")
	}
	if _, err := store.CreateSupplier(ctx, SupplierInput{Name: "测试", Email: "bad"}); !errors.Is(err, ErrInvalidInput) {
		t.Error("非法邮箱供应商应被拒绝")
	}

	// 删除分类后，商品的分类应被置空
	operatorID := seedUser(t, store)
	catID := cat.ID
	product, err := store.CreateProduct(ctx, ProductInput{
		SKU: "C-001", Name: "带分类的商品", Unit: "个",
		CategoryID: &catID, CostPrice: 1, SalePrice: 2,
		Status: models.ProductStatusActive,
	}, operatorID)
	if err != nil {
		t.Fatalf("创建商品失败: %v", err)
	}

	affected, err := store.DeleteCategory(ctx, cat.ID)
	if err != nil {
		t.Fatalf("删除分类失败: %v", err)
	}
	if affected != 1 {
		t.Errorf("受影响商品数 = %d, 期望 1", affected)
	}

	reloaded, err := store.GetProduct(ctx, product.ID)
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if reloaded.CategoryID != nil {
		t.Error("分类删除后商品的分类字段应被置空")
	}
}

// ---------------------------------------------------------------------------
// 状态与资料更新的校验
// ---------------------------------------------------------------------------

// TestSetUserStatusRejectsUnknownStatus 锁死状态取值的白名单。
//
// SetUserStatus 会把参数原样写进 status 列，而登录鉴权只认 active。
// 若不校验，一次畸形请求就能把账号改成「既不是 active 也不是 disabled」
// 的取值：账号从此登不上，界面上的「启用」按钮也救不回来。
func TestSetUserStatusRejectsUnknownStatus(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	admin := seedUser(t, store)
	victim, err := store.CreateUser(ctx, CreateUserInput{
		Username: "victim", Email: "victim@example.com",
		Password: "Secret@123", Role: models.RoleViewer,
	})
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	_ = admin

	for _, bad := range []string{"", "ACTIVE", "active ", "deleted", "禁用", "1", "null"} {
		if err := store.SetUserStatus(ctx, victim.ID, bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("SetUserStatus(%q) 应返回 ErrInvalidInput，实际: %v", bad, err)
		}
	}

	// 状态必须原样保持 —— 被拒绝的写入不能留下痕迹
	reloaded, err := store.GetUserByID(ctx, victim.ID)
	if err != nil {
		t.Fatalf("查询用户失败: %v", err)
	}
	if reloaded.Status != models.UserStatusActive {
		t.Errorf("被拒绝的写入不应改动状态，实际 status = %q", reloaded.Status)
	}

	// 合法取值仍然可用
	if err := store.SetUserStatus(ctx, victim.ID, models.UserStatusDisabled); err != nil {
		t.Errorf("禁用账号不应失败: %v", err)
	}
	if err := store.SetUserStatus(ctx, victim.ID, models.UserStatusActive); err != nil {
		t.Errorf("启用账号不应失败: %v", err)
	}
}

// TestSetUserStatusNotFound 对不存在的用户返回 ErrNotFound，而不是静默成功。
func TestSetUserStatusNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	err := store.SetUserStatus(context.Background(), 987654, models.UserStatusDisabled)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("对不存在的用户设置状态应返回 ErrNotFound，实际: %v", err)
	}
}

// TestUpdateProfileNotFound 对不存在的用户返回 ErrNotFound。
//
// UPDATE 影响 0 行时若返回 nil，页面会提示「资料已更新」，
// 而实际上什么都没改 —— 这类假成功最难排查。
func TestUpdateProfileNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	err := store.UpdateProfile(context.Background(), 987654, "查无此人", "ghost@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("对不存在的用户更新资料应返回 ErrNotFound，实际: %v", err)
	}
}

// TestUpdateUserByAdminNotFound 管理员改不存在的用户返回 ErrNotFound。
func TestUpdateUserByAdminNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	err := store.UpdateUserByAdmin(context.Background(), 987654, AdminUpdateUserInput{
		FullName: "查无此人",
		Email:    "ghost@example.com",
		Role:     models.RoleViewer,
		Status:   models.UserStatusActive,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("管理员更新不存在的用户应返回 ErrNotFound，实际: %v", err)
	}
}
