package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// ---------------------------------------------------------------------------
// 登录
// ---------------------------------------------------------------------------

// LoginPage 渲染登录页。
func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	h.renderLogin(w, r, http.StatusOK,
		map[string]string{},
		map[string]string{},
		r.URL.Query().Get("next"),
	)
}

// LoginSubmit 处理登录表单。
func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	identifier := formString(r, "identifier")
	password := r.FormValue("password")
	remember := formBool(r, "remember")
	next := sanitizeNext(formString(r, "next"))

	values := map[string]string{"identifier": identifier}
	errs := map[string]string{}
	if identifier == "" {
		errs["identifier"] = "请输入用户名或邮箱"
	}
	if password == "" {
		errs["password"] = "请输入密码"
	}
	if len(errs) > 0 {
		h.renderLogin(w, r, http.StatusUnprocessableEntity, values, errs, next)
		return
	}

	ctx := r.Context()
	ip := utils.ClientIP(r, h.cfg.TrustedProxies)

	// 失败次数限制，抵御暴力破解
	since := time.Now().Add(-h.cfg.LockoutWindow)
	locked, err := h.loginLocked(ctx, identifier, ip, since)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if locked {
		// 只留痕、不计入失败次数：否则攻击者持续发请求即可让锁定窗口
		// 无限顺延，把别人的账号永久锁死。
		if err := h.store.RecordBlockedLoginAttempt(ctx, identifier, ip); err != nil {
			h.logger.Warn("记录被限流的登录尝试失败", "错误", err)
		}
		h.logger.Warn("登录已被限流", "标识", identifier, "IP", ip)
		minutes := int(h.cfg.LockoutWindow.Minutes())
		h.renderLogin(w, r, http.StatusTooManyRequests, values, map[string]string{
			"form": fmt.Sprintf("登录尝试过于频繁，请 %d 分钟后再试", minutes),
		}, next)
		return
	}

	user, err := h.store.GetUserByIdentifier(ctx, identifier)
	if err != nil && !errors.Is(err, services.ErrNotFound) {
		h.serverError(w, r, err)
		return
	}

	// 账号不存在时也要做一次等量 bcrypt 计算：否则「查无此人」比「密码错误」
	// 快几十毫秒，攻击者靠计时差异就能枚举出存在的用户名。
	passwordOK := user != nil && services.VerifyPassword(user.PasswordHash, password)
	if user == nil {
		services.BurnPasswordCheck(password)
	}

	if !passwordOK {
		if err := h.store.RecordLoginAttempt(ctx, identifier, ip, false); err != nil {
			h.logger.Warn("记录登录尝试失败", "错误", err)
		}
		h.renderLogin(w, r, http.StatusUnauthorized, values, map[string]string{
			"form": "用户名或密码错误",
		}, next)
		return
	}

	if !user.IsActive() {
		if err := h.store.RecordLoginAttempt(ctx, identifier, ip, false); err != nil {
			h.logger.Warn("记录登录尝试失败", "错误", err)
		}
		h.renderLogin(w, r, http.StatusForbidden, values, map[string]string{
			"form": "该账号已被禁用，请联系管理员",
		}, next)
		return
	}

	if err := h.store.RecordLoginAttempt(ctx, identifier, ip, true); err != nil {
		h.logger.Warn("记录登录尝试失败", "错误", err)
	}
	if err := h.store.ClearFailedAttempts(ctx, identifier); err != nil {
		h.logger.Warn("清理登录失败记录失败", "错误", err)
	}

	if _, err := h.sessions.Create(ctx, w, r, user, remember); err != nil {
		h.serverError(w, r, err)
		return
	}
	if err := h.store.TouchLastLogin(ctx, user.ID); err != nil {
		h.logger.Warn("更新登录时间失败", "错误", err)
	}

	target := "/"
	if next != "" {
		target = next
	}
	h.redirectWith(w, r, target, "success", "欢迎回来，"+user.DisplayName()+"！")
}

// identifierLockoutMultiplier 是「不限来源、仅按标识」这一兜底维度的阈值倍数。
//
// 单看某个标识的失败总数就锁定，是最容易被滥用的一种实现：攻击者无需任何
// 凭据，只要知道受害者的用户名，每 15 分钟发 5 次错误密码就能让对方永远
// 登不进来。因此这里把它降级为**兜底**，阈值放大到主力维度的 10 倍，
// 专门用来对付「大量 IP 各试几次」的分布式爆破 —— 那种攻击下单个 IP
// 的计数永远达不到阈值，只有跨来源的总数才会触发。
const identifierLockoutMultiplier = 10

