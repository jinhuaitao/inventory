// Package middleware 提供 HTTP 中间件：会话加载、权限校验、CSRF 防护、
// 安全响应头、访问日志与 panic 恢复。
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"inventory/internal/auth"
	"inventory/internal/config"
	"inventory/internal/models"
	"inventory/internal/utils"
)

// Middleware 是标准的 HTTP 中间件签名。
type Middleware func(http.Handler) http.Handler

// Chain 按声明顺序把中间件套在 handler 外层（先声明的先执行）。
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey string

const csrfKey ctxKey = "inventory.csrf"

// CSRFFrom 从上下文取出当前请求应使用的 CSRF 令牌。
func CSRFFrom(ctx context.Context) string {
	v, _ := ctx.Value(csrfKey).(string)
	return v
}

// Manager 持有中间件所需的依赖。
type Manager struct {
	sessions *auth.SessionManager
	cfg      *config.Config
	logger   *slog.Logger
}

// New 创建中间件管理器。
func New(sessions *auth.SessionManager, cfg *config.Config, logger *slog.Logger) *Manager {
	return &Manager{sessions: sessions, cfg: cfg, logger: logger}
}

// ---------------------------------------------------------------------------
// 基础中间件
// ---------------------------------------------------------------------------

// Recoverer 捕获 panic，记录堆栈并返回 500。
func Recoverer(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if logger != nil {
						logger.Error("请求处理发生 panic",
							"路径", r.URL.Path,
							"方法", r.Method,
							"错误", rec,
							"堆栈", string(debug.Stack()),
						)
					}
					if strings.Contains(r.Header.Get("Accept"), "application/json") {
						w.Header().Set("Content-Type", "application/json; charset=utf-8")
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = w.Write([]byte(`{"error":"服务器内部错误"}`))
						return
					}
					http.Error(w, "服务器内部错误，请稍后重试", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder 记录响应状态码与字节数。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Flush 透传 Flush，保证流式响应可用。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Logger 输出访问日志。
func Logger(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 静态资源不记录，避免噪音
			if strings.HasPrefix(r.URL.Path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			if rec.status == 0 {
				rec.status = http.StatusOK
			}
			if logger != nil {
				logger.Info("http",
					"方法", r.Method,
					"路径", r.URL.Path,
					"状态", rec.status,
					"耗时", time.Since(start).Round(time.Millisecond).String(),
					"IP", utils.ClientIP(r),
				)
			}
		})
	}
}

