package utils

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// DisplayZone 是界面展示使用的时区（数据库统一存 UTC）。
var DisplayZone = time.FixedZone("UTC+8", 8*3600)

// Local 把时间转换到展示时区。
func Local(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.In(DisplayZone)
}

// FormatDateTime 输出 2006-01-02 15:04:05。
func FormatDateTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return Local(t).Format("2006-01-02 15:04:05")
}

// FormatTime 输出 2006-01-02 15:04。
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return Local(t).Format("2006-01-02 15:04")
}

// FormatDate 输出 2006-01-02。
func FormatDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return Local(t).Format("2006-01-02")
}

// FormatDateTimePtr 处理可空时间。
func FormatDateTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "从未登录"
	}
	return FormatDateTime(*t)
}

// RelativeTime 返回「3 分钟前」这类相对时间描述。
func RelativeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return FormatDateTime(t)
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	default:
		return FormatDate(t)
	}
}

// FormatMoney 输出带千分位的金额，例如 ¥12,345.60。
func FormatMoney(v float64) string {
	neg := v < 0
	v = math.Abs(v)
	s := strconv.FormatFloat(v, 'f', 2, 64)
	intPart, decPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, decPart = s[:i], s[i+1:]
	}
	intPart = groupThousands(intPart)
	out := "¥" + intPart + "." + decPart
	if neg {
		out = "-" + out
	}
	return out
}

// FormatMoneyPlain 输出不带货币符号的千分位金额。
func FormatMoneyPlain(v float64) string {
	return strings.TrimPrefix(FormatMoney(v), "¥")
}

// FormatNumber 输出带千分位的整数。
func FormatNumber(v int) string {
	neg := v < 0
	if neg {
		v = -v
	}
	out := groupThousands(strconv.Itoa(v))
	if neg {
		out = "-" + out
	}
	return out
}

// FormatFloat 保留 n 位小数。
func FormatFloat(v float64, n int) string {
	return strconv.FormatFloat(v, 'f', n, 64)
}

// groupThousands 给整数部分插入千分位分隔符。
// FormatBytes 把字节数格式化为易读的体积文本，例如 "14.2 MB"。
func FormatBytes(n int64) string {
	if n < 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}

	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(n)
	i := -1
	for value >= unit && i < len(units)-1 {
		value /= unit
		i++
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + units[i]
}

func groupThousands(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if n > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// Percent 计算百分比（0-100 之间的浮点数）。
func Percent(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}

// Ratio 返回 part/total，范围限制在 [0,1]，用于进度条宽度。
func Ratio(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	r := float64(part) / float64(total)
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

// Seq 返回 [start, end] 的整数切片，供模板中做简单循环。
func Seq(start, end int) []int {
	if end < start {
		return nil
	}
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

// Add 模板中的加法。
func Add(a, b int) int { return a + b }

// Sub 模板中的减法。
func Sub(a, b int) int { return a - b }

// Mul 模板中的乘法。
func Mul(a, b int) int { return a * b }

// Max 返回较大值。
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Min 返回较小值。
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// BarHeight 把数值换算为柱状图高度百分比（0-100）。
func BarHeight(v, max int) float64 {
	if max <= 0 || v <= 0 {
		return 0
	}
	h := float64(v) / float64(max) * 100
	if h < 2 {
		return 2 // 保证极小值也能看到
	}
	return h
}

// Initial 返回字符串的首字符（支持中文），用于头像。
func Initial(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "?"
	}
	return string([]rune(s)[0])
}
