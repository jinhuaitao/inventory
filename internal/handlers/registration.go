package handlers

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"inventory/internal/models"
	"inventory/internal/services"
)

// ---------------------------------------------------------------------------
// 自助注册开关
//
// 取值优先级：settings 表中的显式设置（管理员在「用户管理」页调整）
// 高于配置项 INVENTORY_ALLOW_REGISTRATION 给出的默认值。
// 环境变量决定初始状态，管理员可以在页面上覆盖它。
// ---------------------------------------------------------------------------

// usersPath 是用户管理页地址，开关操作完成后回到这里。
const usersPath = "/users"

// 注册开关表单支持的动作。
const (
	registrationActionSet   = "set"   // 写入指定状态（enabled=true / false）
	registrationActionReset = "reset" // 删除页面设置，回到环境变量默认值
)

// RegistrationRefreshInterval 是后台同步开关取值的间隔。
//
// 进程内自己写入时会立刻回填缓存，所以这个间隔只影响「由本进程之外改动」
// 的收敛速度：另一个实例、或直接改数据库（备份换入、手工 UPDATE）之后，
// 本进程最多在一个间隔之后跟上，而不是必须重启。
// 代价是每个间隔一次主键查询，可以忽略。
const RegistrationRefreshInterval = 15 * time.Second

// registrationRefreshTimeout 限制单次回源查询的耗时，避免数据库卡住时
// 后台协程长时间挂起。
const registrationRefreshTimeout = 5 * time.Second

// registrationState 缓存开关取值。
//
// 之所以缓存在内存里而不是每次查库：几乎每个页面渲染都要用到它
// （登录页要决定显示「立即注册」还是「已关闭自助注册」），
// 而 render **不能依赖数据库** —— serverError 也要渲染 500 页面，
// 那恰恰是数据库出问题的时候。缓存让错误页在任何情况下都渲染得出来。
//
// 新鲜度由两条路径保证：
//   - 本进程写入后立刻回填 —— 管理员点完按钮立刻看到新状态；
//   - 后台按 RegistrationRefreshInterval 定期回源 —— 吸收外部改动。
type registrationState struct {
	mu     sync.RWMutex
	value  services.RegistrationState
	loaded bool
}

// get 返回缓存取值；第二个返回值表示是否已经成功读到过。
func (s *registrationState) get() (services.RegistrationState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value, s.loaded
}

// put 覆盖缓存。
func (s *registrationState) put(v services.RegistrationState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value, s.loaded = v, true
}

// registrationDefault 返回配置项给出的默认值（环境变量 INVENTORY_ALLOW_REGISTRATION）。
func (h *Handler) registrationDefault() bool { return h.cfg.AllowRegistration }

// InitRegistration 在启动时把开关从数据库载入内存。
//
// 这里刻意让错误**致命**：它紧跟在 Migrate 成功之后执行，此时读库失败说明
// 数据库状态异常，应该在开始对外服务之前就暴露出来。运行期的回源失败则相反，
// 沿用旧值即可（见 RefreshRegistration）。
func (h *Handler) InitRegistration(ctx context.Context) error {
	v, err := h.store.RegistrationState(ctx, h.registrationDefault())
	if err != nil {
		return err
	}
	h.registration.put(v)
	return nil
}

// RefreshRegistration 重新从数据库读取开关并更新缓存。
//
// 失败时**保留上一次的取值**并返回错误：一次数据库抖动不该把开关翻掉，
// 那会让「注册入口突然出现又消失」变成无从解释的现象。
func (h *Handler) RefreshRegistration(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, registrationRefreshTimeout)
	defer cancel()

	v, err := h.store.RegistrationState(ctx, h.registrationDefault())
	if err != nil {
		return err
	}
	h.registration.put(v)
	return nil
}

// RunRegistrationRefresh 按固定间隔回源，直到 ctx 结束。由 cmd/server 在后台启动。
func (h *Handler) RunRegistrationRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = RegistrationRefreshInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.RefreshRegistration(ctx); err != nil {
				h.logger.Warn("同步自助注册开关失败，沿用当前取值", "错误", err)
			}
		}
	}
}

