package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildDSNEncodesReservedCharacters DSN 里路径的保留字符必须被编码。
//
// DSN 的形状是 file:<路径>?<参数>。路径里只要出现 `?` 或 `#`，
// 后面的一切都会被当成查询串 / 片段，五个 _pragma 与 _txlock 全部失效 ——
// 数据库会以「没有 WAL、没有外键、没有忙等待」的默认姿态打开。
func TestBuildDSNEncodesReservedCharacters(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"问号", "/data/we?ird/inventory.db"},
		{"井号", "/data/we#ird/inventory.db"},
		{"百分号", "/data/100%/inventory.db"},
		{"三者都有", "/data/a?b#c%d/inventory.db"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dsn := buildDSN(tc.path)

			if !strings.HasPrefix(dsn, "file:") {
				t.Fatalf("DSN 应以 file: 开头，实际 %q", dsn)
			}

			idx := strings.Index(dsn, "?_pragma=")
			if idx < 0 {
				t.Fatalf("DSN 缺少参数段，实际 %q", dsn)
			}

			// 关键断言：**路径段**里不允许残留未编码的 `?` 或 `#`。
			// 只检查「参数是否出现」是不够的 —— 路径未编码时参数照样在
			// 字符串里，只是变成了路径的一部分，pragma 一条都不会生效。
			//
			// 这里刻意不检查 `%`：编码**之后**的路径本来就含 `%`（如 %3F），
			// 把它一起禁掉等于把正确结果判成错误。
			pathPart := dsn[len("file:"):idx]
			if strings.ContainsAny(pathPart, "?#") {
				t.Errorf("路径段仍含未编码的 ? 或 #: %q（完整 DSN: %q）", pathPart, dsn)
			}

			params := dsn[idx:]
			for _, want := range []string{
				"_pragma=busy_timeout(5000)",
				"_pragma=journal_mode(WAL)",
				"_pragma=foreign_keys(1)",
				"_pragma=synchronous(NORMAL)",
				"_txlock=immediate",
			} {
				if !strings.Contains(params, want) {
					t.Errorf("参数 %q 缺失，DSN = %q", want, dsn)
				}
			}
		})
	}
}

// TestBuildDSNLeavesOrdinaryPathsAlone 普通路径不应被改动。
func TestBuildDSNLeavesOrdinaryPathsAlone(t *testing.T) {
	dsn := buildDSN("/var/lib/inventory/inventory.db")
	want := "file:/var/lib/inventory/inventory.db?_pragma=busy_timeout(5000)"
	if !strings.HasPrefix(dsn, want) {
		t.Errorf("DSN = %q，期望以 %q 开头", dsn, want)
	}
}

// TestOpenWithReservedCharactersAppliesPragmas 是上面那条的**行为级**验证。
//
// 光检查 DSN 字符串还不够 —— 真正要确认的是驱动能把编码后的路径解回来，
// 并且 pragma 确实生效。这里在名字里带 `?` 和 `#` 的目录下真开一个库，
// 逐条核对 WAL 与外键开关。
func TestOpenWithReservedCharactersAppliesPragmas(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "we?ird#dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建含保留字符的目录失败: %v", err)
	}
	path := filepath.Join(dir, "inventory.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("在含保留字符的路径下打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("读取 journal_mode 失败: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode = %q，期望 wal（说明 _pragma 参数没生效）", mode)
	}

	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("读取 foreign_keys 失败: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d，期望 1（说明 _pragma 参数没生效）", fk)
	}

	// 数据库文件必须真的落在那个目录里，而不是被当成别的路径
	if _, err := os.Stat(path); err != nil {
		t.Errorf("数据库文件未出现在预期路径 %s: %v", path, err)
	}
}
