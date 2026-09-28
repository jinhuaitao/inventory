package utils

import (
	"encoding/csv"
	"net/http"
	"net/url"
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
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
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
