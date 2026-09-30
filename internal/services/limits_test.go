package services

import (
	"context"
	"errors"
	"testing"

	"inventory/internal/models"
)

// 本文件集中验证数量上限（MaxQuantity）。
//
// 为什么值得单独测：上限不只用于「挡住多按几个 0 的误输入」，它还是
// `before_qty + delta` 不溢出 int64 的保证。一旦某条写数量的入口漏掉了
// 这个检查，溢出会让 after_qty 变成负数，库存流水与商品实际库存就永久
// 对不上了 —— 追溯能力被破坏，而且从界面上完全看不出来。
// 因此「出入库、盘点、商品期初库存」三条入口都必须各有一个用例。

// TestApplyMovementRejectsQuantityAboveMax 出入库入口的上限校验。
func TestApplyMovementRejectsQuantityAboveMax(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	product, operatorID := newTestProduct(t, store, 100)

	_, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID:  product.ID,
		Quantity:   MaxQuantity + 1,
		UnitPrice:  10,
		OperatorID: operatorID,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超过上限的入库应返回 ErrInvalidInput，实际 %v", err)
	}

	// 失败必须不留痕：库存与流水都不能变。
	got, err := store.GetProduct(ctx, product.ID)
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if got.Quantity != 100 {
		t.Errorf("被拒绝的入库不应改变库存，实际 %d", got.Quantity)
	}
	movements, err := store.ListProductMovements(ctx, product.ID, 10)
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if len(movements) != 1 {
		t.Errorf("被拒绝的入库不应写入流水，实际 %d 条", len(movements))
	}
}

// TestApplyMovementAcceptsQuantityAtMax 边界：恰好等于上限应当放行。
// 上限是「不能超过」，不是「不能达到」。
func TestApplyMovementAcceptsQuantityAtMax(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	product, operatorID := newTestProduct(t, store, 0)

	mv, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID:  product.ID,
		Quantity:   MaxQuantity,
		UnitPrice:  1,
		OperatorID: operatorID,
	})
	if err != nil {
		t.Fatalf("恰好等于上限的入库应被接受，实际报错: %v", err)
	}
	if mv.AfterQty != MaxQuantity {
		t.Errorf("结存 = %d, 期望 %d", mv.AfterQty, MaxQuantity)
	}
}

// TestAdjustStockRejectsTargetAboveMax 盘点入口的上限校验。
func TestAdjustStockRejectsTargetAboveMax(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	product, operatorID := newTestProduct(t, store, 100)

	_, err := store.AdjustStock(ctx, product.ID, MaxQuantity+1, "盘点", operatorID)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超过上限的盘点应返回 ErrInvalidInput，实际 %v", err)
	}

	got, err := store.GetProduct(ctx, product.ID)
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if got.Quantity != 100 {
		t.Errorf("被拒绝的盘点不应改变库存，实际 %d", got.Quantity)
	}
}

// TestAdjustStockAcceptsTargetAtMax 盘点入口的边界。
func TestAdjustStockAcceptsTargetAtMax(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	product, operatorID := newTestProduct(t, store, 100)

	mv, err := store.AdjustStock(ctx, product.ID, MaxQuantity, "盘点", operatorID)
	if err != nil {
		t.Fatalf("恰好等于上限的盘点应被接受，实际报错: %v", err)
	}
	if mv.AfterQty != MaxQuantity {
		t.Errorf("结存 = %d, 期望 %d", mv.AfterQty, MaxQuantity)
	}
}

// TestCreateProductRejectsInitialQuantityAboveMax 期初库存入口的上限校验。
//
// 这条最容易被忽略：它绕过了 ApplyMovement，走的是 ProductInput.normalize，
// 所以必须单独有检查，否则「新建商品时填个超大期初库存」就成了绕过上限的后门。
func TestCreateProductRejectsInitialQuantityAboveMax(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operatorID := seedUser(t, store)

	_, err := store.CreateProduct(ctx, ProductInput{
		SKU:         "T-BIG",
		Name:        "超大期初库存",
		Unit:        "个",
		CostPrice:   1,
		SalePrice:   2,
		Quantity:    MaxQuantity + 1,
		SafetyStock: 0,
		Status:      models.ProductStatusActive,
	}, operatorID)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("超过上限的期初库存应返回 ErrInvalidInput，实际 %v", err)
	}

	// 商品必须没有被创建 —— 否则会留下一个库存异常、又没有期初流水的脏数据。
	if _, err := store.GetProductBySKU(ctx, "T-BIG"); !errors.Is(err, ErrNotFound) {
		t.Errorf("被拒绝的商品不应落库，查询返回 %v", err)
	}
}

