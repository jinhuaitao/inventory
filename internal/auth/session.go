// Package auth 负责会话管理、CSRF 防护与邮件发送。
package auth

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"

	"inventory/internal/config"
	"inventory/internal/models"
	"inventory/internal/services"
	"inventory/internal/utils"
)

// Cookie 名称
const (
	// SessionCookieName 会话 cookie
	SessionCookieName = "inventory_session"
	// CSRFCookieName 与 JS 共享的 CSRF cookie（双提交校验）
	CSRFCookieName = "inventory_csrf"
	// FormFieldName 表单中 CSRF 隐藏字段名
	FormFieldName = "_csrf"
	// HeaderName CSRF 请求头名（供 AJAX 使用）
	HeaderName = "X-CSRF-Token"
)

type contextKey string

const (
	userContextKey    contextKey = "inventory.user"
	sessionContextKey contextKey = "inventory.session"
)

// WithUser 把当前用户放入请求上下文。
func WithUser(ctx context.Context, u *models.User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

// UserFrom 从上下文取出当前用户；未登录返回 nil。
func UserFrom(ctx context.Context) *models.User {
	u, _ := ctx.Value(userContextKey).(*models.User)
	return u
}

// WithSession 把当前会话放入请求上下文。
func WithSession(ctx context.Context, s *models.Session) context.Context {
	return context.WithValue(ctx, sessionContextKey, s)
}

// SessionFrom 从上下文取出当前会话。
func SessionFrom(ctx context.Context) *models.Session {
	s, _ := ctx.Value(sessionContextKey).(*models.Session)
	return s
}

// SessionManager 负责创建、加载与销毁会话。
type SessionManager struct {
	store *services.Store
	cfg   *config.Config
}

// NewSessionManager 创建会话管理器。
func NewSessionManager(store *services.Store, cfg *config.Config) *SessionManager {
	return &SessionManager{store: store, cfg: cfg}
}

// Create 为用户建立新会话，并写入 cookie。
func (m *SessionManager) Create(ctx context.Context, w http.ResponseWriter, r *http.Request, user *models.User, remember bool) (*models.Session, error) {
	token, err := utils.RandomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := utils.RandomToken(32)
	if err != nil {
		return nil, err
	}

	lifetime := m.cfg.SessionLifetime
	if remember {
		lifetime = m.cfg.RememberLifetime
	}
	expires := time.Now().Add(lifetime)

	sess := &models.Session{
		UserID:    user.ID,
		TokenHash: utils.HashToken(token),
		CSRFToken: csrf,
		ExpiresAt: expires,
		UserAgent: utils.UserAgent(r),
		IP:        utils.ClientIP(r),
	}
	if err := m.store.CreateSession(ctx, sess); err != nil {
		return nil, err
	}

	maxAge := int(lifetime.Seconds())
	m.setCookie(w, SessionCookieName, token, maxAge)
	m.setCookie(w, CSRFCookieName, csrf, maxAge)

	return sess, nil
}

// Load 依据请求 cookie 加载会话与用户。
// 返回 (nil, nil, nil) 表示「未登录」，属于正常情况。
func (m *SessionManager) Load(ctx context.Context, r *http.Request) (*models.Session, *models.User, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return nil, nil, nil
	}

	sess, user, err := m.store.GetSessionUser(ctx, utils.HashToken(c.Value))
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	// 过期会话视为未登录
	if !sess.ExpiresAt.After(time.Now()) {
		_ = m.store.DeleteSession(ctx, sess.TokenHash)
		return nil, nil, nil
	}

	// 被禁用的账号立即失效
	if !user.IsActive() {
		_ = m.store.DeleteSession(ctx, sess.TokenHash)
		return nil, nil, nil
	}

	return sess, user, nil
}

// Destroy 销毁当前会话并清除 cookie。
func (m *SessionManager) Destroy(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		if err := m.store.DeleteSession(ctx, utils.HashToken(c.Value)); err != nil {
			return err
		}
	}
	m.clearCookie(w, SessionCookieName)
	m.clearCookie(w, CSRFCookieName)
	return nil
}

// setCookie 统一设置 cookie 安全属性。
func (m *SessionManager) setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: name == SessionCookieName, // CSRF cookie 需要被 JS 读取
		Secure:   m.cfg.IsProduction() && isHTTPS(m.cfg.BaseURL),
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *SessionManager) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: name == SessionCookieName,
		Secure:   m.cfg.IsProduction() && isHTTPS(m.cfg.BaseURL),
		SameSite: http.SameSiteLaxMode,
	})
}

func isHTTPS(baseURL string) bool {
	return len(baseURL) >= 8 && baseURL[:8] == "https://"
}

// ---------------------------------------------------------------------------
// CSRF 令牌提取
// ---------------------------------------------------------------------------

// 上传类请求（multipart/form-data）的解析参数。
//
// ParseForm 只解析 application/x-www-form-urlencoded，对 multipart 表单
// 完全无效 —— 若不特殊处理，文件上传表单里的 _csrf 隐藏字段永远读不到，
// 所有上传都会被 403 拦下。
const (
	// multipartMemory 是驻留内存的上限，超出部分由标准库落到临时文件。
	multipartMemory = 8 << 20
	// MaxUploadBytes 是全站 multipart 请求的体积上限。
	// 必须在解析之前套在 body 上，否则超大请求会先被完整写入磁盘。
	MaxUploadBytes = 512 << 20
)

// IsMultipartForm 判断请求是否为 multipart/form-data。
func IsMultipartForm(r *http.Request) bool {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && ct == "multipart/form-data"
}

// ProvidedCSRFToken 取出客户端提交的 CSRF 令牌：先看请求头，再看表单字段。
//
// 必须区分 multipart 与 urlencoded：ParseForm 不会解析 multipart 的 body，
// 直接拿它取值会让所有文件上传表单被误判为「缺少 CSRF 令牌」。
//
// 调用方若想限制上传体积，必须在调用本函数之前用 http.MaxBytesReader 包住
// r.Body —— 解析发生在本函数内部，包晚了就形同虚设。
func ProvidedCSRFToken(r *http.Request) string {
	if v := r.Header.Get(HeaderName); v != "" {
		return v
	}

	if IsMultipartForm(r) {
		// 已解析过就直接复用；标准库的 ParseMultipartForm 本身是幂等的，
		// 这里显式判断只是为了避免重复套一层解析。
		if r.MultipartForm == nil {
			if err := r.ParseMultipartForm(multipartMemory); err != nil {
				return ""
			}
		}
		return r.FormValue(FormFieldName)
	}

	if err := r.ParseForm(); err != nil {
		return ""
	}
	return r.FormValue(FormFieldName)
}

// CheckCSRF 校验请求携带的 CSRF 令牌是否与会话中的一致。
func CheckCSRF(r *http.Request, sess *models.Session) error {
	if sess == nil {
		return fmt.Errorf("会话不存在")
	}

	provided := ProvidedCSRFToken(r)
	if provided == "" {
		return fmt.Errorf("缺少 CSRF 令牌")
	}
	if !utils.SecureCompare(provided, sess.CSRFToken) {
		return fmt.Errorf("CSRF 令牌校验失败")
	}
	return nil
}
