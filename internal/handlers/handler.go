// Package handlers 实现全部 HTTP 处理器。
package handlers

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"inventory/internal/auth"
	"inventory/internal/config"
	"inventory/internal/middleware"
	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/updater"
	"inventory/internal/utils"
	"inventory/web"
)

// Version 由构建时通过 -ldflags 注入。
var Version = "dev"

// Handler 聚合全部处理器依赖。
type Handler struct {
	store    *services.Store
	renderer *utils.Renderer
	sessions *auth.SessionManager
	updater  *updater.Service
	mw       *middleware.Manager
	cfg      *config.Config
	logger   *slog.Logger

	// registration 是「是否开放自助注册」的运行期开关。
	//
	// 它不同于 cfg.AllowRegistration：后者是环境变量给出的**默认值**，
	// 前者是最终生效值，可由管理员在「用户管理」页随时调整。
	// 必须在对外服务之前调用 InitRegistration 载入。
	registration registrationState
}

// New 创建处理器集合。
func New(
	store *services.Store,
	renderer *utils.Renderer,
	sessions *auth.SessionManager,
	updaterSvc *updater.Service,
	mw *middleware.Manager,
	cfg *config.Config,
	logger *slog.Logger,
) *Handler {
	return &Handler{
		store:    store,
		renderer: renderer,
		sessions: sessions,
		updater:  updaterSvc,
		mw:       mw,
		cfg:      cfg,
		logger:   logger,
	}
}

// PageData 是所有页面模板共享的根上下文。
type PageData struct {
	Title             string
	AppName           string
	Active            string // 侧边栏高亮标识
	User              *models.User
	Flash             *utils.Flash
	CSRF              string
	CurrentPath       string
	Query             url.Values
	Env               string
	AllowRegistration bool
	UpdateEnabled     bool
	Version           string
	Year              int
	Data              any
	// AssetVersion 是静态资源的内容指纹，模板用它给 /static/ 下的 URL
	// 加 ?v= 参数。见 web.StaticVersion 的说明。
	AssetVersion string
}

// render 渲染页面。
func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, tmpl, title, active string, data any) {
	pd := &PageData{
		Title:             title,
		AppName:           h.cfg.AppName,
		Active:            active,
		User:              auth.UserFrom(r.Context()),
		Flash:             utils.GetFlash(w, r),
		CSRF:              middleware.CSRFFrom(r.Context()),
		CurrentPath:       r.URL.Path,
		Query:             r.URL.Query(),
		Env:               h.cfg.Env,
		AllowRegistration: h.AllowRegistration(),
		UpdateEnabled:     h.cfg.UpdateConfigured(),
		Version:           Version,
		Year:              time.Now().In(utils.DisplayZone).Year(),
		Data:              data,
		AssetVersion:      web.StaticVersion(),
	}
	h.renderer.Render(w, status, tmpl, pd)
}

// redirectWith 重定向并附带一条提示消息。
func (h *Handler) redirectWith(w http.ResponseWriter, r *http.Request, target, flashType, message string) {
	if flashType != "" {
		utils.SetFlash(w, flashType, message)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// redirectBack 重定向回来源页（优先使用 referer，否则回到 fallback）。
func (h *Handler) redirectBack(w http.ResponseWriter, r *http.Request, fallback, flashType, message string) {
	target := fallback
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && (u.Host == "" || u.Host == r.Host) {
			target = u.RequestURI()
		}
	}
	h.redirectWith(w, r, target, flashType, message)
}

// currentQueryWithoutPage 把当前请求的查询参数（去掉 page）还原成查询串，
// 供分页链接复用。列表页如果给模板传空串，用户搜索后翻到第 2 页时
// 筛选条件会全部丢失，看起来就像「搜索没生效」。
func currentQueryWithoutPage(r *http.Request) string {
	q := r.URL.Query()
	q.Del("page")
	return q.Encode()
}

// ---------------------------------------------------------------------------
// 错误处理
// ---------------------------------------------------------------------------

// serverError 记录错误并渲染 500 页面。
func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.Error("请求处理失败",
		"路径", r.URL.Path,
		"方法", r.Method,
		"错误", err,
	)
	h.render(w, r, http.StatusInternalServerError, "errors/500.html", "服务器错误", "", nil)
}

// notFound 渲染 404 页面。
func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusNotFound, "errors/404.html", "页面不存在", "", nil)
}