// TestCreateProductAcceptsInitialQuantityAtMax 期初库存的边界。
func TestCreateProductAcceptsInitialQuantityAtMax(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operatorID := seedUser(t, store)

	product, err := store.CreateProduct(ctx, ProductInput{
		SKU:         "T-MAX",
		Name:        "满额期初库存",
		Unit:        "个",
		CostPrice:   1,
		SalePrice:   2,
		Quantity:    MaxQuantity,
		SafetyStock: 0,
		Status:      models.ProductStatusActive,
	}, operatorID)
	if err != nil {
		t.Fatalf("恰好等于上限的期初库存应被接受，实际报错: %v", err)
	}
	if product.Quantity != MaxQuantity {
		t.Errorf("期初库存 = %d, 期望 %d", product.Quantity, MaxQuantity)
	}
}

// TestApplyMovementRejectsCumulativeOverflow 上限必须卡在**结存**上。
//
// 只校验单次输入的话，连续入库可以把库存一路累加突破上限 ——
// 每次都「合法」，加起来却能溢出 int64，after_qty 变成负数，
// 流水与商品库存从此对不上。因此这里分两次入库，第二次必须被拒绝。
func TestApplyMovementRejectsCumulativeOverflow(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	product, operatorID := newTestProduct(t, store, 0)

	// 第一次顶到上限，合法
	if _, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID:  product.ID,
		Quantity:   MaxQuantity,
		UnitPrice:  1,
		OperatorID: operatorID,
	}); err != nil {
		t.Fatalf("顶到上限的入库应被接受，实际报错: %v", err)
	}

	// 第二次再加 1 就会越界，必须拒绝
	_, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
		ProductID:  product.ID,
		Quantity:   1,
		UnitPrice:  1,
		OperatorID: operatorID,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("累加后越界的入库应返回 ErrInvalidInput，实际 %v", err)
	}

	// 库存必须停在第一次入库后的值，且没有多出流水
	got, err := store.GetProduct(ctx, product.ID)
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if got.Quantity != MaxQuantity {
		t.Errorf("被拒绝的入库不应改变库存，实际 %d，期望 %d", got.Quantity, MaxQuantity)
	}
	movements, err := store.ListProductMovements(ctx, product.ID, 10)
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if len(movements) != 1 {
		t.Errorf("被拒绝的入库不应写入流水，实际 %d 条", len(movements))
	}
}

// TestCreateProductRejectsMissingOperator 缺少操作人时必须给出可读的错误。
//
// 期初库存大于 0 时 operatorID 会写进 stock_movements.operator_id，
// 而该列是 NOT NULL REFERENCES users(id)。不提前校验的话，
// 用户看到的会是一句裸的「constraint failed」，整笔建商品操作回滚，
// 却完全不知道是自己哪个字段填错了。
func TestCreateProductRejectsMissingOperator(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	_, err := store.CreateProduct(ctx, ProductInput{
		SKU:      "T-NO-OP",
		Name:     "缺操作人的商品",
		Unit:     "个",
		Quantity: 5,
		Status:   models.ProductStatusActive,
	}, 0)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("缺少操作人应返回 ErrInvalidInput，实际 %v", err)
	}

	// 不能留下半成品
	if _, err := store.GetProductBySKU(ctx, "T-NO-OP"); !errors.Is(err, ErrNotFound) {
		t.Errorf("被拒绝的商品不应落库，查询返回 %v", err)
	}
}

// TestAdjustStockRejectsNoChange 盘点数等于当前库存时不应写入空流水。
//
// 一条 delta=0 的流水既非盘盈也非盘亏，对追溯库存变动没有任何信息量，
// 却会因为误点「盘点」而在流水表里不断堆积噪音。
func TestAdjustStockRejectsNoChange(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	product, operatorID := newTestProduct(t, store, 42)

	_, err := store.AdjustStock(ctx, product.ID, 42, "盘点", operatorID)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("盘点数未变化时应返回 ErrInvalidInput，实际 %v", err)
	}

	movements, err := store.ListProductMovements(ctx, product.ID, 10)
	if err != nil {
		t.Fatalf("查询流水失败: %v", err)
	}
	if len(movements) != 1 {
		t.Errorf("无变化的盘点不应写入流水，实际 %d 条（应只有建商品时的期初流水）", len(movements))
	}
}

// TestAdjustStockAcceptsRealChange 确认正常盘点不受上面的改动影响。
func TestAdjustStockAcceptsRealChange(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	product, operatorID := newTestProduct(t, store, 42)

	mv, err := store.AdjustStock(ctx, product.ID, 40, "盘点", operatorID)
	if err != nil {
		t.Fatalf("有实际差异的盘点不应失败: %v", err)
	}
	if mv.Delta != -2 || mv.AfterQty != 40 {
		t.Errorf("盘点结果 = delta %d / 结存 %d，期望 -2 / 40", mv.Delta, mv.AfterQty)
	}
}
