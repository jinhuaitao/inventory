package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"inventory/internal/models"
)

// ---------------------------------------------------------------------------
// 商品导出截断必须可见（与流水导出同一约定）
//
// 曾经的实现写死 LIMIT 且不回报截断，导出的 CSV 悄悄缺数据。
// 这组用例锁死「多取一行」判定：超过上限 -> truncated=true 且只回上限条；
// 恰好等于上限 -> 不算截断。
// ---------------------------------------------------------------------------

func seedProducts(t *testing.T, store *Store, operator int64, n int) {
	t.Helper()

	ctx := context.Background()
	for i := 0; i < n; i++ {
		sku := fmt.Sprintf("RGR-EXP-%02d", i)
		if _, err := store.CreateProduct(ctx, ProductInput{
			SKU:    sku,
			Name:   "回归商品 " + sku,
			Unit:   "件",
			Status: models.ProductStatusActive,
		}, operator); err != nil {
			t.Fatalf("创建商品 %s 失败: %v", sku, err)
		}
	}
}

// TestListProductsForExportReportsTruncation 超过上限时必须报告截断。
func TestListProductsForExportReportsTruncation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)
	seedProducts(t, store, operator, 5)

	products, truncated, err := store.listProductsForExport(ctx, ProductFilter{}, 3)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if !truncated {
		t.Error("结果被截断时 truncated 必须为 true，否则调用方无法告知用户")
	}
	if len(products) != 3 {
		t.Errorf("返回条数 = %d，期望 3", len(products))
	}
}

// TestListProductsForExportExactlyAtLimit 恰好等于上限时不算截断。
func TestListProductsForExportExactlyAtLimit(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)
	seedProducts(t, store, operator, 3)

	products, truncated, err := store.listProductsForExport(ctx, ProductFilter{}, 3)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if truncated {
		t.Error("条数恰好等于上限时不应报告截断")
	}
	if len(products) != 3 {
		t.Errorf("返回条数 = %d，期望 3", len(products))
	}
}

// ---------------------------------------------------------------------------
// 搜索关键词里的 LIKE 通配符必须按字面匹配
//
// 未转义时，搜 "100%" 会命中所有以 100 开头的商品、搜 "a_b" 会命中 "axb"，
// 结果多出一堆无关数据却没有任何报错。转义 + ESCAPE '\' 是最省钱的修法。
// ---------------------------------------------------------------------------

// TestSearchTreatsPercentAsLiteral 百分号按字面搜索。
func TestSearchTreatsPercentAsLiteral(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)

	for _, in := range []ProductInput{
		{SKU: "W-1", Name: "100% 棉T恤", Unit: "件", Status: models.ProductStatusActive},
		{SKU: "W-2", Name: "1000 元笔记本", Unit: "件", Status: models.ProductStatusActive},
	} {
		if _, err := store.CreateProduct(ctx, in, operator); err != nil {
			t.Fatalf("创建商品失败: %v", err)
		}
	}

	products, truncated, err := store.listProductsForExport(ctx, ProductFilter{Search: "100%"}, 100)
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if truncated {
		t.Fatal("结果被意外截断")
	}
	if len(products) != 1 || products[0].Name != "100% 棉T恤" {
		got := make([]string, 0, len(products))
		for _, p := range products {
			got = append(got, p.Name)
		}
		t.Errorf("搜 100%% 应只命中字面量记录，实际命中 %v", got)
	}
}

// TestSearchTreatsUnderscoreAsLiteral 下划线不作为单字符通配。
func TestSearchTreatsUnderscoreAsLiteral(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)

	for _, in := range []ProductInput{
		{SKU: "U-1", Name: "配件 a_b", Unit: "件", Status: models.ProductStatusActive},
		{SKU: "U-2", Name: "配件 axb", Unit: "件", Status: models.ProductStatusActive},
	} {
		if _, err := store.CreateProduct(ctx, in, operator); err != nil {
			t.Fatalf("创建商品失败: %v", err)
		}
	}

	products, _, err := store.listProductsForExport(ctx, ProductFilter{Search: "a_b"}, 100)
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if len(products) != 1 || products[0].Name != "配件 a_b" {
		t.Errorf("搜 a_b 不应命中 axb，实际命中 %d 条", len(products))
	}
}

// ---------------------------------------------------------------------------
// 登录限流按大小写归一后的标识符计数
//
// users.username 是 COLLATE NOCASE：若限流表按原始大小写分桶，
// 攻击者轮换 "Admin"/"ADMIN"/"aDmIn" 就能绕过「同一账号」维度。
// ---------------------------------------------------------------------------

