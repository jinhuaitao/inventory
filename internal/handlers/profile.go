package handlers

import (
	"errors"
	"net/http"

	"inventory/internal/auth"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// profileData 组装个人资料页所需的全部上下文。
func (h *Handler) profileData(r *http.Request, values, errs map[string]string) map[string]any {
	user := h.currentUser(r)

	sessions, err := h.store.ListUserSessions(r.Context(), user.ID)
	if err != nil {
		sessions = nil
	}

	var currentID int64
	if cur := auth.SessionFrom(r.Context()); cur != nil {
		currentID = cur.ID
	}

	securityConfigured := false
	if ok, err := h.store.HasSecurityQuestions(r.Context(), user.ID); err == nil {
		securityConfigured = ok
	}

	return map[string]any{
		"Values":             values,
		"Errors":             errs,
		"Sessions":           sessions,
		"CurrentSessionID":   currentID,
		"SessionCount":       len(sessions),
		"SecurityConfigured": securityConfigured,
	}
}

// ProfilePage 展示个人资料与登录设备。
func (h *Handler) ProfilePage(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)

	data := h.profileData(r, map[string]string{
		"full_name": user.FullName,
		"email":     user.Email,
	}, map[string]string{})

	h.render(w, r, http.StatusOK, "profile.html", "个人资料", "profile", data)
}

// ProfileUpdate 更新当前用户的资料。
func (h *Handler) ProfileUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	user := h.currentUser(r)
	fullName := formString(r, "full_name")
	email := formString(r, "email")

	values := map[string]string{"full_name": fullName, "email": email}
	errs := map[string]string{}
	if !utils.IsValidEmail(email) {
		errs["email"] = "请输入有效的邮箱地址"
	}

	if len(errs) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, "profile.html", "个人资料", "profile",
			h.profileData(r, values, errs))
		return
	}

	if err := h.store.UpdateProfile(r.Context(), user.ID, fullName, email); err != nil {
		errs["form"] = businessError(err)
		if errors.Is(err, services.ErrConflict) {
			errs["email"] = err.Error()
			delete(errs, "form")
		}
		h.render(w, r, http.StatusUnprocessableEntity, "profile.html", "个人资料", "profile",
			h.profileData(r, values, errs))
		return
	}

	h.redirectWith(w, r, "/profile", "success", "个人资料已更新")
}

// PasswordPage 展示修改密码表单。
func (h *Handler) PasswordPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "password.html", "修改密码", "profile", map[string]any{
		"Errors":       map[string]string{},
		"Strength":     0,
		"StrengthText": "",
	})
}

// PasswordUpdate 处理修改密码提交。
func (h *Handler) PasswordUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	user := h.currentUser(r)
	current := r.FormValue("current_password")
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	strength, strengthText := utils.PasswordStrength(password)
	errs := map[string]string{}

	if current == "" {
		errs["current_password"] = "请输入当前密码"
	}
	if err := utils.ValidatePassword(password); err != nil {
		errs["password"] = err.Error()
	}
	if password != confirm {
		errs["password_confirm"] = "两次输入的密码不一致"
	}
	if current != "" && password != "" && current == password {
		errs["password"] = "新密码不能与当前密码相同"
	}

	if len(errs) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, "password.html", "修改密码", "profile", map[string]any{
			"Errors": errs, "Strength": strength, "StrengthText": strengthText,
		})
		return
	}

	if err := h.store.ChangePassword(r.Context(), user.ID, current, password); err != nil {
		if errors.Is(err, services.ErrInvalidCredentials) {
			errs["current_password"] = "当前密码不正确"
		} else {
			errs["form"] = businessError(err)
		}
		h.render(w, r, http.StatusUnprocessableEntity, "password.html", "修改密码", "profile", map[string]any{
			"Errors": errs, "Strength": strength, "StrengthText": strengthText,
		})
		return
	}

	// 保留当前设备会话，踢掉其它设备
	var keepHash string
	if cur := auth.SessionFrom(r.Context()); cur != nil {
		keepHash = cur.TokenHash
	}
	if _, err := h.store.DeleteSessionsByUser(r.Context(), user.ID, keepHash); err != nil {
		h.logger.Warn("清理其它会话失败", "错误", err)
	}

	h.logger.Info("用户修改了密码", "用户", user.Username)
	h.redirectWith(w, r, "/profile", "success", "密码修改成功，其它设备已被强制退出")
}

// SessionsPage 展示登录设备列表。
func (h *Handler) SessionsPage(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)

	sessions, err := h.store.ListUserSessions(r.Context(), user.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	var currentID int64
	if cur := auth.SessionFrom(r.Context()); cur != nil {
		currentID = cur.ID
	}

	h.render(w, r, http.StatusOK, "sessions.html", "登录设备", "profile", map[string]any{
		"Sessions":         sessions,
		"CurrentSessionID": currentID,
	})
}

// RevokeSession 注销指定的其它设备。
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	id := int64(formInt(r, "session_id", 0))
	if id <= 0 {
		h.redirectWith(w, r, "/profile/sessions", "error", "参数不正确")
		return
	}

	user := h.currentUser(r)
	if cur := auth.SessionFrom(r.Context()); cur != nil && cur.ID == id {
		h.redirectWith(w, r, "/profile/sessions", "warning", "当前设备请使用「退出登录」")
		return
	}

	if err := h.store.DeleteSessionByID(r.Context(), id, user.ID); err != nil {
		h.redirectWith(w, r, "/profile/sessions", "error", businessError(err))
		return
	}
	h.redirectWith(w, r, "/profile/sessions", "success", "该设备已退出登录")
}

// RevokeAllSessions 注销除当前设备外的全部会话。
func (h *Handler) RevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)

	var keepHash string
	if cur := auth.SessionFrom(r.Context()); cur != nil {
		keepHash = cur.TokenHash
	}

	n, err := h.store.DeleteSessionsByUser(r.Context(), user.ID, keepHash)
	if err != nil {
		h.redirectWith(w, r, "/profile/sessions", "error", businessError(err))
		return
	}

	if n == 0 {
		h.redirectWith(w, r, "/profile/sessions", "info", "没有其它登录设备需要退出")
		return
	}
	h.redirectWith(w, r, "/profile/sessions", "success", "已退出其它 "+itoa(n)+" 个设备")
}
