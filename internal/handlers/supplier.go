package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// supplierFormData 是供应商表单页的数据。
type supplierFormData struct {
	Supplier *models.Supplier
	Values   map[string]string
	Errors   map[string]string
	IsEdit   bool
}

// SupplierList 展示供应商列表。
func (h *Handler) SupplierList(w http.ResponseWriter, r *http.Request) {
	f := services.SupplierFilter{
		Search:  utils.Query(r, "q"),
		Page:    utils.ParsePage(utils.Query(r, "page")),
		PerPage: utils.ParsePerPage(utils.Query(r, "per_page")),
	}

	suppliers, pg, err := h.store.ListSuppliers(r.Context(), f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "suppliers/list.html", "供应商管理", "suppliers", map[string]any{
		"Suppliers":  suppliers,
		"Pagination": pg,
		"Search":     f.Search,
		"Query":      currentQueryWithoutPage(r),
	})
}

// SupplierDetail 展示供应商详情及其商品。
func (h *Handler) SupplierDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	supplier, err := h.store.GetSupplier(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	products, _, err := h.store.ListProducts(r.Context(), services.ProductFilter{
		SupplierID: id,
		Page:       1,
		PerPage:    50,
		SortBy:     "name",
		SortDir:    "asc",
	})
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "suppliers/detail.html", supplier.Name, "suppliers", map[string]any{
		"Supplier": supplier,
		"Products": products,
	})
}

// SupplierNew 渲染新建供应商表单。
func (h *Handler) SupplierNew(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	h.render(w, r, http.StatusOK, "suppliers/form.html", "新建供应商", "suppliers", &supplierFormData{
		Values: map[string]string{},
		Errors: map[string]string{},
	})
}

// SupplierCreate 处理新建供应商提交。
func (h *Handler) SupplierCreate(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	in := supplierInputFromForm(r)
	values := supplierValues(r)

	created, err := h.store.CreateSupplier(r.Context(), in)
	if err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrConflict) {
			errs["name"] = err.Error()
			delete(errs, "form")
		}
		if errors.Is(err, services.ErrInvalidInput) {
			errs["form"] = err.Error()
		}
		h.render(w, r, http.StatusUnprocessableEntity, "suppliers/form.html", "新建供应商", "suppliers", &supplierFormData{
			Values: values,
			Errors: errs,
		})
		return
	}

	h.redirectWith(w, r, "/suppliers/"+strconv.FormatInt(created.ID, 10), "success",
		"供应商「"+created.Name+"」创建成功")
}

// SupplierEdit 渲染编辑供应商表单。
func (h *Handler) SupplierEdit(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	supplier, err := h.store.GetSupplier(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "suppliers/form.html", "编辑供应商", "suppliers", &supplierFormData{
		Supplier: supplier,
		Values: map[string]string{
			"name":           supplier.Name,
			"contact_person": supplier.ContactPerson,
			"phone":          supplier.Phone,
			"email":          supplier.Email,
			"address":        supplier.Address,
			"note":           supplier.Note,
		},
		Errors: map[string]string{},
		IsEdit: true,
	})
}

// SupplierUpdate 处理编辑提交。
func (h *Handler) SupplierUpdate(w http.ResponseWriter, r *http.Request) {
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

	in := supplierInputFromForm(r)
	values := supplierValues(r)

	if err := h.store.UpdateSupplier(r.Context(), id, in); err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrConflict) {
			errs["name"] = err.Error()
			delete(errs, "form")
		}
		supplier, _ := h.store.GetSupplier(r.Context(), id)
		h.render(w, r, http.StatusUnprocessableEntity, "suppliers/form.html", "编辑供应商", "suppliers", &supplierFormData{
			Supplier: supplier,
			Values:   values,
			Errors:   errs,
			IsEdit:   true,
		})
		return
	}

	h.redirectWith(w, r, "/suppliers/"+strconv.FormatInt(id, 10), "success", "供应商信息已更新")
}

// SupplierDelete 删除供应商。
func (h *Handler) SupplierDelete(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	affected, err := h.store.DeleteSupplier(r.Context(), id)
	if err != nil {
		h.redirectWith(w, r, "/suppliers", "error", businessError(err))
		return
	}

	msg := "供应商已删除"
	if affected > 0 {
		msg = "供应商已删除，" + strconv.FormatInt(affected, 10) + " 个商品的供应商信息已被清空"
	}
	h.redirectWith(w, r, "/suppliers", "success", msg)
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func supplierInputFromForm(r *http.Request) services.SupplierInput {
	return services.SupplierInput{
		Name:          formString(r, "name"),
		ContactPerson: formString(r, "contact_person"),
		Phone:         formString(r, "phone"),
		Email:         formString(r, "email"),
		Address:       formString(r, "address"),
		Note:          formString(r, "note"),
	}
}

func supplierValues(r *http.Request) map[string]string {
	return formStringMap(r, "name", "contact_person", "phone", "email", "address", "note")
}
