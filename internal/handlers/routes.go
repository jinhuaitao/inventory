package handlers

import (
	"encoding/json"
	"net/http"

	"inventory/internal/middleware"
	"inventory/web"
)

// Routes 构建完整的路由表与中间件链。
func (h *Handler) Routes() http.Handler {
	root := http.NewServeMux()

	// ---------- 静态资源与运维探针 ----------
	root.Handle("GET /static/", http.StripPrefix("/static/", h.staticHandler()))
	root.HandleFunc("GET /healthz", h.Health)
	root.HandleFunc("GET /readyz", h.Ready)
	root.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		// 带上内容指纹：静态资源是按长期缓存下发的，而这个 301 本身也会被
		// 浏览器永久缓存，不带指纹就等于把某个旧版本的图标钉死。
		http.Redirect(w, r, "/static/favicon.svg?v="+web.StaticVersion(), http.StatusMovedPermanently)
	})

	// ---------- 认证（无需登录）----------
	root.Handle("GET /login", h.mw.RedirectIfAuthenticated(http.HandlerFunc(h.LoginPage)))
	root.Handle("POST /login", http.HandlerFunc(h.LoginSubmit))
	root.Handle("GET /register", h.mw.RedirectIfAuthenticated(http.HandlerFunc(h.RegisterPage)))
	root.Handle("POST /register", http.HandlerFunc(h.RegisterSubmit))
	// 找回密码：第一步定位账号并展示安全问题，第二步作答并设置新密码
	root.Handle("GET /forgot-password", h.mw.RedirectIfAuthenticated(http.HandlerFunc(h.ForgotPasswordPage)))
	root.Handle("POST /forgot-password", http.HandlerFunc(h.ForgotPasswordSubmit))
	root.Handle("POST /forgot-password/reset", http.HandlerFunc(h.ForgotPasswordReset))
	root.Handle("POST /logout", http.HandlerFunc(h.Logout))

	// ---------- 需要登录的业务路由 ----------
	app := http.NewServeMux()
	h.registerAppRoutes(app)
	root.Handle("/", h.mw.RequireAuth(app))

	return middleware.Chain(root,
		middleware.Recoverer(h.logger),
		middleware.Logger(h.logger, h.cfg.TrustedProxies),
		middleware.SecurityHeaders(h.cfg),
		h.mw.Authenticate,
		h.mw.CSRF,
	)
}

