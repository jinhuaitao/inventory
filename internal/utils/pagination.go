package utils

import "strconv"

// Pagination 描述一页数据的切片信息，供列表页与分页组件使用。
type Pagination struct {
	Page       int   // 当前页（从 1 开始）
	PerPage    int   // 每页条数
	Total      int   // 记录总数
	TotalPages int   // 总页数
	Offset     int   // SQL OFFSET
	HasPrev    bool  // 是否有上一页
	HasNext    bool  // 是否有下一页
	PrevPage   int   // 上一页页码
	NextPage   int   // 下一页页码
	Start      int   // 当前页第一条记录的序号（从 1 开始，用于「显示第 x-y 条」）
	End        int   // 当前页最后一条记录的序号
	Pages      []int // 要渲染的页码；0 表示省略号
}

// NewPagination 根据页码、每页条数与总数构造分页对象。
// page 会被规整到合法区间；perPage 会被限制在 5-200 之间。
func NewPagination(page, perPage, total int) *Pagination {
	if perPage <= 0 {
		perPage = 20
	}
	if perPage < 5 {
		perPage = 5
	}
	if perPage > 200 {
		perPage = 200
	}
	if page < 1 {
		page = 1
	}

	totalPages := (total + perPage - 1) / perPage
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	p := &Pagination{
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
		Offset:     (page - 1) * perPage,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
		PrevPage:   page - 1,
		NextPage:   page + 1,
	}

	if total == 0 {
		p.Start, p.End = 0, 0
	} else {
		p.Start = p.Offset + 1
		p.End = p.Offset + perPage
		if p.End > total {
			p.End = total
		}
	}

	p.buildPages()
	return p
}

// buildPages 生成页码序列，超过 7 页时用 0 表示省略号。
func (p *Pagination) buildPages() {
	if p.TotalPages <= 7 {
		p.Pages = Seq(1, p.TotalPages)
		return
	}

	keep := map[int]bool{1: true, p.TotalPages: true}
	for i := p.Page - 1; i <= p.Page+1; i++ {
		if i >= 1 && i <= p.TotalPages {
			keep[i] = true
		}
	}
	// 靠近首尾时补齐，避免页码跳动
	if p.Page <= 3 {
		keep[2], keep[3], keep[4] = true, true, true
	}
	if p.Page >= p.TotalPages-2 {
		keep[p.TotalPages-1] = true
		keep[p.TotalPages-2] = true
		keep[p.TotalPages-3] = true
	}

	pages := make([]int, 0, 9)
	prev := 0
	for i := 1; i <= p.TotalPages; i++ {
		if !keep[i] {
			continue
		}
		if prev != 0 && i-prev > 1 {
			pages = append(pages, 0) // 省略号
		}
		pages = append(pages, i)
		prev = i
	}
	p.Pages = pages
}

// PageNumbers 返回不含省略号的纯页码列表（用于导出等场景）。
func (p *Pagination) PageNumbers() []int { return Seq(1, p.TotalPages) }

// ParsePage 从查询参数解析页码。
func ParsePage(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// ParsePerPage 从查询参数解析每页条数。
func ParsePerPage(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 20
	}
	switch n {
	case 10, 20, 50, 100:
		return n
	default:
		return 20
	}
}

// ParseInt64 解析可选的正整数（如筛选用的分类 ID）。
func ParseInt64(raw string) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ParseInt 解析整数，失败时返回默认值。
func ParseInt(raw string, fallback int) int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// ParseFloat 解析浮点数，失败时返回默认值。
func ParseFloat(raw string, fallback float64) float64 {
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return f
}
