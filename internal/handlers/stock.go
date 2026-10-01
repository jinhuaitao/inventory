package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// ---------------------------------------------------------------------------
// 表单页
// ---------------------------------------------------------------------------

// StockInPage 渲染入库表单。
func (h *Handler) StockInPage(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	preset := int64(utils.ParseInt(utils.Query(r, "product_id"), 0))
	h.renderStockForm(w, r, http.StatusOK, models.MovementIn, preset,
		map[string]string{"quantity": "1"}, map[string]string{})
}

// StockOutPage 渲染出库表单。
func (h *Handler) StockOutPage(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	preset := int64(utils.ParseInt(utils.Query(r, "product_id"), 0))
	h.renderStockForm(w, r, http.StatusOK, models.MovementOut, preset,
		map[string]string{"quantity": "1"}, map[string]string{})
}

// StockAdjustPage 渲染盘点表单。
func (h *Handler) StockAdjustPage(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	preset := int64(utils.ParseInt(utils.Query(r, "product_id"), 0))
	h.renderStockForm(w, r, http.StatusOK, models.MovementAdjust, preset,
		map[string]string{}, map[string]string{})
}

// renderStockForm 渲染出入库 / 盘点共用的表单页。
func (h *Handler) renderStockForm(
	w http.ResponseWriter, r *http.Request, status int,
	typ models.MovementType, preset int64, values, errs map[string]string,
) {
	products, _, err := h.store.ListProducts(r.Context(), services.ProductFilter{
		Page: 1, PerPage: 200, SortBy: "name", SortDir: "asc",
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	suppliers, err := h.store.ListAllSuppliers(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	var tmpl, title, active string
	switch typ {
	case models.MovementIn:
		tmpl, title, active = "stock/in.html", "商品入库", "stock-in"
	case models.MovementOut:
		tmpl, title, active = "stock/out.html", "商品出库", "stock-out"
	default:
		tmpl, title, active = "stock/adjust.html", "库存盘点", "stock-adjust"
	}

	h.render(w, r, status, tmpl, title, active, map[string]any{
		"Products":  products,
		"Suppliers": suppliers,
		"Values":    values,
		"Errors":    errs,
		"PresetID":  preset,
	})
}

// ---------------------------------------------------------------------------
// 提交
// ---------------------------------------------------------------------------

// StockInSubmit 处理入库提交。
func (h *Handler) StockInSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	user := h.currentUser(r)
	in := services.MovementInput{
		Quantity:   formInt(r, "quantity", 0),
		UnitPrice:  formFloat(r, "unit_price", 0),
		RefNo:      formString(r, "ref_no"),
		SupplierID: formInt64Ptr(r, "supplier_id"),
		OperatorID: user.ID,
		Note:       formString(r, "note"),
	}
	if pid := formInt64Ptr(r, "product_id"); pid != nil {
		in.ProductID = *pid
	}

	values := stockFormValues(r)
	movement, err := h.store.ApplyMovement(r.Context(), models.MovementIn, in)
	if err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrInvalidInput) || errors.Is(err, services.ErrNotFound) {
			errs["form"] = err.Error()
		}
		h.renderStockForm(w, r, http.StatusUnprocessableEntity, models.MovementIn, in.ProductID, values, errs)
		return
	}

	h.logger.Info("商品入库", "商品", movement.ProductName, "数量", movement.Quantity, "操作人", user.Username)
	h.redirectWith(w, r, "/stock/movements", "success",
		fmt.Sprintf("入库成功：%s +%d %s，当前库存 %d",
			movement.ProductName, movement.Quantity, utils.DefaultString(movement.ProductUnit, "件"), movement.AfterQty))
}

