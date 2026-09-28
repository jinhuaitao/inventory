package services

import (
	"context"
	"errors"
	"sync"
	"testing"

	"inventory/internal/models"
)

// newTestProduct 创建测试商品，返回商品与操作人 ID。
func newTestProduct(t *testing.T, store *Store, initialQty int) (*models.Product, int64) {
	t.Helper()

	ctx := context.Background()
	operatorID := seedUser(t, store)

	product, err := store.CreateProduct(ctx, ProductInput{
		SKU:         "T-001",
		Name:        "测试商品",
		Unit:        "个",
		CostPrice:   10,
		SalePrice:   20,
		Quantity:    initialQty,
		SafetyStock: 20,
		Status:      models.ProductStatusActive,
	}, operatorID)
	if err != nil {
		t.Fatalf("创建测试商品失败: %v", err)
	}
	return product, operatorID
}

func TestCreateProductWithInitialStock(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	product, operatorID := newTestProduct(t, store, 100)

	if product.Quantity != 100 {
		t.Errorf("期初库存 = %d, 期望 100", product.Quantity)
	}
	if product.SKU != "T-001" {
		t.Errorf("SKU = %q", product.SKU)
	}
	if product.StockValue() != 1000 {
		t.Errorf("库存价值 = %v, 期望 1000", product.StockValue())
	}
	if !product.IsLowStock() == false && product.SafetyStock != 20 {
		t.Error("库存充足时不应触发预警")
	}

	// 期初建账应自动生成一条流水
	movements, err := store.ListProductMovements(ctx, product.ID, 10)
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if len(movements) != 1 {
		t.Fatalf("期初应生成 1 条流水，实际 %d 条", len(movements))
	}
	if movements[0].Type != models.MovementInit {
		t.Errorf("流水类型 = %q, 期望 init", movements[0].Type)
	}
	if movements[0].AfterQty != 100 {
		t.Errorf("流水结存 = %d, 期望 100", movements[0].AfterQty)
	}
	if movements[0].OperatorID != operatorID {
		t.Error("流水操作人记录不正确")
	}
}

func TestApplyMovementFullFlow(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	product, operatorID := newTestProduct(t, store, 100)

	// ---- 入库 50 ----
	in, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID: product.ID, Quantity: 50, UnitPrice: 10,
		RefNo: "PO-001", OperatorID: operatorID, Note: "采购入库",
	})
	if err != nil {
		t.Fatalf("入库失败: %v", err)
	}
	if in.BeforeQty != 100 || in.AfterQty != 150 {
		t.Errorf("入库前后库存 = %d → %d, 期望 100 → 150", in.BeforeQty, in.AfterQty)
	}
	if in.Delta != 50 {
		t.Errorf("入库变动量 = %d, 期望 +50", in.Delta)
	}

	// ---- 出库 30 ----
	out, err := store.ApplyMovement(ctx, models.MovementOut, MovementInput{
		ProductID: product.ID, Quantity: 30, UnitPrice: 20,
		RefNo: "SO-001", OperatorID: operatorID, Note: "销售出库",
	})
	if err != nil {
		t.Fatalf("出库失败: %v", err)
	}
	if out.BeforeQty != 150 || out.AfterQty != 120 {
		t.Errorf("出库前后库存 = %d → %d, 期望 150 → 120", out.BeforeQty, out.AfterQty)
	}
	if out.Delta != -30 {
		t.Errorf("出库变动量 = %d, 期望 -30", out.Delta)
	}

	// ---- 商品库存应与流水一致 ----
	updated, err := store.GetProduct(ctx, product.ID)
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if updated.Quantity != 120 {
		t.Errorf("商品库存 = %d, 期望 120", updated.Quantity)
	}

	// ---- 流水条数：期初 + 入库 + 出库 = 3 ----
	_, pg, err := store.ListMovements(ctx, MovementFilter{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if pg.Total != 3 {
		t.Errorf("流水总数 = %d, 期望 3", pg.Total)
	}
}

func TestApplyMovementInsufficientStock(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	product, operatorID := newTestProduct(t, store, 10)

	_, err := store.ApplyMovement(ctx, models.MovementOut, MovementInput{
		ProductID: product.ID, Quantity: 999, OperatorID: operatorID,
	})
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("超量出库应返回 ErrInsufficientStock，实际: %v", err)
	}

	// 失败后库存必须保持不变（事务已回滚）
	p, err := store.GetProduct(ctx, product.ID)
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if p.Quantity != 10 {
		t.Errorf("失败的出库不应改变库存，当前 = %d, 期望 10", p.Quantity)
	}

	// 且不应留下流水
	movements, err := store.ListProductMovements(ctx, product.ID, 10)
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if len(movements) != 1 {
		t.Errorf("失败的出库不应产生流水，实际流水 %d 条", len(movements))
	}
}

