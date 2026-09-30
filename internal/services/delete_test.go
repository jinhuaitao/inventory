package services

import (
	"context"
	"errors"
	"testing"

	"inventory/internal/models"
)

// ---------------------------------------------------------------------------
// 删除操作的审计保护
//
// 库存流水是本系统的账本：商品、供应商一旦在流水里出现过，
// 就不该被物理删除，否则历史记录要么跟着消失（CASCADE），
// 要么失去出处（SET NULL）。下面这几条用例锁死这个约定。
// ---------------------------------------------------------------------------

// seedProductWithoutMovements 创建一个不带期初库存（也就没有流水）的商品。
func seedProductWithoutMovements(t *testing.T, store *Store, operator int64, sku string) *models.Product {
	t.Helper()

	p, err := store.CreateProduct(context.Background(), ProductInput{
		SKU:    sku,
		Name:   sku + "号商品",
		Unit:   "件",
		Status: models.ProductStatusActive,
	}, operator)
	if err != nil {
		t.Fatalf("创建商品 %s 失败: %v", sku, err)
	}
	return p
}

// TestDeleteProductAllowedWhenNoMovements 没有流水的商品可以正常删除。
func TestDeleteProductAllowedWhenNoMovements(t *testing.T) {
	store, _ := newTestStore(t)
	operator := seedUser(t, store)
	ctx := context.Background()

	p := seedProductWithoutMovements(t, store, operator, "NO-MOVE-001")

	if err := store.DeleteProduct(ctx, p.ID); err != nil {
		t.Fatalf("删除无流水商品不应失败: %v", err)
	}
	if _, err := store.GetProduct(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后商品应查不到，实际错误: %v", err)
	}
}

// TestDeleteProductRejectedWhenHasMovements 有流水的商品必须拒绝删除。
//
// 商品被删却留下流水（或流水跟着消失）都会让账对不上，
// 正确做法是拒绝删除、引导用户改用「归档」。
//
// 关于并发：这里只验证**业务规则**。服务层的「查流水 + 删商品」放在同一个
// 事务里，是为了消除「两次查询之间有人插进一次出入库」的竞态窗口；
// 但那条竞态没法用串行用例稳定复现（需要精确卡在两条语句之间）。
// 真正兜底的是数据库外键 —— stock_movements.product_id 已收紧为
// ON DELETE RESTRICT，即便有人绕过服务层直接 DELETE 也会被拒绝，
// 那条路径由 database 包的 TestMovementForeignKeysRejectDeletingReferencedRows 覆盖。
func TestDeleteProductRejectedWhenHasMovements(t *testing.T) {
	store, _ := newTestStore(t)
	operator := seedUser(t, store)
	ctx := context.Background()

	p := seedProductWithoutMovements(t, store, operator, "HAS-MOVE-001")
	if _, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID:  p.ID,
		Quantity:   5,
		OperatorID: operator,
	}); err != nil {
		t.Fatalf("入库失败: %v", err)
	}

	err := store.DeleteProduct(ctx, p.ID)
	if err == nil {
		t.Fatal("删除已有流水的商品应当被拒绝")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("错误应带 ErrInvalidInput 标记，实际: %v", err)
	}

	// 商品必须还在 —— 拒绝删除不能顺手把它删了
	if _, err := store.GetProduct(ctx, p.ID); err != nil {
		t.Errorf("被拒绝的删除不应改动商品: %v", err)
	}
}

// TestDeleteProductRejectedForInitialStock 期初库存也会产生流水，同样受保护。
//
// 建商品时填了期初库存，CreateProduct 内部会写一条 init 流水；
// 若只在 ApplyMovement 的场景下做检查，这类商品就能被连账一起删掉。
func TestDeleteProductRejectedForInitialStock(t *testing.T) {
	store, _ := newTestStore(t)
	operator := seedUser(t, store)
	ctx := context.Background()

	p, err := store.CreateProduct(ctx, ProductInput{
		SKU:      "INIT-STOCK-001",
		Name:     "带期初库存的商品",
		Unit:     "件",
		Quantity: 12,
		Status:   models.ProductStatusActive,
	}, operator)
	if err != nil {
		t.Fatalf("创建商品失败: %v", err)
	}

	if err := store.DeleteProduct(ctx, p.ID); err == nil {
		t.Fatal("带期初库存（即有 init 流水）的商品不应被删除")
	}
}

// TestDeleteProductNotFound 删除不存在的商品返回 ErrNotFound。
func TestDeleteProductNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.DeleteProduct(context.Background(), 987654); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除不存在的商品应返回 ErrNotFound，实际: %v", err)
	}
}