// StockOutSubmit 处理出库提交。
func (h *Handler) StockOutSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	user := h.currentUser(r)
	in := services.MovementInput{
		Quantity:   formInt(r, "quantity", 0),
		UnitPrice:  formFloat(r, "unit_price", 0),
		RefNo:      formString(r, "ref_no"),
		OperatorID: user.ID,
		Note:       formString(r, "note"),
	}
	if pid := formInt64Ptr(r, "product_id"); pid != nil {
		in.ProductID = *pid
	}

	values := stockFormValues(r)
	movement, err := h.store.ApplyMovement(r.Context(), models.MovementOut, in)
	if err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrInvalidInput) ||
			errors.Is(err, services.ErrNotFound) ||
			errors.Is(err, services.ErrInsufficientStock) {
			errs["form"] = err.Error()
		}
		h.renderStockForm(w, r, http.StatusUnprocessableEntity, models.MovementOut, in.ProductID, values, errs)
		return
	}

	h.logger.Info("商品出库", "商品", movement.ProductName, "数量", movement.Quantity, "操作人", user.Username)
	h.redirectWith(w, r, "/stock/movements", "success",
		fmt.Sprintf("出库成功：%s -%d %s，当前库存 %d",
			movement.ProductName, movement.Quantity, utils.DefaultString(movement.ProductUnit, "件"), movement.AfterQty))
}

// StockAdjustSubmit 处理盘点提交。
func (h *Handler) StockAdjustSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	user := h.currentUser(r)
	var productID int64
	if pid := formInt64Ptr(r, "product_id"); pid != nil {
		productID = *pid
	}
	targetQty := formInt(r, "actual_quantity", -1)
	note := formString(r, "note")

	values := stockFormValues(r)

	if productID <= 0 || targetQty < 0 {
		errs := map[string]string{"form": "请选择商品并填写实际库存数量（不能为负数）"}
		h.renderStockForm(w, r, http.StatusUnprocessableEntity, models.MovementAdjust, productID, values, errs)
		return
	}

	movement, err := h.store.AdjustStock(r.Context(), productID, targetQty, note, user.ID)
	if err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrInvalidInput) || errors.Is(err, services.ErrNotFound) {
			errs["form"] = err.Error()
		}
		h.renderStockForm(w, r, http.StatusUnprocessableEntity, models.MovementAdjust, productID, values, errs)
		return
	}

	h.logger.Info("库存盘点", "商品", movement.ProductName,
		"调整前", movement.BeforeQty, "调整后", movement.AfterQty, "操作人", user.Username)

	diff := movement.Delta
	var msg string
	switch {
	case diff > 0:
		msg = fmt.Sprintf("盘点完成：%s 库存 %d → %d（盘盈 +%d）",
			movement.ProductName, movement.BeforeQty, movement.AfterQty, diff)
	case diff < 0:
		msg = fmt.Sprintf("盘点完成：%s 库存 %d → %d（盘亏 %d）",
			movement.ProductName, movement.BeforeQty, movement.AfterQty, diff)
	default:
		msg = fmt.Sprintf("盘点完成：%s 库存无差异（%d）", movement.ProductName, movement.AfterQty)
	}
	h.redirectWith(w, r, "/stock/movements", "success", msg)
}

// stockFormValues 收集表单值用于校验失败回填。
func stockFormValues(r *http.Request) map[string]string {
	v := formStringMap(r, "quantity", "unit_price", "ref_no", "note", "actual_quantity")
	v["product_id"] = formString(r, "product_id")
	v["supplier_id"] = formString(r, "supplier_id")
	return v
}

// ---------------------------------------------------------------------------
// 流水列表
// ---------------------------------------------------------------------------

// MovementList 展示库存流水。
func (h *Handler) MovementList(w http.ResponseWriter, r *http.Request) {
	f := parseMovementFilter(r)

	movements, pg, err := h.store.ListMovements(r.Context(), f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	products, _, err := h.store.ListProducts(r.Context(), services.ProductFilter{
		Page: 1, PerPage: 200, SortBy: "name", SortDir: "asc",
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "stock/movements.html", "库存流水", "movements", map[string]any{
		"Movements":  movements,
		"Pagination": pg,
		"Filter":     f,
		"Products":   products,
		"Types":      models.AllMovementTypes(),
		"Query":      buildMovementQuery(f),
	})
}

// parseMovementFilter 解析流水筛选条件。
func parseMovementFilter(r *http.Request) services.MovementFilter {
	return services.MovementFilter{
		Keyword:   utils.Query(r, "q"),
		Type:      utils.Query(r, "type"),
		ProductID: int64(utils.ParseInt(utils.Query(r, "product"), 0)),
		StartDate: utils.Query(r, "start"),
		EndDate:   utils.Query(r, "end"),
		Page:      utils.ParsePage(utils.Query(r, "page")),
		PerPage:   utils.ParsePerPage(utils.Query(r, "per_page")),
	}
}

// buildMovementQuery 还原查询串。
func buildMovementQuery(f services.MovementFilter) string {
	pairs := make([][2]string, 0, 8)
	add := func(k, v string) { pairs = append(pairs, [2]string{k, v}) }

	if f.Keyword != "" {
		add("q", f.Keyword)
	}
	if f.Type != "" {
		add("type", f.Type)
	}
	if f.ProductID > 0 {
		add("product", strconv.FormatInt(f.ProductID, 10))
	}
	if f.StartDate != "" {
		add("start", f.StartDate)
	}
	if f.EndDate != "" {
		add("end", f.EndDate)
	}
	if f.PerPage > 0 && f.PerPage != 20 {
		add("per_page", strconv.Itoa(f.PerPage))
	}

	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, p[0]+"="+url.QueryEscape(p[1]))
	}
	return strings.Join(parts, "&")
}