// TestLoginAttemptsAreCaseInsensitive 大小写变体汇入同一计数。
func TestLoginAttemptsAreCaseInsensitive(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	since := time.Now().UTC().Add(-time.Hour)

	for _, name := range []string{"Admin", "ADMIN", "aDmIn"} {
		if err := store.RecordLoginAttempt(ctx, name, "10.0.0.1", false); err != nil {
			t.Fatalf("记录失败尝试 %s: %v", name, err)
		}
	}

	n, err := store.CountFailedAttempts(ctx, "admin", since)
	if err != nil {
		t.Fatalf("统计失败次数: %v", err)
	}
	if n != 3 {
		t.Errorf("大小写变体应汇入同一计数：得到 %d，期望 3", n)
	}

	// 带 IP 维度同样要归一，否则同一人换大小写即可无限试探。
	nIP, err := store.CountFailedAttemptsByIdentifierAndIP(ctx, "ADMIN", "10.0.0.1", since)
	if err != nil {
		t.Fatalf("统计 IP 维度失败次数: %v", err)
	}
	if nIP != 3 {
		t.Errorf("IP 维度计数 = %d，期望 3", nIP)
	}

	// 登录成功后按归一标识符清理，变体写入的桶也必须被清空。
	if err := store.RecordLoginAttempt(ctx, "AdMiN", "10.0.0.1", true); err != nil {
		t.Fatalf("记录成功尝试: %v", err)
	}
	if err := store.ClearFailedAttempts(ctx, "admin"); err != nil {
		t.Fatalf("清理失败记录: %v", err)
	}
	n, err = store.CountFailedAttempts(ctx, "ADMIN", since)
	if err != nil {
		t.Fatalf("复查计数: %v", err)
	}
	if n != 0 {
		t.Errorf("清理后应归零，实际残留 %d 条（大小写桶没对上）", n)
	}
}

// ---------------------------------------------------------------------------
// 注册原子性与防用户枚举
// ---------------------------------------------------------------------------

// TestCreateUserWithQuestionsRollsBackOnInvalid 问题校验失败时不得留下孤儿账号。
//
// 旧实现是 CreateUser + SetSecurityQuestions + 手工 DeleteUser 补偿：
// 中途进程被杀就留下「没有安全问题的账号」，找回密码通道直接失明。
func TestCreateUserWithQuestionsRollsBackOnInvalid(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	in := CreateUserInput{
		Username: "atomicuser",
		Email:    "atomic@example.com",
		Password: "Atomic@12345",
		FullName: "原子性测试",
		Role:     "viewer",
	}
	// 少一题：ValidateSecurityAnswers 必失败
	bad := []models.AnswerInput{{Question: "第一问是什么？", Answer: "a"}, {Question: "第二问是什么？", Answer: "b"}}

	if _, err := store.CreateUserWithQuestions(ctx, in, bad); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("非法安全问题应返回 ErrInvalidInput，实际: %v", err)
	}
	if _, err := store.GetUserByIdentifier(ctx, "atomicuser"); !errors.Is(err, ErrNotFound) {
		t.Errorf("校验失败后不应残留已创建的用户，GetUserByIdentifier 返回 %v", err)
	}

	// 合法输入：账号与全部问题必须一次成型
	good := make([]models.AnswerInput, 0, models.SecurityQuestionCount)
	for i := 0; i < models.SecurityQuestionCount; i++ {
		good = append(good, models.AnswerInput{
			Question: fmt.Sprintf("这是第 %d 个测试问题？", i+1),
			Answer:   fmt.Sprintf("答案%d", i+1),
		})
	}
	if _, err := store.CreateUserWithQuestions(ctx, in, good); err != nil {
		t.Fatalf("合法注册失败: %v", err)
	}
	u, err := store.GetUserByIdentifier(ctx, "atomicuser")
	if err != nil {
		t.Fatalf("注册后应能查到用户: %v", err)
	}
	if ok, err := store.HasSecurityQuestions(ctx, u.ID); err != nil || !ok {
		t.Errorf("注册成功后安全问题应就位，HasSecurityQuestions=%v err=%v", ok, err)
	}
}

// TestBurnPasswordCheckNeverMatches 恒定耗时假比较的返回值必须被忽略。
//
// 用户不存在时也执行一次 bcrypt，抹平「存在的账号响应更慢」这一
// 可被计时探测的枚举侧信道。dummy 口令本身是否匹配无关紧要
// （LoginSubmit 只在 user != nil 时采信验证结果），这里锁住的是
// 「调用不会 panic、返回值不影响判定路径」的约定。
func TestBurnPasswordCheckNeverMatches(t *testing.T) {
	for _, pw := range []string{"", "hunter2", "anything-not-real"} {
		if BurnPasswordCheck(pw) {
			t.Errorf("BurnPasswordCheck(%q) 意外匹配，检查 dummy 哈希是否被替换成真实账号", pw)
		}
	}
}

// ---------------------------------------------------------------------------
// 分类更新的名称长度校验与创建对齐
// ---------------------------------------------------------------------------

// TestUpdateCategoryRejectsOverlongName 更新路径不得绕过 50 字上限。
func TestUpdateCategoryRejectsOverlongName(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	c, err := store.CreateCategory(ctx, "短名", "")
	if err != nil {
		t.Fatalf("创建分类失败: %v", err)
	}

	if err := store.UpdateCategory(ctx, c.ID, strings.Repeat("名", 51), ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("超长分类名应返回 ErrInvalidInput，实际: %v", err)
	}
}
