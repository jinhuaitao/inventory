package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// categoryFormData 是分类表单页的数据。
type categoryFormData struct {
	Category *models.Category
	Values   map[string]string
	Errors   map[string]string
	IsEdit   bool
}

// CategoryList 展示分类列表。
func (h *Handler) CategoryList(w http.ResponseWriter, r *http.Request) {
	f := services.CategoryFilter{
		Search:  utils.Query(r, "q"),
		Page:    utils.ParsePage(utils.Query(r, "page")),
		PerPage: utils.ParsePerPage(utils.Query(r, "per_page")),
	}

	categories, pg, err := h.store.ListCategories(r.Context(), f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "categories/list.html", "分类管理", "categories", map[string]any{
		"Categories": categories,
		"Pagination": pg,
		"Search":     f.Search,
		"Query":      currentQueryWithoutPage(r),
	})
}

// CategoryNew 渲染新建分类表单。
func (h *Handler) CategoryNew(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	h.render(w, r, http.StatusOK, "categories/form.html", "新建分类", "categories", &categoryFormData{
		Values: map[string]string{},
		Errors: map[string]string{},
	})
}

// CategoryCreate 处理新建分类提交。
func (h *Handler) CategoryCreate(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	values := formStringMap(r, "name", "description")
	if _, err := h.store.CreateCategory(r.Context(), values["name"], values["description"]); err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrConflict) {
			errs["name"] = err.Error()
			delete(errs, "form")
		}
		if errors.Is(err, services.ErrInvalidInput) {
			errs["name"] = err.Error()
			delete(errs, "form")
		}
		h.render(w, r, http.StatusUnprocessableEntity, "categories/form.html", "新建分类", "categories", &categoryFormData{
			Values: values,
			Errors: errs,
		})
		return
	}

	h.redirectWith(w, r, "/categories", "success", "分类「"+values["name"]+"」创建成功")
}

// CategoryEdit 渲染编辑分类表单。
func (h *Handler) CategoryEdit(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	category, err := h.store.GetCategory(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "categories/form.html", "编辑分类", "categories", &categoryFormData{
		Category: category,
		Values: map[string]string{
			"name":        category.Name,
			"description": category.Description,
		},
		Errors: map[string]string{},
		IsEdit: true,
	})
}

// CategoryUpdate 处理编辑提交。
func (h *Handler) CategoryUpdate(w http.ResponseWriter, r *http.Request) {
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

	values := formStringMap(r, "name", "description")
	if err := h.store.UpdateCategory(r.Context(), id, values["name"], values["description"]); err != nil {
		errs := map[string]string{"form": businessError(err)}
		if errors.Is(err, services.ErrConflict) || errors.Is(err, services.ErrInvalidInput) {
			errs["name"] = err.Error()
			delete(errs, "form")
		}
		category, _ := h.store.GetCategory(r.Context(), id)
		h.render(w, r, http.StatusUnprocessableEntity, "categories/form.html", "编辑分类", "categories", &categoryFormData{
			Category: category,
			Values:   values,
			Errors:   errs,
			IsEdit:   true,
		})
		return
	}

	h.redirectWith(w, r, "/categories", "success", "分类已更新")
}

// CategoryDelete 删除分类。
func (h *Handler) CategoryDelete(w http.ResponseWriter, r *http.Request) {
	if !h.canWrite(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	affected, err := h.store.DeleteCategory(r.Context(), id)
	if err != nil {
		h.redirectWith(w, r, "/categories", "error", businessError(err))
		return
	}

	msg := "分类已删除"
	if affected > 0 {
		msg = "分类已删除，" + strconv.FormatInt(affected, 10) + " 个商品的分类已被清空"
	}
	h.redirectWith(w, r, "/categories", "success", msg)
}