// loginLocked 判断当前登录请求是否应当被限流。
//
// 采用两个维度叠加，各自解决不同的问题：
//
//	维度一（标识 + 来源 IP，阈值 MaxLoginAttempts）
//	  主力防线。把限流收敛到具体攻击来源：单个 IP 打满额度只会锁住它自己，
//	  从别处正常登录的账号主人不受影响 —— 这正是「定向锁死他人账号」的解法。
//
//	维度二（仅标识，阈值 MaxLoginAttempts × identifierLockoutMultiplier）
//	  兜底防线。用于对抗分布式爆破：攻击者换 IP 可以绕开维度一，
//	  但所有来源的失败会累加到同一个标识上，最终仍会被拦住。
func (h *Handler) loginLocked(ctx context.Context, identifier, ip string, since time.Time) (bool, error) {
	n, err := h.store.CountFailedAttemptsByIdentifierAndIP(ctx, identifier, ip, since)
	if err != nil {
		return false, err
	}
	if n >= h.cfg.MaxLoginAttempts {
		return true, nil
	}

	total, err := h.store.CountFailedAttempts(ctx, identifier, since)
	if err != nil {
		return false, err
	}
	return total >= h.cfg.MaxLoginAttempts*identifierLockoutMultiplier, nil
}

// recoveryIdentifierLocked 判断「找回密码第一步（按标识查询）」是否应限流。
// 维度与登录一致：来源优先，跨来源总量兜底。
func (h *Handler) recoveryIdentifierLocked(ctx context.Context, identifier, ip string, since time.Time) (bool, error) {
	n, err := h.store.CountFailedRecoveryByIdentifierAndIP(ctx, identifier, ip, since)
	if err != nil {
		return false, err
	}
	if n >= h.cfg.MaxLoginAttempts {
		return true, nil
	}
	total, err := h.store.CountFailedRecoveryByIdentifier(ctx, identifier, since)
	if err != nil {
		return false, err
	}
	return total >= h.cfg.MaxLoginAttempts*identifierLockoutMultiplier, nil
}

// recoveryAnswerLocked 判断「找回密码第二步（校验安全问题答案）」是否应限流。
func (h *Handler) recoveryAnswerLocked(ctx context.Context, userID int64, ip string, since time.Time) (bool, error) {
	n, err := h.store.CountFailedRecoveryByUserAndIP(ctx, userID, ip, since)
	if err != nil {
		return false, err
	}
	if n >= h.cfg.MaxLoginAttempts {
		return true, nil
	}
	total, err := h.store.CountFailedRecovery(ctx, userID, since)
	if err != nil {
		return false, err
	}
	return total >= h.cfg.MaxLoginAttempts*identifierLockoutMultiplier, nil
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, status int, values, errs map[string]string, next string) {
	h.render(w, r, status, "auth/login.html", "登录", "", map[string]any{
		"Values": values,
		"Errors": errs,
		"Next":   next,
	})
}

// ---------------------------------------------------------------------------
// 安全问题解析辅助
// ---------------------------------------------------------------------------

// parseSecurityInputs 从表单中读取 N 组「问题 + 答案」。
func parseSecurityInputs(r *http.Request) []models.AnswerInput {
	out := make([]models.AnswerInput, 0, models.SecurityQuestionCount)
	for i := 1; i <= models.SecurityQuestionCount; i++ {
		out = append(out, models.AnswerInput{
			Question: formString(r, "question_"+strconv.Itoa(i)),
			Answer:   formString(r, "answer_"+strconv.Itoa(i)),
		})
	}
	return out
}

// securityValues 收集安全问题表单值，用于校验失败时回填。
// 出于安全考虑，答案不回填到页面。
func securityValues(inputs []models.AnswerInput) map[string]string {
	out := make(map[string]string, len(inputs)*2)
	for i, in := range inputs {
		out["question_"+strconv.Itoa(i+1)] = in.Question
	}
	return out
}

