package handlers

import (
	"net/http"
	"strconv"
	"time"

	"inventory/internal/utils"
)

// Reports 展示库存报表：出入库汇总、趋势、分类分布与商品排行。
func (h *Handler) Reports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().In(utils.DisplayZone)

	end := utils.Query(r, "end")
	start := utils.Query(r, "start")
	if end == "" {
		end = now.Format("2006-01-02")
	}
	if start == "" {
		start = now.AddDate(0, 0, -29).Format("2006-01-02")
	}

	summary, err := h.store.Summary(ctx, start, end)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	trend, err := h.store.MovementTrend(ctx, 30)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	categoryStats, err := h.store.CategoryStats(ctx, 10)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	topProducts, err := h.store.TopProductsByValue(ctx, 10)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	topMoving, err := h.store.TopMovingProducts(ctx, 30, 10)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	stats, err := h.store.DashboardStats(ctx)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	maxTrend := 0
	for _, p := range trend {
		if v := p.MaxValue(); v > maxTrend {
			maxTrend = v
		}
	}

	var categoryTotal float64
	for _, c := range categoryStats {
		categoryTotal += c.Value
	}

	h.render(w, r, http.StatusOK, "reports/index.html", "库存报表", "reports", map[string]any{
		"Summary":       summary,
		"Trend":         trend,
		"MaxTrend":      maxTrend,
		"CategoryStats": categoryStats,
		"CategoryTotal": categoryTotal,
		"TopProducts":   topProducts,
		"TopMoving":     topMoving,
		"Stats":         stats,
		"Start":         start,
		"End":           end,
	})
}

// itoa 是 strconv.Itoa 的简写。
func itoa(n int64) string { return strconv.FormatInt(n, 10) }
