package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"inventory/internal/auth"
	"inventory/internal/models"
	"inventory/internal/services"
)

// ---------------------------------------------------------------------------
// 预警页的统计数字
//
// 服务层已经有 CountLowStockBreakdown 的单测，但真正会被用户看到的是页面上的
// 两个数字。这里直接在渲染出来的 HTML 上断言，确保「谁把统计改回按当前页遍历」
// 这类改动在页面层就被挡住 —— 服务层单测绿、页面数字错，是最难发现的一种回归。
// ---------------------------------------------------------------------------

// lowStockStatRe 从预警页抓取三张统计卡上的数字。
var lowStockStatRe = map[string]*regexp.Regexp{
	"total": regexp.MustCompile(`预警商品</div>\s*<div class="stat-value">([0-9,]+)</div>`),
	"out":   regexp.MustCompile(`已缺货</div>\s*<div class="stat-value text-danger">([0-9,]+)</div>`),
	"low":   regexp.MustCompile(`库存偏低</div>\s*<div class="stat-value text-warning">([0-9,]+)</div>`),
}

// lowStockStatNumber 取出指定统计卡上的数字。
func lowStockStatNumber(t *testing.T, body, key string) int {
	t.Helper()

	m := lowStockStatRe[key].FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("页面中找不到 %q 统计卡，说明模板结构变了或页面没渲染出来", key)
	}
	n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	if err != nil {
		t.Fatalf("统计卡 %q 的数字无法解析: %q", key, m[1])
	}
	return n
}

// seedLowStockProducts 造出「超过一页」的预警商品，返回操作人 ID。
func seedLowStockProducts(t *testing.T, h *Handler, low, out int) int64 {
	t.Helper()

	ctx := context.Background()
	u, err := h.store.CreateUser(ctx, services.CreateUserInput{
		Username: "lowstock-operator",
		Email:    "lowstock@example.com",
		Password: "Passw0rd@123",
		Role:     models.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("创建操作人失败: %v", err)
	}

	create := func(sku string, qty int) {
		t.Helper()
		if _, err := h.store.CreateProduct(ctx, services.ProductInput{
			SKU:         sku,
			Name:        sku + "号商品",
			Unit:        "件",
			Quantity:    qty,
			SafetyStock: 10,
			Status:      models.ProductStatusActive,
		}, u.ID); err != nil {
			t.Fatalf("创建商品 %s 失败: %v", sku, err)
		}
	}

	for i := 0; i < low; i++ {
		create("PAGE-LOW-"+strconv.Itoa(i), 2)
	}
	for i := 0; i < out; i++ {
		create("PAGE-OUT-"+strconv.Itoa(i), 0)
	}
	// 一个库存充足的商品，不该出现在任何一类里
	create("PAGE-OK", 999)

	return u.ID
}

// TestLowStockPageStatsCoverAllPages 预警页顶部的数字必须覆盖全部预警商品。
//
// 早先这两个数字是遍历**当前页**的 products 数出来的：默认每页 20 条，
// 预警商品一旦超过一页，数字就只反映第一页的构成 —— 页面还能继续翻页、
// 数字却不再变化，用户只会觉得统计坏了，却怎么翻都找不到原因。
func TestLowStockPageStatsCoverAllPages(t *testing.T) {
	h := newRegistrationTestHandler(t, false)

	const lowCount = 25
	const outCount = 5
	seedLowStockProducts(t, h, lowCount, outCount)

	r := httptest.NewRequest(http.MethodGet, "/stock/low", nil)
	r = r.WithContext(auth.WithUser(r.Context(), testUser(models.RoleAdmin)))

	rec := httptest.NewRecorder()
	h.LowStockList(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("预警页应返回 200，实际 %d", rec.Code)
	}
	body := rec.Body.String()

	// 先确认这一页确实装不下全部预警商品，否则这个用例证明不了任何事
	rows := strings.Count(body, `class="cell-sub"`)
	if rows != 20 {
		t.Fatalf("第一页应渲染 20 行（默认每页 20），实际 %d 行；用例前提不成立", rows)
	}

	total := lowStockStatNumber(t, body, "total")
	out := lowStockStatNumber(t, body, "out")
	low := lowStockStatNumber(t, body, "low")

	if total != lowCount+outCount {
		t.Errorf("预警商品 = %d，期望 %d（列表总数）", total, lowCount+outCount)
	}
	if out != outCount {
		t.Errorf("已缺货 = %d，期望 %d —— 这个数字不能只统计第一页", out, outCount)
	}
	if low != lowCount {
		t.Errorf("库存偏低 = %d，期望 %d —— 这个数字不能只统计第一页", low, lowCount)
	}
	if out+low != total {
		t.Errorf("已缺货 %d + 库存偏低 %d = %d，与预警商品 %d 对不上", out, low, out+low, total)
	}
}

// TestLowStockPageStatsIgnoreArchived 归档商品不应出现在预警页的数字里。
//
// 归档意味着「这个商品已经不用再补货了」，把它算进预警会让数字永远降不下来。
func TestLowStockPageStatsIgnoreArchived(t *testing.T) {
	h := newRegistrationTestHandler(t, false)

	ctx := context.Background()
	u, err := h.store.CreateUser(ctx, services.CreateUserInput{
		Username: "arch-operator",
		Email:    "arch@example.com",
		Password: "Passw0rd@123",
		Role:     models.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("创建操作人失败: %v", err)
	}

	for _, in := range []services.ProductInput{
		{SKU: "ARCH-OUT", Name: "归档缺货", Unit: "件", Quantity: 0, SafetyStock: 10, Status: models.ProductStatusArchived},
		{SKU: "ARCH-LOW", Name: "归档偏低", Unit: "件", Quantity: 1, SafetyStock: 10, Status: models.ProductStatusArchived},
	} {
		if _, err := h.store.CreateProduct(ctx, in, u.ID); err != nil {
			t.Fatalf("创建归档商品 %s 失败: %v", in.SKU, err)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/stock/low", nil)
	r = r.WithContext(auth.WithUser(r.Context(), testUser(models.RoleAdmin)))

	rec := httptest.NewRecorder()
	h.LowStockList(rec, r)

	body := rec.Body.String()
	if got := lowStockStatNumber(t, body, "out"); got != 0 {
		t.Errorf("已缺货 = %d，期望 0（归档商品不计入预警）", got)
	}
	if got := lowStockStatNumber(t, body, "low"); got != 0 {
		t.Errorf("库存偏低 = %d，期望 0（归档商品不计入预警）", got)
	}
	if got := lowStockStatNumber(t, body, "total"); got != 0 {
		t.Errorf("预警商品 = %d，期望 0（归档商品不计入预警）", got)
	}
}