func TestApplyMovementValidation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	product, operatorID := newTestProduct(t, store, 100)

	tests := []struct {
		name string
		typ  models.MovementType
		in   MovementInput
	}{
		{"数量为 0", models.MovementIn, MovementInput{ProductID: product.ID, Quantity: 0, OperatorID: operatorID}},
		{"数量为负", models.MovementIn, MovementInput{ProductID: product.ID, Quantity: -5, OperatorID: operatorID}},
		{"缺少商品", models.MovementIn, MovementInput{ProductID: 0, Quantity: 5, OperatorID: operatorID}},
		{"缺少操作人", models.MovementIn, MovementInput{ProductID: product.ID, Quantity: 5}},
		{"单价为负", models.MovementIn, MovementInput{ProductID: product.ID, Quantity: 5, UnitPrice: -1, OperatorID: operatorID}},
		{"非法类型", models.MovementType("bogus"), MovementInput{ProductID: product.ID, Quantity: 5, OperatorID: operatorID}},
		{"禁止直接调用盘点类型", models.MovementAdjust, MovementInput{ProductID: product.ID, Quantity: 5, OperatorID: operatorID}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.ApplyMovement(ctx, tt.typ, tt.in); err == nil {
				t.Error("期望返回错误，实际成功")
			}
		})
	}

	// 商品不存在
	if _, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID: 999999, Quantity: 1, OperatorID: operatorID,
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的商品应返回 ErrNotFound，实际: %v", err)
	}
}

func TestAdjustStock(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	product, operatorID := newTestProduct(t, store, 120)

	// 盘亏
	mv, err := store.AdjustStock(ctx, product.ID, 118, "月末盘点", operatorID)
	if err != nil {
		t.Fatalf("盘点失败: %v", err)
	}
	if mv.BeforeQty != 120 || mv.AfterQty != 118 {
		t.Errorf("盘点前后 = %d → %d, 期望 120 → 118", mv.BeforeQty, mv.AfterQty)
	}
	if mv.Delta != -2 {
		t.Errorf("盘点差异 = %d, 期望 -2", mv.Delta)
	}
	if mv.Type != models.MovementAdjust {
		t.Errorf("流水类型 = %q, 期望 adjust", mv.Type)
	}

	// 盘盈
	mv, err = store.AdjustStock(ctx, product.ID, 130, "补录", operatorID)
	if err != nil {
		t.Fatalf("盘点失败: %v", err)
	}
	if mv.Delta != 12 {
		t.Errorf("盘盈差异 = %d, 期望 +12", mv.Delta)
	}

	// 盘点为 0
	mv, err = store.AdjustStock(ctx, product.ID, 0, "清空", operatorID)
	if err != nil {
		t.Fatalf("盘点失败: %v", err)
	}
	if mv.AfterQty != 0 {
		t.Errorf("盘点后库存 = %d, 期望 0", mv.AfterQty)
	}

	p, err := store.GetProduct(ctx, product.ID)
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if p.Quantity != 0 {
		t.Errorf("商品库存 = %d, 期望 0", p.Quantity)
	}
	if !p.IsOutOfStock() {
		t.Error("库存为 0 时应判定为缺货")
	}

	// 负数盘点应被拒绝
	if _, err := store.AdjustStock(ctx, product.ID, -1, "", operatorID); err == nil {
		t.Error("负数盘点应被拒绝")
	}
}

// TestConcurrentMovements 验证并发出入库不会丢失更新。
// 这是库存系统最关键的正确性保证。
func TestConcurrentMovements(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	product, operatorID := newTestProduct(t, store, 0)

	const goroutines = 10
	const perGoroutine = 10

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*perGoroutine)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				if _, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
					ProductID: product.ID, Quantity: 1, UnitPrice: 10, OperatorID: operatorID,
				}); err != nil {
					errCh <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("并发入库出现错误: %v", err)
	}

	expected := goroutines * perGoroutine
	p, err := store.GetProduct(ctx, product.ID)
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if p.Quantity != expected {
		t.Errorf("并发入库后库存 = %d, 期望 %d（说明存在丢失更新）", p.Quantity, expected)
	}

	// 流水条数应与成功操作数一致
	_, pg, err := store.ListMovements(ctx, MovementFilter{Page: 1, PerPage: 1})
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if pg.Total != expected {
		t.Errorf("流水总数 = %d, 期望 %d", pg.Total, expected)
	}
}

