package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"inventory/internal/auth"
	"inventory/internal/config"
	"inventory/internal/models"
)

// ---------------------------------------------------------------------------
// 测试脚手架
// ---------------------------------------------------------------------------

// testManager 构造一个只跑「未登录 double-submit」路径的中间件管理器。
//
// 请求不带会话 cookie 时 SessionManager.Load 会立刻返回「未登录」，
// 根本不会碰 store，所以这里传 nil 是安全的。
func testManager() *Manager {
	cfg := &config.Config{RememberLifetime: time.Hour}
	return New(auth.NewSessionManager(nil, cfg), cfg, nil)
}

// newChain 组装 Authenticate → CSRF → next，并返回是否抵达 next 的记录器。
//
// 顺序与 cmd/server 中的真实链路一致：先由 Authenticate 把 CSRF 令牌放进
// 上下文（未登录时取自 double-submit cookie），再由 CSRF 做校验。
func newChain(t *testing.T) (*Manager, *bool, http.Handler) {
	t.Helper()
	m := testManager()
	reached := new(bool)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
	return m, reached, Chain(next, m.Authenticate, m.CSRF)
}

// multipartBody 构造一个带 _csrf 字段与一个文件字段的 multipart 请求体，
// 形状与「数据维护」页的备份恢复表单完全一致。
func multipartBody(t *testing.T, csrfToken, fileField, filename, payload string) (io.Reader, string) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if csrfToken != "" {
		if err := mw.WriteField(auth.FormFieldName, csrfToken); err != nil {
			t.Fatalf("写入 %s 字段失败: %v", auth.FormFieldName, err)
		}
	}
	if fileField != "" {
		part, err := mw.CreateFormFile(fileField, filename)
		if err != nil {
			t.Fatalf("创建文件字段 %s 失败: %v", fileField, err)
		}
		if _, err := part.Write([]byte(payload)); err != nil {
			t.Fatalf("写入文件内容失败: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart writer 失败: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

// doMultipart 发一个 multipart 请求，返回响应记录器。
//
// sessionToken 非空时，先往上下文注入一个已登录会话（其 CSRF 令牌即该值），
// 模拟真实的「管理员在数据维护页提交恢复」链路；为空则表示未登录请求。
//
// ⚠️ 必须显式区分这两种情形：CSRF 中间件对未登录的 multipart 请求会直接
// 拒绝（见 TestCSRFRejectsUnauthenticatedMultipartBeforeParsing），
// 若不注入会话，下面所有用例都会因为「未登录」被拦下，
// 从而**永远测不到令牌比对逻辑本身**。
func doMultipart(t *testing.T, chain http.Handler, sessionToken, csrfToken, fileField, payload string) *httptest.ResponseRecorder {
	t.Helper()

	body, contentType := multipartBody(t, csrfToken, fileField, "inventory.db", payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/restore", body)
	req.Header.Set("Content-Type", contentType)
	if csrfToken != "" {
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrfToken})
	}
	if sessionToken != "" {
		req = req.WithContext(auth.WithSession(req.Context(),
			&models.Session{CSRFToken: sessionToken}))
	}

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	return rec
}

// countingReader 统计 body 被读取的次数与字节数。
//
// 用来客观证明「未登录的 multipart 请求在解析之前就被拒绝了」——
// 光看状态码 403 是不够的，因为校验失败同样返回 403，
// 而两者对服务端的代价天差地别（后者已经把整包数据落盘了）。
type countingReader struct {
	r     io.Reader
	reads int
	bytes int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads++
	n, err := c.r.Read(p)
	c.bytes += int64(n)
	return n, err
}

// ---------------------------------------------------------------------------
// 回归测试：multipart 表单里的 _csrf 必须能被读到
// ---------------------------------------------------------------------------

// TestCSRFAllowsMultipartUpload 是本次修复的核心回归测试。
//
// 修复前 CSRF 中间件统一用 r.ParseForm() 取令牌，而 ParseForm 只解析
// application/x-www-form-urlencoded，对 multipart/form-data 完全无效 ——
// 文件上传表单里的 _csrf 隐藏字段永远读不到，导致所有上传（包括
// 「数据维护」页的备份恢复）恒定 403。
//
// 注意这里必须带**已登录会话**：站内所有 multipart 接口都要求登录，
// 未登录的 multipart 请求会在解析前被直接拒绝（另一条独立的安全约束）。
func TestCSRFAllowsMultipartUpload(t *testing.T) {
	const token = "test-csrf-token"

	_, reached, chain := newChain(t)
	rec := doMultipart(t, chain, token, token, "backup", "SQLite format 3\x00payload")

	if rec.Code != http.StatusOK {
		t.Fatalf("multipart 表单携带正确 _csrf 时应放行，实际状态码 %d，响应体 %q",
			rec.Code, rec.Body.String())
	}
	if !*reached {
		t.Fatal("CSRF 中间件没有把请求交给后续 handler")
	}
}

// TestCSRFMultipartKeepsUploadedFile 确认中间件解析 multipart 之后，
// 业务 handler 仍能读到上传的文件内容。
//
// 标准库的 ParseMultipartForm 是幂等的：中间件为了取 _csrf 先解析一次，
// handler 再调用一次会直接复用缓存，既不会重复读 body，也不会丢文件。
func TestCSRFMultipartKeepsUploadedFile(t *testing.T) {
	const token = "test-csrf-token"
	const payload = "SQLite format 3\x00restore-me"

	m := testManager()
	var gotBody []byte
	var gotName string

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("业务 handler 再次解析表单失败: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		file, header, err := r.FormFile("backup")
		if err != nil {
			t.Errorf("业务 handler 读取上传文件失败: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer file.Close()

		gotName = header.Filename
		if gotBody, err = io.ReadAll(file); err != nil {
			t.Errorf("读取上传内容失败: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	body, contentType := multipartBody(t, token, "backup", "inventory.db", payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/restore", body)
	req.Header.Set("Content-Type", contentType)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})
	// 注入已登录会话：未登录的 multipart 会在解析前被拒。
	req = req.WithContext(auth.WithSession(req.Context(),
		&models.Session{CSRFToken: token}))

	rec := httptest.NewRecorder()
	Chain(next, m.Authenticate, m.CSRF).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("应放行，实际状态码 %d", rec.Code)
	}
	if gotName != "inventory.db" {
		t.Errorf("文件名应为 inventory.db，实际 %q", gotName)
	}
	if string(gotBody) != payload {
		t.Errorf("上传内容应为 %q，实际 %q", payload, string(gotBody))
	}
}

// ---------------------------------------------------------------------------
// 应当被拒绝的情形
// ---------------------------------------------------------------------------

func TestCSRFRejectsMultipartWithoutToken(t *testing.T) {
	const token = "test-csrf-token"

	_, reached, chain := newChain(t)
	// 有文件、没有 _csrf 字段。
	rec := doMultipart(t, chain, token, "", "backup", "payload")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("multipart 表单缺少 _csrf 时应返回 403，实际 %d", rec.Code)
	}
	if *reached {
		t.Fatal("校验未通过却把请求交给了后续 handler")
	}
}

func TestCSRFRejectsMultipartWithWrongToken(t *testing.T) {
	_, reached, chain := newChain(t)
	rec := doMultipart(t, chain, "cookie-token", "form-token", "backup", "payload")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("_csrf 与会话令牌不一致时应返回 403，实际 %d", rec.Code)
	}
	if *reached {
		t.Fatal("校验未通过却把请求交给了后续 handler")
	}
}

// TestCSRFRejectsMalformedMultipart 确认畸形的 multipart body 只是被拒绝，
// 而不会 panic 或放行。
func TestCSRFRejectsMalformedMultipart(t *testing.T) {
	const token = "test-csrf-token"

	_, reached, chain := newChain(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/restore",
		strings.NewReader("这不是合法的 multipart body"))
	// 声明成 multipart 但 boundary 根本不存在。
	req.Header.Set("Content-Type", "multipart/form-data; boundary=doesnotexist")
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})
	// 注入已登录会话，确保测到的是「body 畸形」而不是「未登录」。
	req = req.WithContext(auth.WithSession(req.Context(),
		&models.Session{CSRFToken: token}))

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("畸形 multipart 应返回 403，实际 %d", rec.Code)
	}
	if *reached {
		t.Fatal("畸形请求不应抵达后续 handler")
	}
}