// securityErrors 把安全问题校验错误映射到具体字段，便于逐项标红。
func securityErrors(err error) map[string]string {
	out := map[string]string{}
	if err == nil {
		return out
	}
	msg := err.Error()

	// 服务层错误形如「参数不合法：第 2 个问题的答案至少需要 2 个字」
	for i := 1; i <= models.SecurityQuestionCount; i++ {
		marker := fmt.Sprintf("第 %d 个问题", i)
		if strings.Contains(msg, marker) {
			field := "question_" + strconv.Itoa(i)
			if strings.Contains(msg, "答案") {
				field = "answer_" + strconv.Itoa(i)
			}
			out[field] = strings.TrimPrefix(msg, "参数不合法：")
			return out
		}
	}
	out["form"] = strings.TrimPrefix(msg, "参数不合法：")
	return out
}

// ---------------------------------------------------------------------------
// 注册
// ---------------------------------------------------------------------------

// RegisterPage 渲染注册页。
func (h *Handler) RegisterPage(w http.ResponseWriter, r *http.Request) {
	// 用运行期开关而不是配置默认值：管理员可以在「用户管理」页随时关闭注册，
	// 关闭后这个页面必须立刻拒绝访问，而不是等到下次重启。
	if !h.AllowRegistration() {
		h.renderRegister(w, r, http.StatusForbidden, map[string]string{}, map[string]string{
			"form": "系统已关闭自助注册，请联系管理员开通账号",
		}, true, 0, "")
		return
	}
	h.renderRegister(w, r, http.StatusOK, map[string]string{}, map[string]string{}, false, 0, "")
}

// RegisterSubmit 处理注册表单。
func (h *Handler) RegisterSubmit(w http.ResponseWriter, r *http.Request) {
	// 同上：注册页被关闭后，直接 POST 到这个地址也必须被挡住。
	if !h.AllowRegistration() {
		utils.SetError(w, "系统已关闭自助注册")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
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
	confirm := r.FormValue("password_confirm")
	security := parseSecurityInputs(r)

	values := map[string]string{
		"username":  username,
		"email":     email,
		"full_name": fullName,
	}
	for k, v := range securityValues(security) {
		values[k] = v
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
	if password != confirm {
		errs["password_confirm"] = "两次输入的密码不一致"
	}
	// 安全问题在创建账号前先校验，避免产生「半成品」账号
	for k, v := range securityErrors(services.ValidateSecurityAnswers(security)) {
		errs[k] = v
	}

	strength, strengthText := utils.PasswordStrength(password)

	if len(errs) > 0 {
		h.renderRegister(w, r, http.StatusUnprocessableEntity, values, errs, false, strength, strengthText)
		return
	}

	ctx := r.Context()

	// 默认角色由配置决定（默认只读，避免任何人注册即可改库存）；
	// 若系统中还没有任何用户，第一个注册者自动成为管理员。
	role := models.Role(h.cfg.DefaultRole)
	if !role.Valid() {
		role = models.RoleViewer
	}
	if n, err := h.store.CountUsers(ctx); err == nil && n == 0 {
		role = models.RoleAdmin
	}

	user, err := h.store.CreateUserWithQuestions(ctx, services.CreateUserInput{
		Username: username,
		Email:    email,
		Password: password,
		FullName: fullName,
		Role:     role,
	}, security)
	if err != nil {
		errs["form"] = businessError(err)
		if errors.Is(err, services.ErrConflict) {
			switch {
			case strings.Contains(err.Error(), "用户名"):
				errs["username"] = err.Error()
			case strings.Contains(err.Error(), "邮箱"):
				errs["email"] = err.Error()
			}
			delete(errs, "form")
		}
		h.renderRegister(w, r, http.StatusUnprocessableEntity, values, errs, false, strength, strengthText)
		return
	}

	h.logger.Info("新用户注册成功", "用户名", user.Username, "邮箱", user.Email, "角色", user.Role.Label())

	msg := fmt.Sprintf("注册成功！您的角色是「%s」，请使用 %s 登录。", user.Role.Label(), user.Username)
	if user.Role == models.RoleViewer {
		msg += "只读权限仅可查看数据，如需操作库存请联系管理员调整角色。"
	}
	h.redirectWith(w, r, "/login", "success", msg)
}

// renderRegister 统一渲染注册页。
func (h *Handler) renderRegister(w http.ResponseWriter, r *http.Request, status int,
	values, errs map[string]string, disabled bool, strength int, strengthText string) {

	questions := make([]string, 0, models.SecurityQuestionCount)
	for i := 1; i <= models.SecurityQuestionCount; i++ {
		questions = append(questions, strconv.Itoa(i))
	}

	h.render(w, r, status, "auth/register.html", "注册", "", map[string]any{
		"Values":       values,
		"Errors":       errs,
		"Disabled":     disabled,
		"Strength":     strength,
		"StrengthText": strengthText,
		"QuestionNos":  questions,
		"AnswerMinLen": services.SecurityAnswerMinLen,
		"QuestionMin":  services.SecurityQuestionMinLen,
	})
}

// ---------------------------------------------------------------------------
// 找回密码（安全问题方式）
// ---------------------------------------------------------------------------

// ForgotPasswordPage 渲染「忘记密码」第一步：输入用户名或邮箱。
func (h *Handler) ForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "auth/forgot.html", "找回密码", "", map[string]any{
		"Step":   1,
		"Values": map[string]string{},
		"Errors": map[string]string{},
	})
}