func TestDashboardStats(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	product, operatorID := newTestProduct(t, store, 100)

	if _, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID: product.ID, Quantity: 50, UnitPrice: 10, OperatorID: operatorID,
	}); err != nil {
		t.Fatalf("入库失败: %v", err)
	}
	if _, err := store.ApplyMovement(ctx, models.MovementOut, MovementInput{
		ProductID: product.ID, Quantity: 30, UnitPrice: 20, OperatorID: operatorID,
	}); err != nil {
		t.Fatalf("出库失败: %v", err)
	}

	stats, err := store.DashboardStats(ctx)
	if err != nil {
		t.Fatalf("统计仪表盘失败: %v", err)
	}

	if stats.TotalProducts != 1 {
		t.Errorf("商品总数 = %d, 期望 1", stats.TotalProducts)
	}
	if stats.TotalStock != 120 {
		t.Errorf("库存总量 = %d, 期望 120", stats.TotalStock)
	}
	if stats.StockValue != 1200 {
		t.Errorf("库存总值 = %v, 期望 1200", stats.StockValue)
	}
	if stats.TotalMovements != 3 {
		t.Errorf("流水总数 = %d, 期望 3", stats.TotalMovements)
	}
	if stats.TodayIn != 50 {
		t.Errorf("今日入库 = %d, 期望 50", stats.TodayIn)
	}
	if stats.TodayOut != 30 {
		t.Errorf("今日出库 = %d, 期望 30", stats.TodayOut)
	}
	if stats.TotalUsers != 1 {
		t.Errorf("用户总数 = %d, 期望 1", stats.TotalUsers)
	}
}

func TestListProductsFilterAndPagination(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	operatorID := seedUser(t, store)

	category, err := store.CreateCategory(ctx, "测试分类", "")
	if err != nil {
		t.Fatalf("创建分类失败: %v", err)
	}

	for i := 1; i <= 25; i++ {
		sku := "SKU-" + padNumber(i)
		catID := category.ID
		_, err := store.CreateProduct(ctx, ProductInput{
			SKU:         sku,
			Name:        "商品" + padNumber(i),
			Unit:        "个",
			CostPrice:   10,
			SalePrice:   20,
			Quantity:    i,
			SafetyStock: 5,
			CategoryID:  &catID,
			Status:      models.ProductStatusActive,
		}, operatorID)
		if err != nil {
			t.Fatalf("创建商品 %s 失败: %v", sku, err)
		}
	}

	// 分页
	products, pg, err := store.ListProducts(ctx, ProductFilter{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if len(products) != 10 {
		t.Errorf("第 1 页应返回 10 条，实际 %d", len(products))
	}
	if pg.Total != 25 || pg.TotalPages != 3 {
		t.Errorf("分页信息 = 总数 %d / 总页数 %d, 期望 25 / 3", pg.Total, pg.TotalPages)
	}

	// 第二页
	products, _, err = store.ListProducts(ctx, ProductFilter{Page: 2, PerPage: 10})
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if len(products) != 10 {
		t.Errorf("第 2 页应返回 10 条，实际 %d", len(products))
	}

	// 末页
	products, _, err = store.ListProducts(ctx, ProductFilter{Page: 3, PerPage: 10})
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if len(products) != 5 {
		t.Errorf("第 3 页应返回 5 条，实际 %d", len(products))
	}

	// 搜索
	products, pg, err = store.ListProducts(ctx, ProductFilter{Search: "SKU-001", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("搜索失败: %v", err)
	}
	if pg.Total != 1 {
		t.Errorf("搜索 SKU-001 应命中 1 条，实际 %d", pg.Total)
	}

	// 分类筛选
	_, pg, err = store.ListProducts(ctx, ProductFilter{CategoryID: category.ID, Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("按分类筛选失败: %v", err)
	}
	if pg.Total != 25 {
		t.Errorf("分类筛选应命中 25 条，实际 %d", pg.Total)
	}

	// 低库存筛选（数量 1-5 的共 5 条，安全库存为 5）
	_, pg, err = store.ListProducts(ctx, ProductFilter{LowStockOnly: true, Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("低库存筛选失败: %v", err)
	}
	if pg.Total != 5 {
		t.Errorf("低库存应命中 5 条，实际 %d", pg.Total)
	}
}

func TestDeleteProductWithMovements(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	product, _ := newTestProduct(t, store, 100)

	// 有流水时不允许删除，避免审计记录丢失
	err := store.DeleteProduct(ctx, product.ID)
	if err == nil {
		t.Fatal("存在流水的商品不应允许删除")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("应返回 ErrInvalidInput，实际: %v", err)
	}

	// 归档是允许的
	if err := store.SetProductStatus(ctx, product.ID, models.ProductStatusArchived); err != nil {
		t.Fatalf("归档商品失败: %v", err)
	}

	// 归档后默认列表不再展示
	_, pg, err := store.ListProducts(ctx, ProductFilter{Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if pg.Total != 0 {
		t.Errorf("归档商品不应出现在默认列表中，实际 %d 条", pg.Total)
	}

	// 显式指定状态可以看到
	_, pg, err = store.ListProducts(ctx, ProductFilter{Status: "archived", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("查询归档商品失败: %v", err)
	}
	if pg.Total != 1 {
		t.Errorf("归档列表应有 1 条，实际 %d", pg.Total)
	}
}

// padNumber 把数字补齐为 3 位，便于生成有序 SKU。
func padNumber(n int) string {
	s := ""
	if n < 10 {
		s = "00"
	} else if n < 100 {
		s = "0"
	}
	return s + itoaHelper(n)
}

func itoaHelper(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