// TestCSRFRejectsUnauthenticatedMultipartBeforeParsing 锁定一条重要的安全约束：
// **未登录的 multipart 请求必须在解析 body 之前就被拒绝**。
//
// 背景：CSRF 中间件跑在鉴权之前，而未登录的请求同样可以带 multipart body。
// 若不做判断就交给 ParseMultipartForm，任何人都能在没有任何凭据的情况下
// 发起一次 512MB 的上传，让服务端把整包数据落盘 —— 磁盘与 IO 直接被打满。
// 更糟的是「CSRF 校验失败」发生在解析**之后**，也就是说校验根本拦不住这个成本。
//
// 因此这里不能只看状态码（两种路径都是 403），必须用计数 reader
// 客观证明 body 一个字节都没被读过。
func TestCSRFRejectsUnauthenticatedMultipartBeforeParsing(t *testing.T) {
	body, contentType := multipartBody(t, "whatever", "backup", "inventory.db",
		strings.Repeat("A", 1<<20)) // 1MB 载荷，足以在计数上留下明显痕迹

	counting := &countingReader{r: body}
	req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/restore", counting)
	req.Header.Set("Content-Type", contentType)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "whatever"})
	// 故意不注入会话 —— 模拟匿名攻击者。

	_, reached, chain := newChain(t)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("未登录的 multipart 请求应返回 403，实际 %d", rec.Code)
	}
	if *reached {
		t.Fatal("未登录的 multipart 请求不应抵达后续 handler")
	}
	if counting.reads != 0 || counting.bytes != 0 {
		t.Errorf("未登录的 multipart body 不应被读取，实际读取 %d 次 / %d 字节",
			counting.reads, counting.bytes)
	}
}

