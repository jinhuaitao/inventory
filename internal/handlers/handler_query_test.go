package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCurrentQueryWithoutPage 分页链接要保留筛选条件、只剔除页码。
//
// 丢 Query 会让「第 2 页」变回「无筛选的第 2 页」：用户搜索后翻页，
// 列表悄悄退回全量，属于最容易漏、也最难被用户原谅的交互 bug。
func TestCurrentQueryWithoutPage(t *testing.T) {
	req := httptest.NewRequest("GET", "/users?page=3&q=%E5%BC%A0&role=admin", nil)

	q := currentQueryWithoutPage(req)

	if strings.Contains(q, "page=") {
		t.Errorf("结果不应包含 page 参数：%q", q)
	}
	for _, want := range []string{"q=", "role=admin"} {
		if !strings.Contains(q, want) {
			t.Errorf("筛选条件 %q 丢失，实际: %q", want, q)
		}
	}
}

// TestCurrentQueryWithoutPageEmpty 只有页码时返回空串，模板据此拼接分隔符。
func TestCurrentQueryWithoutPageEmpty(t *testing.T) {
	req := httptest.NewRequest("GET", "/users?page=2", nil)

	if got := currentQueryWithoutPage(req); got != "" {
		t.Errorf("仅有 page 参数时应返回空串，实际 %q", got)
	}
}