// registerAppRoutes 注册登录后的全部业务路由。
func (h *Handler) registerAppRoutes(mux *http.ServeMux) {
	// 仪表盘
	mux.HandleFunc("GET /{$}", h.Dashboard)

	// 个人中心
	mux.HandleFunc("GET /profile", h.ProfilePage)
	mux.HandleFunc("POST /profile", h.ProfileUpdate)
	mux.HandleFunc("GET /profile/password", h.PasswordPage)
	mux.HandleFunc("POST /profile/password", h.PasswordUpdate)
	mux.HandleFunc("GET /profile/security", h.SecurityQuestionsPage)
	mux.HandleFunc("POST /profile/security", h.SecurityQuestionsUpdate)
	mux.HandleFunc("GET /profile/sessions", h.SessionsPage)
	mux.HandleFunc("POST /profile/sessions/revoke", h.RevokeSession)
	mux.HandleFunc("POST /profile/sessions/revoke-all", h.RevokeAllSessions)

	// 商品管理
	mux.HandleFunc("GET /products", h.ProductList)
	mux.HandleFunc("GET /products/new", h.ProductNew)
	mux.HandleFunc("POST /products", h.ProductCreate)
	mux.HandleFunc("GET /products/export", h.ProductExport)
	mux.HandleFunc("GET /products/{id}", h.ProductDetail)
	mux.HandleFunc("GET /products/{id}/edit", h.ProductEdit)
	mux.HandleFunc("POST /products/{id}", h.ProductUpdate)
	mux.HandleFunc("POST /products/{id}/status", h.ProductStatus)
	mux.HandleFunc("POST /products/{id}/delete", h.ProductDelete)

	// 分类管理
	mux.HandleFunc("GET /categories", h.CategoryList)
	mux.HandleFunc("GET /categories/new", h.CategoryNew)
	mux.HandleFunc("POST /categories", h.CategoryCreate)
	mux.HandleFunc("GET /categories/{id}/edit", h.CategoryEdit)
	mux.HandleFunc("POST /categories/{id}", h.CategoryUpdate)
	mux.HandleFunc("POST /categories/{id}/delete", h.CategoryDelete)

	// 供应商管理
	mux.HandleFunc("GET /suppliers", h.SupplierList)
	mux.HandleFunc("GET /suppliers/new", h.SupplierNew)
	mux.HandleFunc("POST /suppliers", h.SupplierCreate)
	mux.HandleFunc("GET /suppliers/{id}", h.SupplierDetail)
	mux.HandleFunc("GET /suppliers/{id}/edit", h.SupplierEdit)
	mux.HandleFunc("POST /suppliers/{id}", h.SupplierUpdate)
	mux.HandleFunc("POST /suppliers/{id}/delete", h.SupplierDelete)

	// 库存操作
	mux.HandleFunc("GET /stock/in", h.StockInPage)
	mux.HandleFunc("POST /stock/in", h.StockInSubmit)
	mux.HandleFunc("GET /stock/out", h.StockOutPage)
	mux.HandleFunc("POST /stock/out", h.StockOutSubmit)
	mux.HandleFunc("GET /stock/adjust", h.StockAdjustPage)
	mux.HandleFunc("POST /stock/adjust", h.StockAdjustSubmit)
	mux.HandleFunc("GET /stock/movements", h.MovementList)
	mux.HandleFunc("GET /stock/movements/export", h.MovementExport)
	mux.HandleFunc("GET /stock/low", h.LowStockList)

	// 报表
	mux.HandleFunc("GET /reports", h.Reports)

	// 用户管理
	mux.HandleFunc("GET /users", h.UserList)
	mux.HandleFunc("GET /users/new", h.UserNew)
	mux.HandleFunc("POST /users", h.UserCreate)
	mux.HandleFunc("GET /users/{id}/edit", h.UserEdit)
	mux.HandleFunc("POST /users/{id}", h.UserUpdate)
	mux.HandleFunc("POST /users/{id}/delete", h.UserDelete)

	// 系统更新（仅管理员）
	mux.HandleFunc("GET /admin/update", h.UpdatePage)
	mux.HandleFunc("POST /admin/update/check", h.UpdateCheck)
	mux.HandleFunc("POST /admin/update/apply", h.UpdateApply)

	// 数据维护（仅管理员）：备份 / 恢复 / 清理演示数据
	mux.HandleFunc("GET /admin/maintenance", h.MaintenancePage)
	mux.HandleFunc("POST /admin/maintenance/backup", h.BackupCreate)
	mux.HandleFunc("GET /admin/maintenance/backup/download", h.BackupDownload)
	mux.HandleFunc("POST /admin/maintenance/backup/delete", h.BackupDelete)
	mux.HandleFunc("POST /admin/maintenance/restore", h.RestoreUpload)
	mux.HandleFunc("POST /admin/maintenance/restore/discard", h.RestoreDiscard)
	mux.HandleFunc("POST /admin/maintenance/purge-demo-data", h.PurgeDemoData)

	// 兜底 404
	mux.HandleFunc("/", h.notFound)
}

// ---------------------------------------------------------------------------
// 运维探针
// ---------------------------------------------------------------------------

// Health 存活探针：进程能响应即视为存活。
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": Version,
	})
}

// Ready 就绪探针：额外检查数据库连通性。
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DB().PingContext(r.Context()); err != nil {
		h.logger.Error("数据库健康检查失败", "错误", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "unavailable",
			"error":  "数据库连接异常",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}