// registrationSnapshot 返回当前生效的开关状态。
//
// 缓存尚未载入时（例如将来有人直接构造 Handler 却漏调 InitRegistration）
// 回落到配置默认值，而不是结构体零值 —— 零值是「关闭」，那会让一个默认开放的
// 站点静默变成关闭，而且没有任何迹象。宁可多写这四行，也不要留这种坑。
func (h *Handler) registrationSnapshot() services.RegistrationState {
	if v, ok := h.registration.get(); ok {
		return v
	}
	return services.RegistrationState{Enabled: h.registrationDefault()}
}

// AllowRegistration 返回当前是否开放自助注册。
func (h *Handler) AllowRegistration() bool {
	return h.registrationSnapshot().Enabled
}

// RegistrationStatus 返回开关取值，以及它是否来自管理员在页面上的设置。
func (h *Handler) RegistrationStatus() (enabled, explicit bool) {
	v := h.registrationSnapshot()
	return v.Enabled, v.Explicit
}

// UserRegistrationToggle 由管理员在「用户管理」页开启或关闭自助注册。
//
// 表单提交的是**期望的目标状态**（enabled=true / false）而不是「切换」：
// 重复提交、或在另一个标签页里已经改过的情况下，结果依然可预期 ——
// 「切换」语义的按钮会让并发操作互相打架。
//
// 参数解析刻意严格：字段缺失、取值非法、动作未知都直接拒绝。
// 若沿用 formBool「读不到就当 false」的习惯，一次畸形请求就会变成
// 「静默关闭注册」—— 一个安全相关的开关不该有这种失败方式。
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

	action := formString(r, "action")
	if action == "" {
		action = registrationActionSet
	}

	switch action {
	case registrationActionSet:
		enable, err := strconv.ParseBool(formString(r, "enabled"))
		if err != nil {
			h.logger.Warn("注册开关请求参数不合法",
				"enabled", r.FormValue("enabled"), "操作人", actor)
			h.redirectWith(w, r, usersPath, "error", "请求参数不正确，请刷新页面后重试")
			return
		}

		if err := h.store.SetRegistrationEnabled(ctx, enable); err != nil {
			h.logger.Error("保存自助注册开关失败", "错误", err, "操作人", actor)
			h.redirectWith(w, r, usersPath, "error", "保存自助注册开关失败，请稍后重试")
			return
		}
		// 写入成功后直接回填缓存，管理员点完立刻看到新状态。
		// 这里信任写入值而不是回读：SetSetting 落库的就是 strconv.FormatBool(enable)，
		// 回读只会得到同一个布尔值，白搭一次查询。
		h.registration.put(services.RegistrationState{Enabled: enable, Explicit: true})

		h.logger.Info("管理员调整了自助注册开关", "状态", registrationWord(enable), "操作人", actor)
		h.redirectWith(w, r, usersPath, "success", "已"+registrationWord(enable)+"自助注册")

	case registrationActionReset:
		if err := h.store.ResetRegistrationSetting(ctx); err != nil {
			h.logger.Error("恢复自助注册默认设置失败", "错误", err, "操作人", actor)
			h.redirectWith(w, r, usersPath, "error", "恢复默认设置失败，请稍后重试")
			return
		}
		enabled := h.registrationDefault()
		h.registration.put(services.RegistrationState{Enabled: enabled})

		h.logger.Info("自助注册开关已恢复为环境变量默认值",
			"状态", registrationWord(enabled), "操作人", actor)
		h.redirectWith(w, r, usersPath, "success",
			"已恢复为环境变量默认值（当前"+registrationWord(enabled)+"）")

	default:
		h.logger.Warn("收到未知的注册开关动作", "action", action, "操作人", actor)
		h.redirectWith(w, r, usersPath, "error", "请求参数不正确，请刷新页面后重试")
	}
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
