package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testService() *Service {
	return New(Options{
		Enabled: true,
		Repo:    "octocat/inventory",
		Current: "v1.0.0",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// ---------------------------------------------------------------------------
// SSRF 防护
// ---------------------------------------------------------------------------

func TestCheckHost(t *testing.T) {
	svc := testService()

	allowed := []string{
		"https://github.com/octocat/inventory/releases/download/v1.0.0/pkg.tar.gz",
		"https://api.github.com/repos/octocat/inventory/releases/latest",
		"https://objects.githubusercontent.com/some/path",
		"https://release-assets.githubusercontent.com/asset",
		"https://codeload.github.com/octocat/inventory/tar.gz/v1.0.0",
		"https://GitHub.com/octocat/inventory",
	}

	for _, u := range allowed {
		t.Run("允许 "+u, func(t *testing.T) {
			if err := svc.checkHost(u); err != nil {
				t.Errorf("应允许该地址，实际报错: %v", err)
			}
		})
	}

	blocked := []struct {
		name string
		url  string
	}{
		{"明文 HTTP 被拒绝", "http://github.com/octocat/inventory"},
		{"内网地址被拒绝", "https://127.0.0.1/evil"},
		{"localhost 被拒绝", "https://localhost/evil"},
		{"云元数据地址被拒绝", "https://169.254.169.254/latest/meta-data/"},
		{"私有网段被拒绝", "https://192.168.1.1/evil"},
		{"伪装域名被拒绝", "https://github.com.evil.example/evil"},
		{"后缀伪装被拒绝", "https://notgithubusercontent.com/evil"},
		{"其他域名被拒绝", "https://example.com/evil"},
		{"ftp 协议被拒绝", "ftp://github.com/evil"},
		{"file 协议被拒绝", "file:///etc/passwd"},
		{"空地址被拒绝", ""},
	}

	for _, tc := range blocked {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.checkHost(tc.url); err == nil {
				t.Errorf("应拒绝 %q，但校验通过了", tc.url)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 校验和解析
// ---------------------------------------------------------------------------

func TestParseChecksum(t *testing.T) {
	const target = "inventory-server-v1.0.0-linux-amd64.tar.gz"
	const hash = "8f8f52c6649542cf027bbc9b9c68d1ec042f9f34808a40413f0b8b3f66f3caa4"

	t.Run("标准两空格格式", func(t *testing.T) {
		content := hash + "  " + target + "\n"
		got, ok := parseChecksum(content, target)
		if !ok || got != hash {
			t.Errorf("got (%q, %v)", got, ok)
		}
	})

	t.Run("二进制模式星号前缀", func(t *testing.T) {
		content := hash + " *" + target + "\n"
		got, ok := parseChecksum(content, target)
		if !ok || got != hash {
			t.Errorf("got (%q, %v)", got, ok)
		}
	})

	t.Run("多行中定位目标", func(t *testing.T) {
		content := strings.Join([]string{
			"aaaa  inventory-server-v1.0.0-darwin-arm64.tar.gz",
			hash + "  " + target,
			"bbbb  checksums.txt",
			"",
		}, "\n")
		got, ok := parseChecksum(content, target)
		if !ok || got != hash {
			t.Errorf("got (%q, %v)", got, ok)
		}
	})

	t.Run("目标不存在", func(t *testing.T) {
		content := "aaaa  other-file.tar.gz\n"
		if _, ok := parseChecksum(content, target); ok {
			t.Error("不应匹配到目标文件")
		}
	})

	t.Run("忽略注释与空行", func(t *testing.T) {
		content := "# 校验和文件\n\n" + hash + "  " + target + "\n"
		if _, ok := parseChecksum(content, target); !ok {
			t.Error("应忽略注释行后匹配成功")
		}
	})

	t.Run("只有文件名没有哈希不匹配", func(t *testing.T) {
		if _, ok := parseChecksum(target+"\n", target); ok {
			t.Error("单字段行不应被当作有效记录")
		}
	})

	t.Run("空内容", func(t *testing.T) {
		if _, ok := parseChecksum("", target); ok {
			t.Error("空内容不应匹配")
		}
	})
}

// ---------------------------------------------------------------------------
// Apply 前置条件
// ---------------------------------------------------------------------------

func TestApplyPreconditions(t *testing.T) {
	svc := testService()
	ctx := context.Background()

	t.Run("没有可用更新", func(t *testing.T) {
		_, err := svc.Apply(ctx, &Status{UpdateAvailable: false})
		if err == nil || !strings.Contains(err.Error(), "没有可用的更新") {
			t.Errorf("期望「没有可用的更新」，实际 %v", err)
		}
	})

	t.Run("缺少平台安装包", func(t *testing.T) {
		_, err := svc.Apply(ctx, &Status{UpdateAvailable: true})
		if err == nil || !strings.Contains(err.Error(), "安装包") {
			t.Errorf("期望提示缺少安装包，实际 %v", err)
		}
	})

	t.Run("缺少校验和文件时拒绝更新", func(t *testing.T) {
		_, err := svc.Apply(ctx, &Status{
			UpdateAvailable: true,
			Asset:           &Asset{Name: "pkg.tar.gz", DownloadURL: "https://github.com/x/y/pkg.tar.gz"},
		})
		if err == nil || !strings.Contains(err.Error(), "checksums.txt") {
			t.Errorf("缺少校验和时应中止，实际 %v", err)
		}
	})

	t.Run("nil 状态使用缓存", func(t *testing.T) {
		_, err := svc.Apply(ctx, nil)
		if err == nil {
			t.Error("缓存中无可用更新时应报错")
		}
	})
}

// ---------------------------------------------------------------------------
// 解压
// ---------------------------------------------------------------------------

// makeTarGz 构造一个包含指定文件的 tar.gz。
func makeTarGz(t *testing.T, name string, content []byte) string {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0o755,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("写入 tar 头失败: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("写入 tar 内容失败: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("关闭 tar 失败: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("关闭 gzip 失败: %v", err)
	}

	path := filepath.Join(t.TempDir(), name+".tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("写入安装包失败: %v", err)
	}
	return path
}

// makeZip 构造一个包含指定文件的 zip。
func makeZip(t *testing.T, name string, content []byte) string {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("创建 zip 条目失败: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("写入 zip 内容失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}

	path := filepath.Join(t.TempDir(), name+".zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("写入安装包失败: %v", err)
	}
	return path
}

func TestExtractBinary(t *testing.T) {
	payload := []byte("#!/bin/sh\necho hello\n")

	t.Run("从 tar.gz 解压", func(t *testing.T) {
		archive := makeTarGz(t, "inventory-server-v1.0.0-linux-amd64", payload)
		dest := filepath.Join(t.TempDir(), "out")

		if err := extractBinary(archive, dest); err != nil {
			t.Fatalf("解压失败: %v", err)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("读取解压结果失败: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("解压内容 = %q, 期望 %q", got, payload)
		}

		info, err := os.Stat(dest)
		if err != nil {
			t.Fatalf("stat 失败: %v", err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("解压出的文件应具备可执行权限，实际 %v", info.Mode().Perm())
		}
	})

	t.Run("从 zip 解压", func(t *testing.T) {
		archive := makeZip(t, "inventory-server-v1.0.0-windows-amd64.exe", payload)
		dest := filepath.Join(t.TempDir(), "out")

		if err := extractBinary(archive, dest); err != nil {
			t.Fatalf("解压失败: %v", err)
		}
		got, _ := os.ReadFile(dest)
		if !bytes.Equal(got, payload) {
			t.Errorf("解压内容 = %q, 期望 %q", got, payload)
		}
	})

	t.Run("空归档返回错误", func(t *testing.T) {
		archive := makeTarGz(t, "empty", []byte{})
		// 大小为 0 的条目会被跳过
		if err := extractBinary(archive, filepath.Join(t.TempDir(), "out")); err == nil {
			t.Error("空归档应返回错误")
		}
	})

	t.Run("损坏的归档返回错误", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "broken.tar.gz")
		if err := os.WriteFile(path, []byte("not a gzip file"), 0o644); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		if err := extractBinary(path, filepath.Join(t.TempDir(), "out")); err == nil {
			t.Error("损坏的归档应返回错误")
		}
	})
}

func TestWriteExecutableRejectsEmpty(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	if err := writeExecutable(dest, bytes.NewReader(nil)); err == nil {
		t.Error("写入空内容应返回错误")
	}
}

func TestShortHash(t *testing.T) {
	if got := shortHash("abc"); got != "abc" {
		t.Errorf("短哈希应原样返回，实际 %q", got)
	}
	long := "8f8f52c6649542cf027bbc9b9c68d1ec042f9f34808a40413f0b8b3f66f3caa4"
	got := shortHash(long)
	if !strings.HasPrefix(long, strings.TrimSuffix(got, "…")) {
		t.Errorf("短哈希应为前缀，实际 %q", got)
	}
}

func TestEnabledAndRepo(t *testing.T) {
	t.Run("配置完整时启用", func(t *testing.T) {
		svc := New(Options{Enabled: true, Repo: "octocat/inventory"}, nil)
		if !svc.Enabled() {
			t.Error("应处于启用状态")
		}
		if svc.Repo() != "octocat/inventory" {
			t.Errorf("Repo() = %q", svc.Repo())
		}
	})

	t.Run("仓库格式不合法时不启用", func(t *testing.T) {
		svc := New(Options{Enabled: true, Repo: "inventory"}, nil)
		if svc.Enabled() {
			t.Error("缺少 owner 时不应启用")
		}
	})

	t.Run("显式关闭", func(t *testing.T) {
		svc := New(Options{Enabled: false, Repo: "octocat/inventory"}, nil)
		if svc.Enabled() {
			t.Error("Enabled=false 时不应启用")
		}
	})

	t.Run("初始状态可读取", func(t *testing.T) {
		svc := New(Options{Enabled: true, Repo: "octocat/inventory", Current: "v1.0.0"}, nil)
		st := svc.Cached()
		if st.Current != "v1.0.0" {
			t.Errorf("Current = %q", st.Current)
		}
		if st.Platform == "" {
			t.Error("Platform 不应为空")
		}
	})
}
