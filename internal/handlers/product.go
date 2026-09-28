package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// productFormData 是商品新建 / 编辑页的数据。
type productFormData struct {
	Product    *models.Product
	Values     map[string]string
	Errors     map[string]string
	Categories []models.Category
	Suppliers  []models.Supplier
	IsEdit     bool
}

// productListData 是商品列表页的数据。
type productListData struct {
	Products   []models.Product
	Pagination *utils.Pagination
	Filter     services.ProductFilter
	Categories []models.Category
	Suppliers  []models.Supplier
	Query      string
}

// ---------------------------------------------------------------------------
// 列表
// ---------------------------------------------------------------------------

// ProductList 展示商品列表，支持搜索、筛选、排序与分页。
func (h *Handler) ProductList(w http.ResponseWriter, r *http.Request) {
	f := parseProductFilter(r)

	products, pg, err := h.store.ListProducts(r.Context(), f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	categories, err := h.store.ListAllCategories(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	suppliers, err := h.store.ListAllSuppliers(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "products/list.html", "商品管理", "products", &productListData{
		Products:   products,
		Pagination: pg,
		Filter:     f,
		Categories: categories,
		Suppliers:  suppliers,
		Query:      buildProductQuery(f),
	})
}

// parseProductFilter 从查询参数构造筛选条件。
func parseProductFilter(r *http.Request) services.ProductFilter {
	return services.ProductFilter{
		Search:       utils.Query(r, "q"),
		CategoryID:   int64(utils.ParseInt(utils.Query(r, "category"), 0)),
		SupplierID:   int64(utils.ParseInt(utils.Query(r, "supplier"), 0)),
		Status:       utils.Query(r, "status"),
		LowStockOnly: utils.Query(r, "low") == "1",
		SortBy:       utils.Query(r, "sort"),
		SortDir:      utils.Query(r, "dir"),
		Page:         utils.ParsePage(utils.Query(r, "page")),
		PerPage:      utils.ParsePerPage(utils.Query(r, "per_page")),
	}
}

// buildProductQuery 把筛选条件还原成查询串（固定顺序），供分页与导出链接复用。
func buildProductQuery(f services.ProductFilter) string {
	pairs := make([][2]string, 0, 8)
	add := func(k, v string) { pairs = append(pairs, [2]string{k, v}) }

	if f.Search != "" {
		add("q", f.Search)
	}
	if f.CategoryID > 0 {
		add("category", strconv.FormatInt(f.CategoryID, 10))
	}
	if f.SupplierID > 0 {
		add("supplier", strconv.FormatInt(f.SupplierID, 10))
	}
	if f.Status != "" {
		add("status", f.Status)
	}
	if f.LowStockOnly {
		add("low", "1")
	}
	if f.SortBy != "" {
		add("sort", f.SortBy)
	}
	if f.SortDir != "" {
		add("dir", f.SortDir)
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

// ---------------------------------------------------------------------------
// 详情
// ---------------------------------------------------------------------------

// ProductDetail 展示商品详情与最近流水。
func (h *Handler) ProductDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	product, err := h.store.GetProduct(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	movements, err := h.store.ListProductMovements(r.Context(), id, 20)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "products/detail.html", product.Name, "products", map[string]any{
		"Product":   product,
		"Movements": movements,
	})
}

// ---------------------------------------------------------------------------
// 新建
// ---------------------------------------------------------------------------

// ProductNew 渲染新建商品表单。
func (h *Handler) ProductNew(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	h.renderProductForm(w, r, http.StatusOK, false, nil, map[string]string{
		"unit":   "件",
		"status": models.ProductStatusActive,
	}, map[string]string{})
}

// ProductCreate 处理新建商品提交。
func (h *Handler) ProductCreate(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	in := productInputFromForm(r)
	values := formStringMap(r, "sku", "name", "barcode", "unit", "location", "description", "status")
	values["cost_price"] = formString(r, "cost_price")
	values["sale_price"] = formString(r, "sale_price")
	values["quantity"] = formString(r, "quantity")
	values["safety_stock"] = formString(r, "safety_stock")

	user := h.currentUser(r)
	product, err := h.store.CreateProduct(r.Context(), in, user.ID)
	if err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrInvalidInput) {
			errs["form"] = err.Error()
		}
		if errors.Is(err, services.ErrConflict) {
			errs["sku"] = err.Error()
			delete(errs, "form")
		}
		h.renderProductForm(w, r, http.StatusUnprocessableEntity, false, nil, values, errs)
		return
	}

	h.redirectWith(w, r, "/products/"+strconv.FormatInt(product.ID, 10), "success",
		"商品「"+product.Name+"」创建成功")
}

// ---------------------------------------------------------------------------
// 编辑
// ---------------------------------------------------------------------------

// ProductEdit 渲染编辑商品表单。
func (h *Handler) ProductEdit(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	product, err := h.store.GetProduct(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	h.renderProductForm(w, r, http.StatusOK, true, product, valuesFromProduct(product), map[string]string{})
}

// ProductUpdate 处理编辑提交。库存数量不在此处修改。
func (h *Handler) ProductUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	product, err := h.store.GetProduct(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	in := productInputFromForm(r)
	values := valuesFromProduct(product)
	for k, v := range formStringMap(r, "sku", "name", "barcode", "unit", "location", "description", "status") {
		values[k] = v
	}
	values["cost_price"] = formString(r, "cost_price")
	values["sale_price"] = formString(r, "sale_price")
	values["safety_stock"] = formString(r, "safety_stock")
	values["category_id"] = formString(r, "category_id")
	values["supplier_id"] = formString(r, "supplier_id")

	if err := h.store.UpdateProduct(r.Context(), id, in); err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrInvalidInput) {
			errs["form"] = err.Error()
		}
		if errors.Is(err, services.ErrConflict) {
			errs["sku"] = err.Error()
			delete(errs, "form")
		}
		h.renderProductForm(w, r, http.StatusUnprocessableEntity, true, product, values, errs)
		return
	}

	h.redirectWith(w, r, "/products/"+strconv.FormatInt(id, 10), "success", "商品信息已更新")
}

