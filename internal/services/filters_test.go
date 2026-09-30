package services

import (
	"context"
	"fmt"
	"testing"

	"inventory/internal/models"
)

// ---------------------------------------------------------------------------
// 筛选口径一致性
//
// 这一组用例针对的都是「同一件事在两个地方算法不同」的问题：
// 界面上的数字对不上列表条数、下拉里选了「全部」却看不到全部 ——
// 这类缺陷不会报错，只会让人对系统失去信任。
// ---------------------------------------------------------------------------

// createProductWithStock 建一个带指定库存 / 安全库存 / 状态的商品。
func createProductWithStock(t *testing.T, store *Store, operator int64, sku string, qty, safety int, status string) {
	t.Helper()

	if _, err := store.CreateProduct(context.Background(), ProductInput{
		SKU:         sku,
		Name:        sku + "号商品",
		Unit:        "件",
		Quantity:    qty,
		SafetyStock: safety,
		Status:      status,
	}, operator); err != nil {
		t.Fatalf("创建商品 %s 失败: %v", sku, err)
	}
}

// TestLowStockCountMatchesWarningList 仪表盘的低库存徽标必须与预警列表条数一致。
//
// 早先仪表盘统计多了一个 quantity > 0，把缺货商品排除在计数之外，
// 而预警列表却把它们算进来 —— 于是徽标显示 1、点进去却有 2 条，
// 用户只会怀疑「是不是有一半数据没加载出来」。
//
// 缺货是「低库存」里更严重的一档，两者是包含关系：
// 徽标 = 预警列表条数，缺货单独用 OutOfStockCount 呈现。
func TestLowStockCountMatchesWarningList(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)

	createProductWithStock(t, store, operator, "LS-OUT", 0, 5, models.ProductStatusActive)   // 缺货
	createProductWithStock(t, store, operator, "LS-LOW", 3, 5, models.ProductStatusActive)   // 有货但偏低
	createProductWithStock(t, store, operator, "LS-OK", 50, 5, models.ProductStatusActive)   // 正常
	createProductWithStock(t, store, operator, "LS-BOUND", 5, 5, models.ProductStatusActive) // 恰好等于安全库存

	stats, err := store.DashboardStats(ctx)
	if err != nil {
		t.Fatalf("统计仪表盘失败: %v", err)
	}

	_, page, err := store.ListProducts(ctx, ProductFilter{
		LowStockOnly: true, Page: 1, PerPage: 20,
	})
	if err != nil {
		t.Fatalf("查询预警列表失败: %v", err)
	}

	if page.Total != 3 {
		t.Errorf("预警列表条数 = %d，期望 3（缺货 1 + 偏低 1 + 恰好等于安全库存 1）", page.Total)
	}
	if stats.LowStockCount != page.Total {
		t.Errorf("仪表盘低库存数 = %d，与预警列表条数 %d 不一致",
			stats.LowStockCount, page.Total)
	}
	if stats.OutOfStockCount != 1 {
		t.Errorf("缺货数 = %d，期望 1", stats.OutOfStockCount)
	}
}

// TestLowStockListIncludesOutOfStock 缺货商品必须出现在预警列表里。
//
// 库存为 0 却不在「库存预警」中显示，是最容易酿成实际缺料事故的漏网之鱼。
func TestLowStockListIncludesOutOfStock(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)

	createProductWithStock(t, store, operator, "WARN-OUT", 0, 10, models.ProductStatusActive)

	products, err := store.ListLowStockProducts(ctx, 10)
	if err != nil {
		t.Fatalf("查询低库存商品失败: %v", err)
	}
	if len(products) != 1 || products[0].SKU != "WARN-OUT" {
		t.Fatalf("缺货商品应出现在低库存列表里，实际 %+v", products)
	}
}

// TestProductFilterStatusAllIncludesArchived 下拉里的「全部状态」必须真的返回全部。
//
// 早先 "" 与 "all" 共用同一个分支，都会追加 status != 'archived'，
// 于是用户选了「全部状态」反而筛不出归档商品 ——
// 一个叫「全部」却少给结果的筛选项，比根本没有这个选项更容易误导人。
func TestProductFilterStatusAllIncludesArchived(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)

	createProductWithStock(t, store, operator, "ST-ACTIVE", 1, 0, models.ProductStatusActive)
	createProductWithStock(t, store, operator, "ST-ARCHIVED", 1, 0, models.ProductStatusArchived)

	cases := []struct {
		name   string
		status string
		want   int
	}{
		{"默认视图隐藏归档", "", 1},
		{"全部状态包含归档", "all", 2},
		{"精确筛选在售", models.ProductStatusActive, 1},
		{"精确筛选归档", models.ProductStatusArchived, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, page, err := store.ListProducts(ctx, ProductFilter{
				Status: tc.status, Page: 1, PerPage: 20,
			})
			if err != nil {
				t.Fatalf("查询商品失败: %v", err)
			}
			if page.Total != tc.want {
				t.Errorf("status=%q 时商品数 = %d，期望 %d", tc.status, page.Total, tc.want)
			}
		})
	}
}