// MovementExport 导出流水为 CSV。
func (h *Handler) MovementExport(w http.ResponseWriter, r *http.Request) {
	f := parseMovementFilter(r)

	movements, truncated, err := h.store.ListMovementsForExport(r.Context(), f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	header := []string{
		"时间", "类型", "商品名称", "SKU", "变动数量", "变动前", "变动后",
		"单价", "金额", "单据号", "供应商", "操作人", "备注",
	}
	rows := make([][]string, 0, len(movements)+1)
	for _, m := range movements {
		rows = append(rows, []string{
			utils.FormatDateTime(m.CreatedAt),
			m.Type.Label(),
			m.ProductName,
			m.ProductSKU,
			m.SignedQuantity(),
			strconv.Itoa(m.BeforeQty),
			strconv.Itoa(m.AfterQty),
			utils.FormatFloat(m.UnitPrice, 2),
			utils.FormatFloat(m.Amount(), 2),
			m.RefNo,
			utils.DefaultString(m.SupplierName, "-"),
			m.OperatorName,
			m.Note,
		})
	}

	// 被行数上限截断时，必须把这个事实写进**文件本身**。
	// 导出走的是直接下载，页面上没有地方提示；只记一条日志的话，
	// 用户拿着缺了一截的 CSV 去对账，问题只会更大。
	if truncated {
		rows = append(rows, []string{fmt.Sprintf(
			"注意：符合条件的流水超过 %d 条上限，本文件仅包含最近 %d 条，请收窄时间范围后重新导出",
			services.MaxExportRows, services.MaxExportRows)})
		h.logger.Warn("流水导出被行数上限截断",
			"上限", services.MaxExportRows,
			"已导出", len(movements),
			"开始日期", f.StartDate,
			"结束日期", f.EndDate,
		)
	}

	if err := utils.WriteCSV(w, utils.TimestampedFilename("库存流水"), header, rows); err != nil {
		h.logger.Error("导出流水 CSV 失败", "错误", err)
	}
}

// ---------------------------------------------------------------------------
// 低库存
// ---------------------------------------------------------------------------

// LowStockList 展示库存预警列表。
func (h *Handler) LowStockList(w http.ResponseWriter, r *http.Request) {
	f := services.ProductFilter{
		LowStockOnly: true,
		SortBy:       "quantity",
		SortDir:      "asc",
		Page:         utils.ParsePage(utils.Query(r, "page")),
		PerPage:      utils.ParsePerPage(utils.Query(r, "per_page")),
	}

	products, pg, err := h.store.ListProducts(r.Context(), f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	// 顶部两个数字必须覆盖**全部**预警商品，因此走独立的聚合查询。
	// 早先这里是遍历当前页的 products 来数，分页后数出来的只是「本页」的构成：
	// 预警商品一旦超过一页（默认 20 条），这两个数字就会比实际小，
	// 而且列表还能翻页、数字却不再变，用户只会觉得统计坏了。
	outOfStock, lowStock, err := h.store.CountLowStockBreakdown(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "stock/low.html", "库存预警", "low-stock", map[string]any{
		"Products":   products,
		"Pagination": pg,
		"OutOfStock": outOfStock,
		"LowStock":   lowStock,
		"Query":      currentQueryWithoutPage(r),
	})
}
