package utils

import "testing"

func TestNewPagination(t *testing.T) {
	// 100 条记录，每页 20 条，共 5 页
	p := NewPagination(1, 20, 100)

	if p.Page != 1 || p.TotalPages != 5 || p.Total != 100 {
		t.Fatalf("分页基础字段不正确: %+v", p)
	}
	if p.HasPrev {
		t.Error("第 1 页不应有上一页")
	}
	if !p.HasNext {
		t.Error("第 1 页应有下一页")
	}
	if p.Offset != 0 {
		t.Errorf("第 1 页 Offset 应为 0，实际 %d", p.Offset)
	}
	if p.Start != 1 || p.End != 20 {
		t.Errorf("第 1 页显示区间应为 1-20，实际 %d-%d", p.Start, p.End)
	}

	// 中间页
	p = NewPagination(3, 20, 100)
	if p.Offset != 40 {
		t.Errorf("第 3 页 Offset 应为 40，实际 %d", p.Offset)
	}
	if p.Start != 41 || p.End != 60 {
		t.Errorf("第 3 页显示区间应为 41-60，实际 %d-%d", p.Start, p.End)
	}
	if !p.HasPrev || !p.HasNext {
		t.Error("中间页应同时有上一页和下一页")
	}

	// 末页
	p = NewPagination(5, 20, 100)
	if p.HasNext {
		t.Error("末页不应有下一页")
	}
	if p.End != 100 {
		t.Errorf("末页 End 应为 100，实际 %d", p.End)
	}
}

func TestPaginationEdgeCases(t *testing.T) {
	// 空结果
	p := NewPagination(1, 20, 0)
	if p.TotalPages != 1 {
		t.Errorf("无数据时总页数应为 1，实际 %d", p.TotalPages)
	}
	if p.Start != 0 || p.End != 0 {
		t.Errorf("无数据时区间应为 0-0，实际 %d-%d", p.Start, p.End)
	}

	// 页码越界应被修正
	p = NewPagination(999, 20, 50)
	if p.Page != 3 {
		t.Errorf("越界页码应修正为末页 3，实际 %d", p.Page)
	}

	p = NewPagination(-5, 20, 50)
	if p.Page != 1 {
		t.Errorf("负数页码应修正为 1，实际 %d", p.Page)
	}

	// 非整除的最后一页
	p = NewPagination(3, 20, 55)
	if p.End != 55 {
		t.Errorf("末页 End 应为实际总数 55，实际 %d", p.End)
	}

	// 每页条数被限制
	p = NewPagination(1, 0, 10)
	if p.PerPage != 20 {
		t.Errorf("每页条数为 0 时应回落到 20，实际 %d", p.PerPage)
	}
	p = NewPagination(1, 9999, 10)
	if p.PerPage != 200 {
		t.Errorf("每页条数上限应为 200，实际 %d", p.PerPage)
	}
}

func TestBuildPages(t *testing.T) {
	// 页数较少时全部展示
	p := NewPagination(1, 20, 100)
	if len(p.Pages) != 5 {
		t.Errorf("5 页时应展示全部页码，实际 %v", p.Pages)
	}

	// 页数较多时应包含省略号（0 表示）
	p = NewPagination(10, 20, 1000) // 50 页
	if len(p.Pages) == 0 {
		t.Fatal("页码列表不应为空")
	}
	hasEllipsis := false
	for _, n := range p.Pages {
		if n == 0 {
			hasEllipsis = true
		}
	}
	if !hasEllipsis {
		t.Errorf("页数较多时应包含省略号，实际 %v", p.Pages)
	}

	// 必须包含首页与末页
	if p.Pages[0] != 1 {
		t.Errorf("应包含首页，实际 %v", p.Pages)
	}
	if p.Pages[len(p.Pages)-1] != p.TotalPages {
		t.Errorf("应包含末页，实际 %v", p.Pages)
	}

	// 当前页必须在列表中
	found := false
	for _, n := range p.Pages {
		if n == p.Page {
			found = true
		}
	}
	if !found {
		t.Errorf("当前页应出现在页码列表中，实际 %v", p.Pages)
	}
}

func TestParseHelpers(t *testing.T) {
	if ParsePage("") != 1 || ParsePage("abc") != 1 || ParsePage("0") != 1 || ParsePage("-3") != 1 {
		t.Error("非法页码应回落到 1")
	}
	if ParsePage("7") != 7 {
		t.Error("合法页码解析失败")
	}

	if ParsePerPage("50") != 50 || ParsePerPage("10") != 10 || ParsePerPage("100") != 100 {
		t.Error("合法每页条数解析失败")
	}
	if ParsePerPage("33") != 20 || ParsePerPage("abc") != 20 {
		t.Error("非法每页条数应回落到 20")
	}

	if v, ok := ParseInt64("12"); !ok || v != 12 {
		t.Error("ParseInt64 解析正整数失败")
	}
	if _, ok := ParseInt64(""); ok {
		t.Error("空字符串应返回 false")
	}
	if _, ok := ParseInt64("0"); ok {
		t.Error("0 应返回 false")
	}
	if _, ok := ParseInt64("-1"); ok {
		t.Error("负数应返回 false")
	}

	if ParseInt("5", 9) != 5 || ParseInt("x", 9) != 9 {
		t.Error("ParseInt 行为不正确")
	}
	if ParseFloat("1.5", 0) != 1.5 || ParseFloat("x", 3) != 3 {
		t.Error("ParseFloat 行为不正确")
	}
}
