package handlers

import (
	"errors"
	"net/http"
	"strings"

	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// userFormData 是用户表单页的数据。
type userFormData struct {
	Target *models.User
	Values map[string]string
	Errors map[string]string
	Roles  []models.Role
	IsEdit bool
}

// UserList 展示用户列表（仅管理员）。
func (h *Handler) UserList(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	f := services.UserFilter{
		Search:  utils.Query(r, "q"),
		Role:    utils.Query(r, "role"),
		Status:  utils.Query(r, "status"),
		Page:    utils.ParsePage(utils.Query(r, "page")),
		PerPage: utils.ParsePerPage(utils.Query(r, "per_page")),
	}

	users, pg, err := h.store.ListUsers(r.Context(), f)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "users/list.html", "用户管理", "users", map[string]any{
		"Users":        users,
		"Pagination":   pg,
		"Filter":       f,
		"Roles":        models.AllRoles(),
		"Registration": h.registrationPageData(),
		"Query":        currentQueryWithoutPage(r),
	})
}

// UserNew 渲染新建用户表单。
func (h *Handler) UserNew(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}
	h.render(w, r, http.StatusOK, "users/form.html", "新建用户", "users", &userFormData{
		Values: map[string]string{"role": string(models.RoleViewer)},
		Errors: map[string]string{},
		Roles:  models.AllRoles(),
	})
}

// UserCreate 处理新建用户提交。
func (h *Handler) UserCreate(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	username := formString(r, "username")
	email := formString(r, "email")
	fullName := formString(r, "full_name")
	password := r.FormValue("password")
	role := models.Role(formString(r, "role"))

	values := map[string]string{
		"username":  username,
		"email":     email,
		"full_name": fullName,
		"role":      string(role),
	}
	errs := map[string]string{}

	if !utils.IsValidUsername(username) {
		errs["username"] = "用户名需为 3-32 位字母、数字、下划线或连字符"
	}
	if !utils.IsValidEmail(email) {
		errs["email"] = "请输入有效的邮箱地址"
	}
	if err := utils.ValidatePassword(password); err != nil {
		errs["password"] = err.Error()
	}
	if !role.Valid() {
		errs["role"] = "请选择有效的角色"
	}

	if len(errs) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, "users/form.html", "新建用户", "users", &userFormData{
			Values: values, Errors: errs, Roles: models.AllRoles(),
		})
		return
	}

	created, err := h.store.CreateUser(r.Context(), services.CreateUserInput{
		Username: username,
		Email:    email,
		Password: password,
		FullName: fullName,
		Role:     role,
	})
	if err != nil {
		errs["form"] = businessError(err)
		if errors.Is(err, services.ErrConflict) {
			if strings.Contains(err.Error(), "用户名") {
				errs["username"] = err.Error()
			} else {
				errs["email"] = err.Error()
			}
			delete(errs, "form")
		}
		h.render(w, r, http.StatusUnprocessableEntity, "users/form.html", "新建用户", "users", &userFormData{
			Values: values, Errors: errs, Roles: models.AllRoles(),
		})
		return
	}

	h.logger.Info("管理员创建了用户", "新用户", created.Username, "角色", created.Role.Label(),
		"操作人", h.currentUser(r).Username)
	h.redirectWith(w, r, usersPath, "success", "用户「"+created.Username+"」创建成功")
}

// UserEdit 渲染编辑用户表单。
func (h *Handler) UserEdit(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	target, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	h.render(w, r, http.StatusOK, "users/form.html", "编辑用户", "users", &userFormData{
		Target: target,
		Values: map[string]string{
			"username":  target.Username,
			"email":     target.Email,
			"full_name": target.FullName,
			"role":      string(target.Role),
			"status":    target.Status,
		},
		Errors: map[string]string{},
		Roles:  models.AllRoles(),
		IsEdit: true,
	})
}

// UserUpdate 处理编辑提交。
func (h *Handler) UserUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
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

	target, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			h.notFound(w, r)
			return
		}
		h.serverError(w, r, err)
		return
	}

	current := h.currentUser(r)
	role := models.Role(formString(r, "role"))
	status := formString(r, "status")
	if status == "" {
		status = models.UserStatusActive
	}
	email := formString(r, "email")
	fullName := formString(r, "full_name")
	password := r.FormValue("password")

	values := map[string]string{
		"username":  target.Username,
		"email":     email,
		"full_name": fullName,
		"role":      string(role),
		"status":    status,
	}
	errs := map[string]string{}

	if !utils.IsValidEmail(email) {
		errs["email"] = "请输入有效的邮箱地址"
	}
	if !role.Valid() {
		errs["role"] = "请选择有效的角色"
	}
	if password != "" {
		if err := utils.ValidatePassword(password); err != nil {
			errs["password"] = err.Error()
		}
	}
	if id == current.ID && (role != models.RoleAdmin || status != models.UserStatusActive) {
		errs["form"] = "不能修改自己的角色或禁用自己的账号"
	}

	if len(errs) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, "users/form.html", "编辑用户", "users", &userFormData{
			Target: target, Values: values, Errors: errs, Roles: models.AllRoles(), IsEdit: true,
		})
		return
	}

	err = h.store.UpdateUserByAdmin(r.Context(), id, services.AdminUpdateUserInput{
		FullName: fullName,
		Email:    email,
		Role:     role,
		Status:   status,
		Password: password,
	})
	if err != nil {
		errs["form"] = businessError(err)
		if errors.Is(err, services.ErrConflict) {
			errs["email"] = err.Error()
			delete(errs, "form")
		}
		h.render(w, r, http.StatusUnprocessableEntity, "users/form.html", "编辑用户", "users", &userFormData{
			Target: target, Values: values, Errors: errs, Roles: models.AllRoles(), IsEdit: true,
		})
		return
	}

	h.logger.Info("管理员更新了用户", "目标用户", target.Username, "操作人", current.Username)
	h.redirectWith(w, r, usersPath, "success", "用户「"+target.Username+"」已更新")
}

// UserDelete 删除用户。
func (h *Handler) UserDelete(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, r)
		return
	}

	current := h.currentUser(r)
	if id == current.ID {
		h.redirectWith(w, r, usersPath, "error", "不能删除自己的账号")
		return
	}

	target, err := h.store.GetUserByID(r.Context(), id)
	if err != nil {
		h.redirectWith(w, r, usersPath, "error", businessError(err))
		return
	}

	if err := h.store.DeleteUser(r.Context(), id); err != nil {
		h.redirectWith(w, r, usersPath, "error", businessError(err))
		return
	}

	h.logger.Info("管理员删除了用户", "目标用户", target.Username, "操作人", current.Username)
	h.redirectWith(w, r, usersPath, "success", "用户「"+target.Username+"」已删除")
}