// forbidden 渲染 403 提示。
func (h *Handler) forbidden(w http.ResponseWriter, r *http.Request, msg string) {
	if utils.WantsJSON(r) {
		// 复用 writeJSON：手拼字符串遇到 msg 里的引号会产出非法 JSON。
		writeJSON(w, http.StatusForbidden, map[string]string{"error": msg})
		return
	}
	utils.SetError(w, msg)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// businessError 把业务错误转换为用户可读的提示。
func businessError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, services.ErrNotFound):
		return "记录不存在或已被删除"
	case errors.Is(err, services.ErrConflict):
		return err.Error()
	case errors.Is(err, services.ErrInsufficientStock):
		return err.Error()
	case errors.Is(err, services.ErrLastAdmin):
		return err.Error()
	case errors.Is(err, services.ErrInvalidInput):
		return err.Error()
	case errors.Is(err, services.ErrInvalidCredentials):
		return err.Error()
	case errors.Is(err, services.ErrTokenInvalid):
		return "链接无效或已过期，请重新申请"
	case errors.Is(err, services.ErrAccountDisabled):
		return err.Error()
	default:
		return "操作失败，请稍后重试"
	}
}

// ---------------------------------------------------------------------------
// 权限辅助
// ---------------------------------------------------------------------------

// currentUser 返回当前登录用户（可能为 nil）。
func (h *Handler) currentUser(r *http.Request) *models.User {
	return auth.UserFrom(r.Context())
}

// canWrite 检查写权限；无权限时已写出响应并返回 false。
func (h *Handler) canWrite(w http.ResponseWriter, r *http.Request) bool {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return false
	}
	if !u.Role.CanWrite() {
		h.forbidden(w, r, "当前角色为「"+u.Role.Label()+"」，没有修改数据的权限")
		return false
	}
	return true
}

// canManageUsers 检查用户管理权限。
func (h *Handler) canManageUsers(w http.ResponseWriter, r *http.Request) bool {
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return false
	}
	if !u.Role.CanManageUsers() {
		h.forbidden(w, r, "只有管理员可以管理用户账号")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// 表单解析辅助
// ---------------------------------------------------------------------------

// formString 读取并去除首尾空格的表单字段。
func formString(r *http.Request, key string) string {
	return strings.TrimSpace(r.FormValue(key))
}

// formInt 读取整数字段，解析失败返回默认值。
func formInt(r *http.Request, key string, fallback int) int {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// formFloat 读取浮点字段，解析失败返回默认值。
//
// ⚠️ 落库前请先用 formNumError 把关：静默回退 0 只适合「取默认值」的
// 场景，业务数值字段直接采用会让 "12,000" 这样的输入悄悄变成 0 元。
func formFloat(r *http.Request, key string, fallback float64) float64 {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return f
}

// formNumError 校验数字表单字段：空值视为「未填」放行（由服务层取默认/0），
// 非法输入（如 "12,000"、"abc"）返回字段错误文案，绝不能静默归零落库。
func formNumError(r *http.Request, key, label string, integer bool) string {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return ""
	}
	var err error
	if integer {
		_, err = strconv.Atoi(raw)
	} else {
		_, err = strconv.ParseFloat(raw, 64)
	}
	if err != nil {
		if integer {
			return label + "必须是整数"
		}
		return label + "不是有效的数字"
	}
	return ""
}

// formBool 读取复选框状态。
func formBool(r *http.Request, key string) bool {
	v := strings.ToLower(strings.TrimSpace(r.FormValue(key)))
	return v == "on" || v == "true" || v == "1" || v == "yes"
}

// formInt64Ptr 读取可选的外键 ID，空值或 0 返回 nil。
func formInt64Ptr(r *http.Request, key string) *int64 {
	raw := strings.TrimSpace(r.FormValue(key))
	if raw == "" {
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

// formStringMap 把表单值收集为 map，用于校验失败时回填。
func formStringMap(r *http.Request, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = strings.TrimSpace(r.FormValue(k))
	}
	return out
}

// ---------------------------------------------------------------------------
// 静态资源
// ---------------------------------------------------------------------------

// staticHandler 从内嵌文件系统提供静态资源，并设置合理的缓存头。
func (h *Handler) staticHandler() http.Handler {
	sub, err := fs.Sub(web.FS, "static")
	if err != nil {
		h.logger.Error("加载静态资源目录失败", "错误", err)
		return http.NotFoundHandler()
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 资源 URL 带内容指纹（模板里用 ?v={{.AssetVersion}} 生成），
		// 因此可以放心长期缓存：内容一变指纹就变，URL 变了浏览器自然会重新拉取。
		//
		// ⚠️ 千万不要去掉指纹却保留长 max-age —— 一旦浏览器缓存了旧脚本，
		// 而响应又没有 ETag / Last-Modified，它将无法重新验证，
		// 二进制更新后前端会继续跑旧代码（页面文案是新的、功能是旧的）。
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	})
}