// TestProductFilterStatusAllMatchesDashboardTotal 「全部状态」应与仪表盘的统计口径对上。
//
// 仪表盘的 TotalProducts 用的就是「非归档」，两者本该一致；
// 这个用例把这条隐含约定固定下来，免得将来改了一处忘了另一处。
func TestProductFilterStatusAllMatchesDashboardTotal(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)

	createProductWithStock(t, store, operator, "DASH-1", 1, 0, models.ProductStatusActive)
	createProductWithStock(t, store, operator, "DASH-2", 1, 0, models.ProductStatusActive)
	createProductWithStock(t, store, operator, "DASH-3", 1, 0, models.ProductStatusArchived)

	stats, err := store.DashboardStats(ctx)
	if err != nil {
		t.Fatalf("统计仪表盘失败: %v", err)
	}
	if stats.TotalProducts != 2 {
		t.Errorf("仪表盘在售商品数 = %d，期望 2（不含归档）", stats.TotalProducts)
	}

	_, page, err := store.ListProducts(ctx, ProductFilter{Status: "all", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("查询商品失败: %v", err)
	}
	if page.Total != 3 {
		t.Errorf("「全部状态」商品数 = %d，期望 3", page.Total)
	}
}

// TestCountLowStockBreakdownCoversAllPages 预警构成统计必须覆盖全部商品，而不是当前页。
//
// 早先「已缺货 / 库存偏低」是遍历当前页的商品数出来的：预警商品一旦超过
// 一页（默认 20 条），两个数字就只反映第一页的构成，而且列表还能继续翻页、
// 数字却不再变化 —— 用户只会觉得统计坏了，却找不到原因。
func TestCountLowStockBreakdownCoversAllPages(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)

	// 刻意超过一页：25 条偏低 + 5 条缺货 = 30 条预警
	const lowCount = 25
	const outCount = 5

	for i := 0; i < lowCount; i++ {
		createProductWithStock(t, store, operator,
			fmt.Sprintf("BRK-LOW-%03d", i), 2, 10, models.ProductStatusActive)
	}
	for i := 0; i < outCount; i++ {
		createProductWithStock(t, store, operator,
			fmt.Sprintf("BRK-OUT-%03d", i), 0, 10, models.ProductStatusActive)
	}
	// 一个正常商品，不应被算进任何一类
	createProductWithStock(t, store, operator, "BRK-OK-001", 999, 10, models.ProductStatusActive)

	outOfStock, lowStock, err := store.CountLowStockBreakdown(ctx)
	if err != nil {
		t.Fatalf("统计预警构成失败: %v", err)
	}
	if outOfStock != outCount {
		t.Errorf("已缺货 = %d，期望 %d（不能只统计第一页）", outOfStock, outCount)
	}
	if lowStock != lowCount {
		t.Errorf("库存偏低 = %d，期望 %d（不能只统计第一页）", lowStock, lowCount)
	}

	// 与预警列表的总条数必须严丝合缝
	_, page, err := store.ListProducts(ctx, ProductFilter{
		LowStockOnly: true, Page: 1, PerPage: 20,
	})
	if err != nil {
		t.Fatalf("查询预警列表失败: %v", err)
	}
	if outOfStock+lowStock != page.Total {
		t.Errorf("已缺货 %d + 库存偏低 %d = %d，与预警列表总条数 %d 不一致",
			outOfStock, lowStock, outOfStock+lowStock, page.Total)
	}
}

// TestCountLowStockBreakdownExcludesArchived 归档商品不应计入预警统计。
func TestCountLowStockBreakdownExcludesArchived(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	operator := seedUser(t, store)

	createProductWithStock(t, store, operator, "BRK-ARCH-1", 0, 10, models.ProductStatusArchived)
	createProductWithStock(t, store, operator, "BRK-ARCH-2", 1, 10, models.ProductStatusArchived)

	outOfStock, lowStock, err := store.CountLowStockBreakdown(ctx)
	if err != nil {
		t.Fatalf("统计预警构成失败: %v", err)
	}
	if outOfStock != 0 || lowStock != 0 {
		t.Errorf("归档商品不应计入预警：已缺货 %d、偏低 %d，期望均为 0", outOfStock, lowStock)
	}
}