// SecurityHeaders 添加一组安全相关的响应头。
func SecurityHeaders(cfg *config.Config) Middleware {
	csp := strings.Join([]string{
		"default-src 'self'",
		"img-src 'self' data:",
		"style-src 'self' 'unsafe-inline'",
		"script-src 'self'",
		"font-src 'self' data:",
		"connect-src 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"object-src 'none'",
	}, "; ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Content-Security-Policy", csp)
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			if cfg.IsProduction() && strings.HasPrefix(cfg.BaseURL, "https://") {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// 会话与权限
// ---------------------------------------------------------------------------

// Authenticate 加载会话并把用户与 CSRF 令牌注入上下文。
// 未登录同样放行，由后续的 RequireAuth 决定是否拦截。
func (m *Manager) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		sess, user, err := m.sessions.Load(ctx, r)
		if err != nil {
			if m.logger != nil {
				m.logger.Error("加载会话失败", "错误", err, "路径", r.URL.Path)
			}
			http.Error(w, "服务器内部错误，请稍后重试", http.StatusInternalServerError)
			return
		}

		var csrfToken string
		if sess != nil && user != nil {
			csrfToken = sess.CSRFToken
			// 保持 cookie 与会话中的令牌同步
			if c, err := r.Cookie(auth.CSRFCookieName); err != nil || c.Value != csrfToken {
				m.writeCSRFCookie(w, csrfToken)
			}
			ctx = auth.WithSession(ctx, sess)
			ctx = auth.WithUser(ctx, user)
		} else {
			// 未登录：使用 double-submit cookie 方案保护登录 / 注册等表单
			if c, err := r.Cookie(auth.CSRFCookieName); err == nil && c.Value != "" {
				csrfToken = c.Value
			} else {
				token, err := utils.RandomToken(32)
				if err == nil {
					csrfToken = token
					m.writeCSRFCookie(w, token)
				}
			}
		}

		ctx = context.WithValue(ctx, csrfKey, csrfToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Manager) writeCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(m.cfg.RememberLifetime.Seconds()),
		HttpOnly: false, // 需允许页面 JS 读取，用于 AJAX 请求头
		Secure:   m.cfg.IsProduction() && strings.HasPrefix(m.cfg.BaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

// RequireAuth 要求登录，未登录时跳转到登录页并带上回跳地址。
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFrom(r.Context()) == nil {
			m.unauthorized(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole 要求用户具备指定角色之一。
func (m *Manager) RequireRole(roles ...models.Role) Middleware {
	allowed := make(map[models.Role]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.UserFrom(r.Context())
			if user == nil {
				m.unauthorized(w, r)
				return
			}
			if !allowed[user.Role] {
				m.forbidden(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireWrite 要求具备写权限（管理员或仓管员）。
func (m *Manager) RequireWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFrom(r.Context())
		if user == nil {
			m.unauthorized(w, r)
			return
		}
		if !user.Role.CanWrite() {
			m.forbidden(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RedirectIfAuthenticated 已登录用户访问登录 / 注册页时直接跳转到首页。
func (m *Manager) RedirectIfAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFrom(r.Context()) != nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) unauthorized(w http.ResponseWriter, r *http.Request) {
	if utils.WantsJSON(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"请先登录"}`))
		return
	}
	target := "/login"
	if r.Method == http.MethodGet && r.URL.Path != "/" {
		target += "?next=" + url.QueryEscape(r.URL.RequestURI())
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (m *Manager) forbidden(w http.ResponseWriter, r *http.Request) {
	if utils.WantsJSON(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"没有权限执行该操作"}`))
		return
	}
	http.Error(w, "403 没有权限执行该操作", http.StatusForbidden)
}

// ---------------------------------------------------------------------------
// CSRF
// ---------------------------------------------------------------------------

// 上传类请求（multipart/form-data）的解析参数统一由 auth 包维护，
// 这里只保留一个对外别名，避免调用方再去 import auth 包。
//
// ParseForm 只解析 application/x-www-form-urlencoded，对 multipart 表单
// 完全无效 —— 若不特殊处理，文件上传表单里的 _csrf 隐藏字段永远读不到，
// 所有上传都会被 403 拦下。
const (
	// MaxUploadBytes 是全站 multipart 请求的体积上限。
	MaxUploadBytes = auth.MaxUploadBytes
)

// CSRF 校验所有非幂等请求携带的令牌。
//
// 已登录：与会话中保存的令牌比对（最强）。
// 未登录：与 CSRF cookie 比对（double-submit）。
func (m *Manager) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		expected := ""
		if sess := auth.SessionFrom(r.Context()); sess != nil {
			expected = sess.CSRFToken
		} else {
			expected = CSRFFrom(r.Context())
		}

		if expected == "" {
			m.csrfFail(w, r, "会话已失效，请刷新页面后重试")
			return
		}

		provided := r.Header.Get(auth.HeaderName)
		if provided == "" {
			// multipart 的体积上限必须在解析之前套上，否则超大请求会先落满
			// 临时目录；解析本身交给 auth.ProvidedCSRFToken。
			if auth.IsMultipartForm(r) {
				r.Body = http.MaxBytesReader(w, r.Body, auth.MaxUploadBytes)
			}
			provided = auth.ProvidedCSRFToken(r)
		}

		if provided == "" || !utils.SecureCompare(provided, expected) {
			m.csrfFail(w, r, "安全校验未通过，请刷新页面后重试")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (m *Manager) csrfFail(w http.ResponseWriter, r *http.Request, msg string) {
	if m.logger != nil {
		m.logger.Warn("CSRF 校验失败", "路径", r.URL.Path, "方法", r.Method, "IP", utils.ClientIP(r))
	}
	if utils.WantsJSON(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
		return
	}
	http.Error(w, "403 "+msg, http.StatusForbidden)
}