// ForgotPasswordSubmit 处理第一步：定位账号并展示其安全问题。
//
// 说明：安全问题由用户自定义，必须展示出来才能作答，
// 因此这一步无法像邮箱方式那样完全隐藏「账号是否存在」。
// 为降低被枚举的风险，这里对查询与答题都做了频率限制。
func (h *Handler) ForgotPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	identifier := formString(r, "identifier")
	values := map[string]string{"identifier": identifier}

	if identifier == "" {
		h.renderForgotStep1(w, r, http.StatusUnprocessableEntity, values,
			map[string]string{"identifier": "请输入用户名或邮箱"})
		return
	}

	ctx := r.Context()
	ip := utils.ClientIP(r, h.cfg.TrustedProxies)

	// 对同一标识的失败尝试做限制，避免被用来批量探测账号。
	//
	// 与登录同理，主力维度是「标识 + 来源 IP」：否则攻击者只要知道受害者的
	// 用户名，发几次不存在的账号名就能把对方锁在自助找回之外。
	since := time.Now().Add(-h.cfg.LockoutWindow)
	if locked, err := h.recoveryIdentifierLocked(ctx, identifier, ip, since); err != nil {
		h.serverError(w, r, err)
		return
	} else if locked {
		// 只留痕、不计入失败次数，否则锁定会被持续刷新而永不过期。
		if err := h.store.RecordBlockedRecoveryAttempt(ctx, 0, identifier, ip); err != nil {
			h.logger.Warn("记录被限流的找回密码尝试失败", "错误", err)
		}
		h.renderForgotStep1(w, r, http.StatusTooManyRequests, values, map[string]string{
			"form": fmt.Sprintf("尝试次数过多，请 %d 分钟后再试", int(h.cfg.LockoutWindow.Minutes())),
		})
		return
	}

	user, err := h.store.GetUserByIdentifier(ctx, identifier)
	if err != nil && !errors.Is(err, services.ErrNotFound) {
		h.serverError(w, r, err)
		return
	}

	if user == nil || !user.IsActive() {
		_ = h.store.RecordRecoveryAttempt(ctx, 0, identifier, ip, false)
		h.renderForgotStep1(w, r, http.StatusNotFound, values, map[string]string{
			"identifier": "账号不存在或已被禁用，请联系管理员协助处理",
		})
		return
	}

	questions, err := h.store.GetSecurityQuestionTexts(ctx, user.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if len(questions) < models.SecurityQuestionCount {
		h.renderForgotStep1(w, r, http.StatusConflict, values, map[string]string{
			"identifier": "该账号尚未设置安全问题，无法自助找回密码，请联系管理员重置",
		})
		return
	}

	h.renderForgotStep2(w, r, http.StatusOK, identifier, user.Username, questions,
		map[string]string{}, map[string]string{}, 0, "")
}

