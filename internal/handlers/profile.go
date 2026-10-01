package handlers

import (
	"errors"
	"net/http"
	"strings"

	"inventory/internal/auth"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// profileData 组装个人资料页所需的全部上下文。
func (h *Handler) profileData(r *http.Request, values, errs map[string]string) map[string]any {
	user := h.currentUser(r)

	// 附属信息取不到时不再伪装成「无设备 / 未设置安全问题」——
	// 那会把数据库故障粉饰成正常状态，用户与运维都被误导。
	// 出错记 Warn，页面上的空态同时附上「可能未刷新成功」提示。
	sessions, err := h.store.ListUserSessions(r.Context(), user.ID)
	if err != nil {
		h.logger.Warn("加载登录设备列表失败", "错误", err)
		sessions = nil
		errs = mergeProfileNotice(errs, "部分信息加载失败，请刷新页面重试")
	}

	var currentID int64
	if cur := auth.SessionFrom(r.Context()); cur != nil {
		currentID = cur.ID
	}

	securityConfigured := false
	if ok, err := h.store.HasSecurityQuestions(r.Context(), user.ID); err == nil {
		securityConfigured = ok
	} else {
		h.logger.Warn("检查安全问题设置状态失败", "错误", err)
		errs = mergeProfileNotice(errs, "部分信息加载失败，请刷新页面重试")
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

// mergeProfileNotice 在不覆盖既有表单错误的前提下补一条页面级提示，
// 让「附属信息加载失败」对用户可见，而不是静默显示成空数据。
func mergeProfileNotice(errs map[string]string, msg string) map[string]string {
	if errs == nil {
		errs = map[string]string{}
	}
	if _, ok := errs["form"]; !ok {
		errs["form"] = msg
	}
	return errs
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
//
// 姓名可以随意改；**邮箱不行** —— 它是账号的唯一标识与找回密码的通道，
// 属于敏感的身份变更，必须做一次「再验证」（要求重新输入当前密码）。
//
// 否则只要会话被窃取（XSS、未锁屏的设备、被复制走的 cookie），
// 攻击者改掉邮箱就等于把找回通道指向自己，随后完成账号接管 ——
// 而受害者连一条通知都收不到。站内「修改安全问题」已经有这个要求，
// 改邮箱的敏感度相当，不应例外。
func (h *Handler) ProfileUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	user := h.currentUser(r)
	fullName := formString(r, "full_name")
	email := formString(r, "email")
	current := r.FormValue("current_password")

	values := map[string]string{"full_name": fullName, "email": email}
	errs := map[string]string{}
	if !utils.IsValidEmail(email) {
		errs["email"] = "请输入有效的邮箱地址"
	}

	// 只有邮箱确实发生变化时才要求密码 —— 避免改个姓名也要输密码的无谓摩擦。
	emailChanged := !strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(user.Email))
	if emailChanged && current == "" {
		errs["current_password"] = "修改邮箱需要输入当前密码"
	}

	if len(errs) > 0 {
		h.render(w, r, http.StatusUnprocessableEntity, "profile.html", "个人资料", "profile",
			h.profileData(r, values, errs))
		return
	}

	// bcrypt 校验放在最后：先做完廉价的格式检查，避免无意义的计算开销。
	if emailChanged && !services.VerifyPassword(user.PasswordHash, current) {
		h.logger.Warn("修改邮箱时当前密码校验失败", "用户", user.Username)
		errs["current_password"] = "当前密码不正确"
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

	if emailChanged {
		h.logger.Info("用户修改了邮箱", "用户", user.Username, "新邮箱", email)
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