// ProductStatus 归档或恢复商品。
func (h *Handler) ProductStatus(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	status := formString(r, "status")
	action := "已更新状态"
	if status == models.ProductStatusArchived {
		action = "已归档"
	} else {
		action = "已恢复为在售"
	}

	if err := h.store.SetProductStatus(r.Context(), id, status); err != nil {
		h.redirectWith(w, r, "/products/"+strconv.FormatInt(id, 10), "error", businessError(err))
		return
	}
	h.redirectWith(w, r, "/products/"+strconv.FormatInt(id, 10), "success", "商品"+action)
}

// ProductDelete 删除商品（存在流水时会被拒绝）。
func (h *Handler) ProductDelete(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	if err := h.store.DeleteProduct(r.Context(), id); err != nil {
		h.redirectWith(w, r, "/products/"+strconv.FormatInt(id, 10), "error", businessError(err))
		return
	}
	h.redirectWith(w, r, "/products", "success", "商品已删除")
}

// ---------------------------------------------------------------------------
// 导出
// ---------------------------------------------------------------------------

// ProductExport 导出符合条件的商品为 CSV。
func (h *Handler) ProductExport(w http.ResponseWriter, r *http.Request) {
	f := parseProductFilter(r)
	f.Page, f.PerPage = 1, 200

	products, err := h.store.ListProductsForExport(r.Context(), f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	header := []string{
		"SKU", "商品名称", "条码", "分类", "供应商", "单位",
		"进价", "售价", "当前库存", "安全库存", "库存金额", "库位", "状态", "更新时间",
	}
	rows := make([][]string, 0, len(products))
	for _, p := range products {
		status := "在售"
		if p.Status == models.ProductStatusArchived {
			status = "已归档"
		}
		rows = append(rows, []string{
			p.SKU, p.Name, p.Barcode,
			utils.DefaultString(p.CategoryName, "未分类"),
			utils.DefaultString(p.SupplierName, "-"),
			p.Unit,
			utils.FormatFloat(p.CostPrice, 2),
			utils.FormatFloat(p.SalePrice, 2),
			strconv.Itoa(p.Quantity),
			strconv.Itoa(p.SafetyStock),
			utils.FormatFloat(p.StockValue(), 2),
			p.Location,
			status,
			utils.FormatDateTime(p.UpdatedAt),
		})
	}

	if err := utils.WriteCSV(w, utils.TimestampedFilename("商品列表"), header, rows); err != nil {
		h.logger.Error("导出商品 CSV 失败", "错误", err)
	}
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// renderProductForm 渲染商品表单页。
func (h *Handler) renderProductForm(w http.ResponseWriter, r *http.Request, status int, isEdit bool, product *models.Product, values, errs map[string]string) {
	categories, err := h.store.ListAllCategories(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	suppliers, err := h.store.ListAllSuppliers(r.Context())
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	title := "新建商品"
	if isEdit {
		title = "编辑商品"
	}

	h.render(w, r, status, "products/form.html", title, "products", &productFormData{
		Product:    product,
		Values:     values,
		Errors:     errs,
		Categories: categories,
		Suppliers:  suppliers,
		IsEdit:     isEdit,
	})
}

// productInputFromForm 从表单构造商品入参。
func productInputFromForm(r *http.Request) services.ProductInput {
	return services.ProductInput{
		SKU:         formString(r, "sku"),
		Name:        formString(r, "name"),
		Barcode:     formString(r, "barcode"),
		CategoryID:  formInt64Ptr(r, "category_id"),
		SupplierID:  formInt64Ptr(r, "supplier_id"),
		Unit:        formString(r, "unit"),
		CostPrice:   formFloat(r, "cost_price", 0),
		SalePrice:   formFloat(r, "sale_price", 0),
		Quantity:    formInt(r, "quantity", 0),
		SafetyStock: formInt(r, "safety_stock", 0),
		Location:    formString(r, "location"),
		Description: formString(r, "description"),
		Status:      formString(r, "status"),
	}
}

// valuesFromProduct 把商品实体转换为表单回填值。
func valuesFromProduct(p *models.Product) map[string]string {
	m := map[string]string{
		"sku":          p.SKU,
		"name":         p.Name,
		"barcode":      p.Barcode,
		"unit":         p.Unit,
		"cost_price":   utils.FormatFloat(p.CostPrice, 2),
		"sale_price":   utils.FormatFloat(p.SalePrice, 2),
		"quantity":     strconv.Itoa(p.Quantity),
		"safety_stock": strconv.Itoa(p.SafetyStock),
		"location":     p.Location,
		"description":  p.Description,
		"status":       p.Status,
	}
	if p.CategoryID != nil {
		m["category_id"] = strconv.FormatInt(*p.CategoryID, 10)
	}
	if p.SupplierID != nil {
		m["supplier_id"] = strconv.FormatInt(*p.SupplierID, 10)
	}
	return m
}

// pathID 读取路径参数 {id}。
func pathID(r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
