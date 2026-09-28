package handlers

import (
	"net/http"
)

// Dashboard 渲染首页仪表盘，汇总库存核心指标与近期动态。
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stats, err := h.store.DashboardStats(ctx)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	trend, err := h.store.MovementTrend(ctx, 14)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	categoryStats, err := h.store.CategoryStats(ctx, 8)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	topProducts, err := h.store.TopProductsByValue(ctx, 8)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	topMoving, err := h.store.TopMovingProducts(ctx, 30, 8)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	lowStock, err := h.store.ListLowStockProducts(ctx, 8)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	recent, err := h.store.RecentMovements(ctx, 10)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	// 趋势图纵轴上限
	maxTrend := 0
	for _, p := range trend {
		if v := p.MaxValue(); v > maxTrend {
			maxTrend = v
		}
	}

	// 分类占比图的分母
	var categoryTotal float64
	for _, c := range categoryStats {
		categoryTotal += c.Value
	}

	h.render(w, r, http.StatusOK, "dashboard.html", "仪表盘", "dashboard", map[string]any{
		"Stats":         stats,
		"Trend":         trend,
		"MaxTrend":      maxTrend,
		"CategoryStats": categoryStats,
		"CategoryTotal": categoryTotal,
		"TopProducts":   topProducts,
		"TopMoving":     topMoving,
		"LowStock":      lowStock,
		"Recent":        recent,
	})
}