// ForgotPasswordReset 处理第二步：校验安全问题的答案并设置新密码。
func (h *Handler) ForgotPasswordReset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	identifier := formString(r, "identifier")
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")
	strength, strengthText := utils.PasswordStrength(password)

	if identifier == "" {
		h.renderForgotStep1(w, r, http.StatusBadRequest, map[string]string{},
			map[string]string{"form": "会话信息已失效，请重新开始找回流程"})
		return
	}

	ctx := r.Context()
	ip := utils.ClientIP(r, h.cfg.TrustedProxies)

	user, err := h.store.GetUserByIdentifier(ctx, identifier)
	if err != nil || user == nil || !user.IsActive() {
		h.renderForgotStep1(w, r, http.StatusBadRequest, map[string]string{"identifier": identifier},
			map[string]string{"form": "账号状态已变化，请重新开始找回流程"})
		return
	}

	questions, err := h.store.GetSecurityQuestionTexts(ctx, user.ID)
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if len(questions) < models.SecurityQuestionCount {
		h.renderForgotStep1(w, r, http.StatusConflict, map[string]string{"identifier": identifier},
			map[string]string{"form": "该账号尚未设置安全问题，请联系管理员重置密码"})
		return
	}

	answers := make([]string, 0, len(questions))
	for i := 1; i <= len(questions); i++ {
		answers = append(answers, formString(r, "answer_"+strconv.Itoa(i)))
	}

	// 表单字段校验（先于答案校验，避免无意义的 bcrypt 计算）
	errs := map[string]string{}
	if err := utils.ValidatePassword(password); err != nil {
		errs["password"] = err.Error()
	}
	if password != confirm {
		errs["password_confirm"] = "两次输入的密码不一致"
	}
	for i, a := range answers {
		if strings.TrimSpace(a) == "" {
			errs["answer_"+strconv.Itoa(i+1)] = "请填写答案"
		}
	}

	// 答题失败次数限制。同样以「账号 + 来源 IP」为主力维度：
	// 只按账号计数的话，攻击者乱填几个答案就能把受害者锁在自助找回之外。
	since := time.Now().Add(-h.cfg.LockoutWindow)
	if locked, err := h.recoveryAnswerLocked(ctx, user.ID, ip, since); err != nil {
		h.serverError(w, r, err)
		return
	} else if locked {
		// 同上：被限流的请求只留痕，不刷新锁定窗口。
		if err := h.store.RecordBlockedRecoveryAttempt(ctx, user.ID, identifier, ip); err != nil {
			h.logger.Warn("记录被限流的找回密码尝试失败", "错误", err)
		}
		errs["form"] = fmt.Sprintf("答案错误次数过多，请 %d 分钟后再试", int(h.cfg.LockoutWindow.Minutes()))
		h.renderForgotStep2(w, r, http.StatusTooManyRequests, identifier, user.Username, questions,
			map[string]string{}, errs, strength, strengthText)
		return
	}

	if len(errs) > 0 {
		h.renderForgotStep2(w, r, http.StatusUnprocessableEntity, identifier, user.Username, questions,
			map[string]string{}, errs, strength, strengthText)
		return
	}

	ok, err := h.store.VerifySecurityAnswers(ctx, user.ID, answers)
	if err != nil {
		if errors.Is(err, services.ErrSecurityQuestionsNotSet) {
			h.renderForgotStep1(w, r, http.StatusConflict, map[string]string{"identifier": identifier},
				map[string]string{"form": "该账号尚未设置安全问题，请联系管理员重置密码"})
			return
		}
		h.serverError(w, r, err)
		return
	}

	if !ok {
		_ = h.store.RecordRecoveryAttempt(ctx, user.ID, identifier, ip, false)

		// 剩余次数取两个维度中更小的那个 —— 实际拦住用户的是先触发的那个，
		// 只按其中一个算会给出偏乐观的数字。
		remaining := h.cfg.MaxLoginAttempts
		if n, err := h.store.CountFailedRecoveryByUserAndIP(ctx, user.ID, ip, since); err == nil {
			remaining = h.cfg.MaxLoginAttempts - n
		}
		if total, err := h.store.CountFailedRecovery(ctx, user.ID, since); err == nil {
			if r := h.cfg.MaxLoginAttempts*identifierLockoutMultiplier - total; r < remaining {
				remaining = r
			}
		}
		if remaining < 0 {
			remaining = 0
		}

		h.logger.Warn("安全问题答案错误", "用户", user.Username, "IP", ip, "剩余次数", remaining)
		h.renderForgotStep2(w, r, http.StatusUnauthorized, identifier, user.Username, questions,
			map[string]string{},
			map[string]string{"form": fmt.Sprintf("安全问题答案不正确，还可尝试 %d 次", remaining)},
			strength, strengthText)
		return
	}

	if err := h.store.SetPassword(ctx, user.ID, password); err != nil {
		h.serverError(w, r, err)
		return
	}

	_ = h.store.RecordRecoveryAttempt(ctx, user.ID, identifier, ip, true)
	_ = h.store.ClearFailedRecovery(ctx, user.ID)

	// 密码已变更，强制所有设备重新登录
	if _, err := h.store.DeleteSessionsByUser(ctx, user.ID, ""); err != nil {
		h.logger.Warn("清理旧会话失败", "用户ID", user.ID, "错误", err)
	}

	h.logger.Info("用户通过安全问题重置了密码", "用户", user.Username, "IP", ip)
	h.redirectWith(w, r, "/login", "success", "密码已重置，请使用新密码登录")
}

