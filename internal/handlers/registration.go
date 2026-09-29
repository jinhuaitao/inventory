package handlers

import (
	"context"
	"net/http"
	"sync"

	"inventory/internal/models"
)

// ---------------------------------------------------------------------------
// 自助注册开关
//
// 取值优先级：settings 表中的显式设置（管理员在「用户管理」页调整）
// 高于配置项 INVENTORY_ALLOW_REGISTRATION 给出的默认值。
// 环境变量决定初始状态，管理员可以在页面上覆盖它。
// ---------------------------------------------------------------------------

// registrationState 缓存开关的当前取值。
//
// 之所以缓存在内存里而不是每次查库：几乎每个页面渲染都要用到它
// （登录页要决定显示「立即注册」还是「已关闭自助注册」），
// 而 PageData 的组装本是纯内存操作，不该因此多打一次数据库。
// 写入路径只有管理员点按钮这一条，写完立刻回填，不存在长时间不一致。
type registrationState struct {
	mu       sync.RWMutex
	enabled  bool
	explicit bool
}

// get 返回 (当前取值, 是否由页面设置过)。
func (s *registrationState) get() (bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled, s.explicit
}

// set 覆盖缓存的取值。
func (s *registrationState) set(enabled, explicit bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled, s.explicit = enabled, explicit
}

// InitRegistration 在服务启动时把自助注册开关从数据库载入内存。
//
// 必须在开始对外提供 HTTP 服务之前调用一次：registrationState 的零值是
// 「关闭」，不加载的话一个本来开放注册的站点会静默变成关闭。
func (h *Handler) InitRegistration(ctx context.Context) error {
	st, err := h.store.RegistrationState(ctx, h.cfg.AllowRegistration)
	if err != nil {
		return err
	}
	h.registration.set(st.Enabled, st.Explicit)
	return nil
}

// AllowRegistration 返回当前是否开放自助注册。
func (h *Handler) AllowRegistration() bool {
	enabled, _ := h.registration.get()
	return enabled
}

// RegistrationStatus 返回开关取值，以及它是否来自管理员在页面上的设置。
func (h *Handler) RegistrationStatus() (enabled, explicit bool) {
	return h.registration.get()
}

// UserRegistrationToggle 由管理员在「用户管理」页开启或关闭自助注册。
//
// 表单提交的是**期望的目标状态**（enabled=true / false）而不是「切换」：
// 重复提交、或在另一个标签页里已经改过的情况下，结果依然可预期 ——
// 一个「切换」语义的按钮会让并发操作互相打架。
func (h *Handler) UserRegistrationToggle(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.serverError(w, r, err)
		return
	}

	ctx := r.Context()
	actor := h.currentUser(r).Username

	// 恢复默认：删除页面设置，让开关重新由环境变量决定。
	if formString(r, "action") == "reset" {
		if err := h.store.ResetRegistrationSetting(ctx); err != nil {
			h.logger.Error("恢复自助注册默认设置失败", "错误", err, "操作人", actor)
			h.redirectWith(w, r, "/users", "error", "恢复默认设置失败，请稍后重试")
			return
		}
		enabled := h.cfg.AllowRegistration
		h.registration.set(enabled, false)
		h.logger.Info("自助注册开关已恢复为环境变量默认值",
			"状态", registrationWord(enabled), "操作人", actor)
		h.redirectWith(w, r, "/users", "success",
			"已恢复为环境变量默认值（当前"+registrationWord(enabled)+"）")
		return
	}

	enable := formBool(r, "enabled")
	if err := h.store.SetRegistrationEnabled(ctx, enable); err != nil {
		h.logger.Error("保存自助注册开关失败", "错误", err, "操作人", actor)
		h.redirectWith(w, r, "/users", "error", "保存自助注册开关失败，请稍后重试")
		return
	}
	h.registration.set(enable, true)

	h.logger.Info("管理员调整了自助注册开关", "状态", registrationWord(enable), "操作人", actor)
	h.redirectWith(w, r, "/users", "success", "已"+registrationWord(enable)+"自助注册")
}

// registrationWord 把开关状态转成中文，供提示与日志复用。
func registrationWord(enabled bool) string {
	if enabled {
		return "开启"
	}
	return "关闭"
}

// registrationPageData 组装「用户管理」页里自助注册卡片需要的数据。
func (h *Handler) registrationPageData() map[string]any {
	enabled, explicit := h.RegistrationStatus()

	// 自助注册用户的默认角色由配置决定，卡片上如实说明，
	// 免得管理员以为新注册的人会直接拿到可写权限。
	role := models.Role(h.cfg.DefaultRole)
	if !role.Valid() {
		role = models.RoleViewer
	}

	return map[string]any{
		"Enabled":     enabled,
		"Explicit":    explicit,
		"EnvDefault":  h.cfg.AllowRegistration,
		"DefaultRole": role.Label(),
	}
}