// ---------------------------------------------------------------------------
// 其他提交方式不能被这次改动破坏
// ---------------------------------------------------------------------------

// TestCSRFAllowsURLEncodedFormField 确认传统的 urlencoded 表单仍然可用
// （站内绝大多数表单走的是这条路径）。
func TestCSRFAllowsURLEncodedFormField(t *testing.T) {
	const token = "test-csrf-token"

	_, reached, chain := newChain(t)
	form := url.Values{auth.FormFieldName: {token}}
	req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/backup",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !*reached {
		t.Fatalf("urlencoded 表单携带正确 _csrf 时应放行，实际状态码 %d", rec.Code)
	}
}

// TestCSRFAllowsHeaderToken 确认 AJAX 走请求头的路径可用。
func TestCSRFAllowsHeaderToken(t *testing.T) {
	const token = "test-csrf-token"

	_, reached, chain := newChain(t)
	req := httptest.NewRequest(http.MethodPost, "/api/products",
		strings.NewReader(`{"name":"螺丝"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.HeaderName, token)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !*reached {
		t.Fatalf("携带正确 %s 请求头时应放行，实际状态码 %d", auth.HeaderName, rec.Code)
	}
}

// TestCSRFHeaderPathDoesNotConsumeBody 确认带请求头时不会去解析表单，
// 否则 JSON 请求体会被 ParseForm 提前读空，业务 handler 拿到空 body。
func TestCSRFHeaderPathDoesNotConsumeBody(t *testing.T) {
	const token = "test-csrf-token"
	const payload = `{"name":"螺丝"}`

	m := testManager()
	var gotBody []byte

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/products", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.HeaderName, token)
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})

	rec := httptest.NewRecorder()
	Chain(next, m.Authenticate, m.CSRF).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("应放行，实际状态码 %d", rec.Code)
	}
	if string(gotBody) != payload {
		t.Errorf("业务 handler 应读到完整 JSON body，实际 %q", string(gotBody))
	}
}

// TestCSRFSkipsSafeMethods 确认 GET / HEAD / OPTIONS 不做校验。
func TestCSRFSkipsSafeMethods(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			_, reached, chain := newChain(t)
			// 故意不带任何令牌。
			req := httptest.NewRequest(method, "/admin/maintenance", nil)
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK || !*reached {
				t.Fatalf("%s 不应做 CSRF 校验，实际状态码 %d", method, rec.Code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 已登录：以会话中的令牌为准
// ---------------------------------------------------------------------------

// TestCSRFUsesSessionTokenWhenLoggedIn 确认已登录时校验的是会话令牌，
// double-submit cookie 里的旧值无法顶替。
func TestCSRFUsesSessionTokenWhenLoggedIn(t *testing.T) {
	const sessionToken = "session-csrf-token"
	const staleCookie = "stale-csrf-cookie"

	post := func(t *testing.T, formToken string) *httptest.ResponseRecorder {
		t.Helper()
		m := testManager()
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		form := url.Values{auth.FormFieldName: {formToken}}
		req := httptest.NewRequest(http.MethodPost, "/admin/maintenance/backup",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: staleCookie})

		// 直接注入会话，模拟 Authenticate 已完成登录态加载。
		ctx := auth.WithSession(req.Context(), &models.Session{CSRFToken: sessionToken})
		req = req.WithContext(ctx)

		rec := httptest.NewRecorder()
		m.CSRF(next).ServeHTTP(rec, req)
		return rec
	}

	if rec := post(t, sessionToken); rec.Code != http.StatusOK {
		t.Fatalf("表单令牌与会话令牌一致时应放行，实际状态码 %d", rec.Code)
	}
	if rec := post(t, staleCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("仅 cookie 旧值匹配会话令牌时应返回 403，实际 %d", rec.Code)
	}
}
