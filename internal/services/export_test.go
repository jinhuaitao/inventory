package services

import (
	"context"
	"testing"

	"inventory/internal/models"
)

// ---------------------------------------------------------------------------
// 导出截断必须可见
//
// 早先 ListMovementsForExport 写死 `LIMIT 50000` 且不返回任何提示，
// 导出的 CSV 悄悄少了数据，使用者还以为自己拿到了全量 ——
// 再拿这份残缺数据去对账，后果比直接报错严重得多。
// 下面的用例锁死「截断必须被如实报告」这条约定。
// ---------------------------------------------------------------------------

// seedMovements 写入 n 条入库流水，返回商品 ID。
func seedMovements(t *testing.T, store *Store, n int) int64 {
	t.Helper()

	ctx := context.Background()
	operator := seedUser(t, store)
	product := seedProductWithoutMovements(t, store, operator, "EXP-001")

	for i := 0; i < n; i++ {
		if _, err := store.ApplyMovement(ctx, models.MovementIn, MovementInput{
			ProductID:  product.ID,
			Quantity:   1,
			UnitPrice:  1,
			OperatorID: operator,
		}); err != nil {
			t.Fatalf("写入第 %d 条流水失败: %v", i+1, err)
		}
	}
	return product.ID
}

// TestListMovementsForExportReportsTruncation 超过上限时必须报告截断。
func TestListMovementsForExportReportsTruncation(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	seedMovements(t, store, 5)

	const limit = 3
	movements, truncated, err := store.listMovementsForExport(ctx, MovementFilter{}, limit)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if !truncated {
		t.Error("结果被截断时 truncated 必须为 true，否则调用方无法告知用户")
	}
	if len(movements) != limit {
		t.Errorf("返回条数 = %d，期望 %d", len(movements), limit)
	}
}

// TestListMovementsForExportExactlyAtLimit 恰好等于上限时不算截断。
//
// 这正是「多取一行」存在的理由：只看 len == limit 无法区分
// 「恰好这么多」与「后面还有更多」，会把完好的导出误报成残缺的。
func TestListMovementsForExportExactlyAtLimit(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	seedMovements(t, store, 3)

	movements, truncated, err := store.listMovementsForExport(ctx, MovementFilter{}, 3)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if truncated {
		t.Error("条数恰好等于上限时不应报告截断")
	}
	if len(movements) != 3 {
		t.Errorf("返回条数 = %d，期望 3", len(movements))
	}
}

// TestListMovementsForExportUnderLimit 未达上限时返回全部且不报告截断。
func TestListMovementsForExportUnderLimit(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	seedMovements(t, store, 2)

	movements, truncated, err := store.listMovementsForExport(ctx, MovementFilter{}, 10)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if truncated {
		t.Error("未达上限时不应报告截断")
	}
	if len(movements) != 2 {
		t.Errorf("返回条数 = %d，期望 2", len(movements))
	}
}

// TestListMovementsForExportHonoursFilter 截断不应破坏筛选条件。
func TestListMovementsForExportHonoursFilter(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	productID := seedMovements(t, store, 4)

	// 按商品筛选应命中全部 4 条
	movements, truncated, err := store.listMovementsForExport(ctx,
		MovementFilter{ProductID: productID}, 100)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if truncated {
		t.Error("未达上限时不应报告截断")
	}
	if len(movements) != 4 {
		t.Errorf("返回条数 = %d，期望 4", len(movements))
	}

	// 筛选一个不存在的商品应为空
	movements, _, err = store.listMovementsForExport(ctx,
		MovementFilter{ProductID: 987654}, 100)
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if len(movements) != 0 {
		t.Errorf("筛选不存在的商品应返回 0 条，实际 %d", len(movements))
	}
}