// renderForgotStep1 渲染找回密码第一步。
func (h *Handler) renderForgotStep1(w http.ResponseWriter, r *http.Request, status int,
	values, errs map[string]string) {
	h.render(w, r, status, "auth/forgot.html", "找回密码", "", map[string]any{
		"Step":   1,
		"Values": values,
		"Errors": errs,
	})
}

// renderForgotStep2 渲染找回密码第二步（展示安全问题 + 设置新密码）。
func (h *Handler) renderForgotStep2(w http.ResponseWriter, r *http.Request, status int,
	identifier, username string, questions []string, values, errs map[string]string,
	strength int, strengthText string) {

	items := make([]map[string]any, 0, len(questions))
	for i, q := range questions {
		no := i + 1
		items = append(items, map[string]any{
			"No":     no,
			"Field":  "answer_" + strconv.Itoa(no),
			"Text":   q,
			"Error":  errs["answer_"+strconv.Itoa(no)],
			"Answer": values["answer_"+strconv.Itoa(no)],
		})
	}

	h.render(w, r, status, "auth/forgot.html", "找回密码", "", map[string]any{
		"Step":         2,
		"Identifier":   identifier,
		"Username":     username,
		"Questions":    items,
		"Values":       values,
		"Errors":       errs,
		"Strength":     strength,
		"StrengthText": strengthText,
	})
}

// ---------------------------------------------------------------------------
// 退出登录
// ---------------------------------------------------------------------------

// Logout 销毁当前会话。
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.sessions.Destroy(r.Context(), w, r); err != nil {
		h.logger.Warn("退出登录失败", "错误", err)
	}
	h.redirectWith(w, r, "/login", "info", "您已安全退出")
}

// sanitizeNext 防止开放重定向：只接受站内相对路径，否则返回空串。
//
// 判定刻意比「字符串看着像相对路径」更严。因为最终落进 Location 头的值
// 是由**浏览器**解析的，而浏览器遵循 WHATWG URL 规范，会先静默剥离
// ASCII 空白（Tab、LF、CR、垂直制表符等）再解析。于是：
//
//	"/\t/evil.com"    → 剥离 Tab 后变成 "//evil.com"
//	"/%09/evil.com"   → 解码后同样是 Tab，同样是 "//evil.com"
//
// 两者在字符串层面都以单个斜杠开头、也不含裸换行，光靠 HasPrefix 与
// Contains("\n") 拦不住，但浏览器会把它们当成**协议相对 URL**，
// 一次登录跳转就把用户送到站外（正好是钓鱼最喜欢的落点）。
//
// 因此这里做三件事：先看前缀，再用 url.Parse 复核没有 Scheme / Host，
// 最后对**解析之后**的字符串拒绝控制字符 —— 顺序不能颠倒，
// 百分号编码的控制字符只有在解析后才会现形。
func sanitizeNext(next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return ""
	}
	// "//host" 与 "/\host" 在浏览器里都被当成协议相对 URL。
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return ""
	}

	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/") {
		return ""
	}

	// 丢弃 fragment：它不参与导航目标，留在 URL 里只会让审计更难读。
	rebuilt := u.Path
	if u.RawQuery != "" {
		rebuilt += "?" + u.RawQuery
	}
	if containsControlChar(rebuilt) {
		return ""
	}
	return rebuilt
}

// containsControlChar 报告 s 是否含 ASCII 空白或控制字符（含空格与 DEL）。
//
// 这些字符在写入 Location 头之前不会被 Go 过滤，却会被浏览器当作空白
// 剥掉，从而改变 URL 的实际语义，属于开放重定向的经典绕过手法：
// "/ /evil.com" 与 "/\t/evil.com" 剥掉空白后都成了 "//evil.com"。
//
// 空格之所以一并拒绝，是因为 WHATWG 的剥离规则覆盖它，
// 而站内路径与查询串本就不该出现裸空格（该编码成 %20）。
func containsControlChar(s string) bool {
	for _, r := range s {
		if r <= 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
