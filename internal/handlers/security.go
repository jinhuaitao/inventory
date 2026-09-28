package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"inventory/internal/models"
	"inventory/internal/services"
)

// SecurityQuestionsPage 展示并允许修改当前用户的安全问题。
//
// 安全问题直接决定能否自助找回密码，因此修改前必须验证当前密码。
func (h *Handler) SecurityQuestionsPage(w http.ResponseWriter, r *http.Request) {
	user := h.currentUser(r)

	questions, err := h.store.GetSecurityQuestions(r.Context(), user.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}

	values := map[string]string{}
	for _, q := range questions {
		values["question_"+strconv.Itoa(q.Position)] = q.Question
	}

	h.render(w, r, http.StatusOK, "security.html", "安全问题", "profile", map[string]any{
		"Values":       values,
		"Errors":       map[string]string{},
		"Count":        len(questions),
		"Required":     models.SecurityQuestionCount,
		"Configured":   len(questions) >= models.SecurityQuestionCount,
		"QuestionNos":  questionNumbers(),
		"QuestionMin":  services.SecurityQuestionMinLen,
		"AnswerMinLen": services.SecurityAnswerMinLen,
	})
}

// SecurityQuestionsUpdate 保存安全问题。
func (h *Handler) SecurityQuestionsUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	user := h.currentUser(r)
	current := r.FormValue("current_password")
	security := parseSecurityInputs(r)

	values := securityValues(security)
	errs := map[string]string{}

	if current == "" {
		errs["current_password"] = "请输入当前密码以确认身份"
	}
	for k, v := range securityErrors(services.ValidateSecurityAnswers(security)) {
		errs[k] = v
	}

	if len(errs) > 0 {
		h.renderSecurityForm(w, r, http.StatusUnprocessableEntity, values, errs)
		return
	}

	// 校验当前密码
	if !services.VerifyPassword(user.PasswordHash, current) {
		errs["current_password"] = "当前密码不正确"
		h.renderSecurityForm(w, r, http.StatusUnprocessableEntity, values, errs)
		return
	}

	if err := h.store.SetSecurityQuestions(r.Context(), user.ID, security); err != nil {
		if errors.Is(err, services.ErrInvalidInput) {
			for k, v := range securityErrors(err) {
				errs[k] = v
			}
		} else {
			errs["form"] = businessError(err)
		}
		h.renderSecurityForm(w, r, http.StatusUnprocessableEntity, values, errs)
		return
	}

	h.logger.Info("用户更新了安全问题", "用户", user.Username)
	h.redirectWith(w, r, "/profile/security", "success",
		"安全问题已更新，请牢记答案——它们是您找回密码的唯一凭据")
}

// renderSecurityForm 统一渲染安全问题表单。
func (h *Handler) renderSecurityForm(w http.ResponseWriter, r *http.Request, status int,
	values, errs map[string]string) {

	user := h.currentUser(r)
	count := 0
	if n, err := h.store.CountSecurityQuestions(r.Context(), user.ID); err == nil {
		count = n
	}

	h.render(w, r, status, "security.html", "安全问题", "profile", map[string]any{
		"Values":       values,
		"Errors":       errs,
		"Count":        count,
		"Required":     models.SecurityQuestionCount,
		"Configured":   count >= models.SecurityQuestionCount,
		"QuestionNos":  questionNumbers(),
		"QuestionMin":  services.SecurityQuestionMinLen,
		"AnswerMinLen": services.SecurityAnswerMinLen,
	})
}

// questionNumbers 返回 "1".."N"，供模板循环使用。
func questionNumbers() []string {
	out := make([]string, 0, models.SecurityQuestionCount)
	for i := 1; i <= models.SecurityQuestionCount; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return out
}