// TestDeleteSupplierRejectedWhenReferencedByMovements
// 供应商出现在流水里时不允许删除。
//
// 早期 schema 把 stock_movements.supplier_id 定义成 ON DELETE SET NULL，
// 于是删掉一个供应商会把**全部历史入库单**上的供应商字段抹成空 ——
// 采购记录瞬间失去出处。这是本次修复的核心场景。
func TestDeleteSupplierRejectedWhenReferencedByMovements(t *testing.T) {
	store, _ := newTestStore(t)
	operator := seedUser(t, store)
	ctx := context.Background()

	sup, err := store.CreateSupplier(ctx, SupplierInput{Name: "历史供应商"})
	if err != nil {
		t.Fatalf("创建供应商失败: %v", err)
	}
	p := seedProductWithoutMovements(t, store, operator, "SUP-MOVE-001")

	// 一次带供应商的入库，流水里就会记下它
	if _, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID:  p.ID,
		Quantity:   3,
		SupplierID: &sup.ID,
		OperatorID: operator,
	}); err != nil {
		t.Fatalf("入库失败: %v", err)
	}

	_, err = store.DeleteSupplier(ctx, sup.ID)
	if err == nil {
		t.Fatal("删除已出现在流水里的供应商应当被拒绝")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("错误应带 ErrInvalidInput 标记，实际: %v", err)
	}

	// 流水必须原样保留：供应商字段不能被抹成 NULL
	var supplierID *int64
	err = store.DB().QueryRowContext(ctx,
		`SELECT supplier_id FROM stock_movements WHERE product_id = ?`, p.ID).Scan(&supplierID)
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if supplierID == nil || *supplierID != sup.ID {
		t.Errorf("流水的 supplier_id 应保留为 %d，实际 %v", sup.ID, supplierID)
	}
}

// TestDeleteSupplierAllowedWhenNoMovements 没有任何流水引用的供应商可以删除，
// 并正确返回受影响（解除关联）的商品数。
func TestDeleteSupplierAllowedWhenNoMovements(t *testing.T) {
	store, _ := newTestStore(t)
	operator := seedUser(t, store)
	ctx := context.Background()

	sup, err := store.CreateSupplier(ctx, SupplierInput{Name: "可删供应商"})
	if err != nil {
		t.Fatalf("创建供应商失败: %v", err)
	}
	for _, sku := range []string{"SUP-DEL-001", "SUP-DEL-002"} {
		if _, err := store.CreateProduct(ctx, ProductInput{
			SKU:        sku,
			Name:       sku + "号商品",
			Unit:       "件",
			SupplierID: &sup.ID,
			Status:     models.ProductStatusActive,
		}, operator); err != nil {
			t.Fatalf("创建商品 %s 失败: %v", sku, err)
		}
	}

	affected, err := store.DeleteSupplier(ctx, sup.ID)
	if err != nil {
		t.Fatalf("删除无流水引用的供应商不应失败: %v", err)
	}
	if affected != 2 {
		t.Errorf("受影响商品数 = %d，期望 2", affected)
	}

	// 关联商品的供应商字段应被置空，而不是连商品一起删掉
	var nulls int
	if err := store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM products WHERE supplier_id IS NULL`).Scan(&nulls); err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if nulls != 2 {
		t.Errorf("解除关联的商品数 = %d，期望 2", nulls)
	}
}

// TestDeleteSupplierNotFound 删除不存在的供应商返回 ErrNotFound。
func TestDeleteSupplierNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.DeleteSupplier(context.Background(), 987654); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除不存在的供应商应返回 ErrNotFound，实际: %v", err)
	}
}

// TestDeleteCategoryReturnsAccurateAffectedCount
// 删除分类返回的「受影响商品数」必须与实际情况一致。
//
// 统计与删除若分成两条独立语句，中间插入的改动会让这个数字失真，
// 界面据此提示用户的内容也就跟着错了 —— 因此两者必须在同一事务内完成。
func TestDeleteCategoryReturnsAccurateAffectedCount(t *testing.T) {
	store, _ := newTestStore(t)
	operator := seedUser(t, store)
	ctx := context.Background()

	cat, err := store.CreateCategory(ctx, "待删分类", "")
	if err != nil {
		t.Fatalf("创建分类失败: %v", err)
	}
	for _, sku := range []string{"CAT-DEL-001", "CAT-DEL-002", "CAT-DEL-003"} {
		if _, err := store.CreateProduct(ctx, ProductInput{
			SKU:        sku,
			Name:       sku + "号商品",
			Unit:       "件",
			CategoryID: &cat.ID,
			Status:     models.ProductStatusActive,
		}, operator); err != nil {
			t.Fatalf("创建商品 %s 失败: %v", sku, err)
		}
	}

	affected, err := store.DeleteCategory(ctx, cat.ID)
	if err != nil {
		t.Fatalf("删除分类失败: %v", err)
	}
	if affected != 3 {
		t.Errorf("受影响商品数 = %d，期望 3", affected)
	}
}
