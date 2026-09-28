package updater

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 附件匹配：裸二进制发布形态
// ---------------------------------------------------------------------------

// 取自 jinhuaitao/inventory 的 v1.0.01 正式发布，用于锁定真实回归场景。
var releaseAssetsV1001 = []Asset{
	{
		Name:        "inventory-server-amd64",
		Size:        15052960,
		DownloadURL: "https://github.com/jinhuaitao/inventory/releases/download/v1.0.01/inventory-server-amd64",
		Digest:      "sha256:013cbdab2509dda257830f9aee87f3a3883fa789fef23ddaf5e4759f7abdca21",
	},
	{
		Name:        "inventory-server-arm64",
		Size:        14287008,
		DownloadURL: "https://github.com/jinhuaitao/inventory/releases/download/v1.0.01/inventory-server-arm64",
		Digest:      "sha256:cca98fd32a09af9d3e5ff8144ddda0e3b12f904e32c5ea32010d874e5c772fb4",
	},
}

func TestMatchAssetRawBinary(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		goarch string
		want   string
	}{
		{"linux-amd64 命中裸二进制", "linux", "amd64", "inventory-server-amd64"},
		{"linux-arm64 命中裸二进制", "linux", "arm64", "inventory-server-arm64"},
		{"darwin-amd64 不误装 Linux 产物", "darwin", "amd64", ""},
		{"darwin-arm64 不误装 Linux 产物", "darwin", "arm64", ""},
		{"windows-amd64 不误装 Linux 产物", "windows", "amd64", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchAsset(releaseAssetsV1001, tc.goos, tc.goarch)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("期望匹配不到安装包，实际得到 %q", got.Name)
				}
				return
			}
			if got == nil {
				t.Fatalf("期望匹配到 %q，实际为 nil", tc.want)
			}
			if got.Name != tc.want {
				t.Errorf("匹配到 %q，期望 %q", got.Name, tc.want)
			}
		})
	}
}

func TestMatchAssetPrefersArchive(t *testing.T) {
	assets := []Asset{
		{Name: "inventory-server-amd64"},
		{Name: "inventory-server-v1.0.0-linux-amd64.tar.gz"},
		{Name: "inventory-server-linux-amd64"},
	}

	t.Run("归档优先于裸二进制", func(t *testing.T) {
		got := matchAsset(assets, "linux", "amd64")
		if got == nil || got.Name != "inventory-server-v1.0.0-linux-amd64.tar.gz" {
			t.Errorf("应优先选中归档，实际 %v", got)
		}
	})

	t.Run("带系统名的裸二进制优先于仅带架构名", func(t *testing.T) {
		only := []Asset{
			{Name: "inventory-server-amd64"},
			{Name: "inventory-server-linux-amd64"},
		}
		got := matchAsset(only, "linux", "amd64")
		if got == nil || got.Name != "inventory-server-linux-amd64" {
			t.Errorf("应优先选中带系统名的裸二进制，实际 %v", got)
		}
	})

	t.Run("Windows 使用 zip 后缀", func(t *testing.T) {
		win := []Asset{{Name: "inventory-server-v1.0.0-windows-amd64.zip"}}
		got := matchAsset(win, "windows", "amd64")
		if got == nil || got.Name != "inventory-server-v1.0.0-windows-amd64.zip" {
			t.Errorf("应命中 Windows 归档，实际 %v", got)
		}
	})

	t.Run("空附件列表返回 nil", func(t *testing.T) {
		if got := matchAsset(nil, "linux", "amd64"); got != nil {
			t.Errorf("期望 nil，实际 %v", got)
		}
	})
}

func TestAssetKind(t *testing.T) {
	tests := []struct {
		name string
		want Kind
	}{
		{"inventory-server-v1.0.0-linux-amd64.tar.gz", KindTarGz},
		{"pkg.tgz", KindTarGz},
		{"inventory-server-v1.0.0-windows-amd64.zip", KindZip},
		{"inventory-server-amd64", KindBinary},
		{"inventory-server-linux-arm64", KindBinary},
		{"", KindUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := (&Asset{Name: tc.name}).Kind(); got != tc.want {
				t.Errorf("Kind() = %v，期望 %v", got, tc.want)
			}
		})
	}

	t.Run("nil 附件", func(t *testing.T) {
		var a *Asset
		if got := a.Kind(); got != KindUnknown {
			t.Errorf("Kind() = %v，期望 KindUnknown", got)
		}
	})

	t.Run("中文描述", func(t *testing.T) {
		if got := KindBinary.String(); got != "裸可执行文件" {
			t.Errorf("String() = %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// 校验和来源
// ---------------------------------------------------------------------------

func TestParseDigest(t *testing.T) {
	const valid = "013cbdab2509dda257830f9aee87f3a3883fa789fef23ddaf5e4759f7abdca21"

	tests := []struct {
		name   string
		digest string
		wantOK bool
	}{
		{"标准 sha256 前缀", "sha256:" + valid, true},
		{"大小写不敏感", "SHA256:" + strings.ToUpper(valid), true},
		{"带首尾空格", "  sha256:" + valid + "  ", true},
		{"缺少算法前缀", valid, false},
		{"算法不支持", "sha512:" + valid, false},
		{"长度不足", "sha256:013cbdab", false},
		{"含非十六进制字符", "sha256:" + strings.Repeat("z", 64), false},
		{"空字符串", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseDigest(tc.digest)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v，期望 %v", ok, tc.wantOK)
			}
			if ok && got != valid {
				t.Errorf("解析结果 %q，期望 %q", got, valid)
			}
		})
	}
}

func TestResolveChecksumKind(t *testing.T) {
	const valid = "013cbdab2509dda257830f9aee87f3a3883fa789fef23ddaf5e4759f7abdca21"

	t.Run("checksums.txt 优先", func(t *testing.T) {
		got := resolveChecksumKind(
			&Asset{Name: "pkg", Digest: "sha256:" + valid},
			&Asset{Name: "checksums.txt"},
		)
		if got != ChecksumFile {
			t.Errorf("期望 %q，实际 %q", ChecksumFile, got)
		}
	})

	t.Run("无 checksums.txt 时回退到附件摘要", func(t *testing.T) {
		got := resolveChecksumKind(&Asset{Name: "pkg", Digest: "sha256:" + valid}, nil)
		if got != ChecksumDigest {
			t.Errorf("期望 %q，实际 %q", ChecksumDigest, got)
		}
	})

	t.Run("两者都没有", func(t *testing.T) {
		got := resolveChecksumKind(&Asset{Name: "pkg"}, nil)
		if got != ChecksumNone {
			t.Errorf("期望 %q，实际 %q", ChecksumNone, got)
		}
	})

	t.Run("无安装包", func(t *testing.T) {
		got := resolveChecksumKind(nil, nil)
		if got != ChecksumNone {
			t.Errorf("期望 %q，实际 %q", ChecksumNone, got)
		}
	})
}

func TestExpectedHashFallsBackToDigest(t *testing.T) {
	const valid = "013cbdab2509dda257830f9aee87f3a3883fa789fef23ddaf5e4759f7abdca21"
	svc := testService()

	t.Run("使用附件摘要且不发起网络请求", func(t *testing.T) {
		st := &Status{Asset: &Asset{Name: "inventory-server-amd64", Digest: "sha256:" + valid}}
		got, source, err := svc.expectedHash(context.Background(), st)
		if err != nil {
			t.Fatalf("不应报错: %v", err)
		}
		if got != valid || source != ChecksumDigest {
			t.Errorf("got (%q, %q)", got, source)
		}
	})

	t.Run("既无 checksums.txt 也无摘要时中止", func(t *testing.T) {
		st := &Status{Asset: &Asset{Name: "inventory-server-amd64"}}
		_, _, err := svc.expectedHash(context.Background(), st)
		if err == nil {
			t.Fatal("应返回错误")
		}
		if !strings.Contains(err.Error(), "checksums.txt") {
			t.Errorf("错误信息应说明缺少校验来源，实际 %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// 裸可执行文件落盘与平台校验
// ---------------------------------------------------------------------------

// platformMagic 返回当前平台可执行文件的魔数，用于构造测试样本。
func platformMagic() []byte {
	switch runtime.GOOS {
	case "linux":
		return []byte{0x7f, 'E', 'L', 'F'}
	case "windows":
		return []byte{'M', 'Z'}
	default: // darwin
		return []byte{0xcf, 0xfa, 0xed, 0xfe}
	}
}

// foreignMagic 返回一个确定不属于当前平台的魔数。
func foreignMagic() []byte {
	if runtime.GOOS == "linux" {
		return []byte{'M', 'Z', 0x00, 0x00}
	}
	return []byte{0x7f, 'E', 'L', 'F'}
}

func writeRawBinary(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	return path
}

func TestExtractFromRawBinary(t *testing.T) {
	t.Run("匹配当前平台的裸二进制直接落盘", func(t *testing.T) {
		payload := append(platformMagic(), []byte("fake payload")...)
		path := writeRawBinary(t, "inventory-server-amd64", payload)
		dest := filepath.Join(t.TempDir(), "out")

		if err := extractBinary(path, dest); err != nil {
			t.Fatalf("落盘失败: %v", err)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("读取结果失败: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("内容 = %q，期望 %q", got, payload)
		}

		info, err := os.Stat(dest)
		if err != nil {
			t.Fatalf("stat 失败: %v", err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("应具备可执行权限，实际 %v", info.Mode().Perm())
		}
	})

	t.Run("平台不符的二进制被拒绝", func(t *testing.T) {
		payload := append(foreignMagic(), []byte("fake payload")...)
		path := writeRawBinary(t, "inventory-server-amd64", payload)

		err := extractBinary(path, filepath.Join(t.TempDir(), "out"))
		if err == nil {
			t.Fatal("应拒绝其它平台的可执行文件")
		}
		if !strings.Contains(err.Error(), runtime.GOOS) {
			t.Errorf("错误信息应指出当前平台，实际 %v", err)
		}
	})

	t.Run("空文件被拒绝", func(t *testing.T) {
		path := writeRawBinary(t, "inventory-server-amd64", nil)
		if err := extractBinary(path, filepath.Join(t.TempDir(), "out")); err == nil {
			t.Error("空文件应被拒绝")
		}
	})

	t.Run("纯文本文件被拒绝", func(t *testing.T) {
		path := writeRawBinary(t, "inventory-server-amd64", []byte("this is not an executable"))
		if err := extractBinary(path, filepath.Join(t.TempDir(), "out")); err == nil {
			t.Error("非可执行文件应被拒绝")
		}
	})
}

func TestMatchesExecutableHeader(t *testing.T) {
	tests := []struct {
		goos string
		head [4]byte
		want bool
	}{
		{"linux", [4]byte{0x7f, 'E', 'L', 'F'}, true},
		{"linux", [4]byte{'M', 'Z', 0, 0}, false},
		{"windows", [4]byte{'M', 'Z', 0, 0}, true},
		{"windows", [4]byte{0x7f, 'E', 'L', 'F'}, false},
		{"darwin", [4]byte{0xcf, 0xfa, 0xed, 0xfe}, true},
		{"darwin", [4]byte{0xca, 0xfe, 0xba, 0xbe}, true},
		{"darwin", [4]byte{0x7f, 'E', 'L', 'F'}, false},
	}

	for _, tc := range tests {
		if got := matchesExecutableHeader(tc.head, tc.goos); got != tc.want {
			t.Errorf("matchesExecutableHeader(%v, %q) = %v，期望 %v", tc.head, tc.goos, got, tc.want)
		}
	}

	t.Run("未覆盖的平台不限制", func(t *testing.T) {
		if !matchesExecutableHeader([4]byte{0, 0, 0, 0}, "plan9") {
			t.Error("未覆盖平台应放行")
		}
	})
}

// ---------------------------------------------------------------------------
// 端到端：Check() 对接真实发布结构
// ---------------------------------------------------------------------------

const releaseJSONV1001 = `{
  "tag_name": "v1.0.01",
  "name": "Release v1.0.01",
  "body": "",
  "html_url": "https://github.com/jinhuaitao/inventory/releases/tag/v1.0.01",
  "published_at": "2026-09-28T01:48:31Z",
  "draft": false,
  "prerelease": false,
  "assets": [
    {
      "name": "inventory-server-amd64",
      "size": 15052960,
      "browser_download_url": "https://github.com/jinhuaitao/inventory/releases/download/v1.0.01/inventory-server-amd64",
      "digest": "sha256:013cbdab2509dda257830f9aee87f3a3883fa789fef23ddaf5e4759f7abdca21"
    },
    {
      "name": "inventory-server-arm64",
      "size": 14287008,
      "browser_download_url": "https://github.com/jinhuaitao/inventory/releases/download/v1.0.01/inventory-server-arm64",
      "digest": "sha256:cca98fd32a09af9d3e5ff8144ddda0e3b12f904e32c5ea32010d874e5c772fb4"
    }
  ]
}`

// roundTripFunc 让 http.Client 把请求转发到本地测试服务器。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// newStubClient 返回一个把 GitHub API 请求重定向到本地桩服务的客户端。
func newStubClient(t *testing.T, body string) *http.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("解析测试服务器地址失败: %v", err)
	}

	return &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			return http.DefaultTransport.RoundTrip(r)
		}),
	}
}

// newReleaseStubService 构造一个对接桩服务的更新服务。
func newReleaseStubService(t *testing.T, body string) *Service {
	t.Helper()
	return New(Options{
		Enabled: true,
		Repo:    "jinhuaitao/inventory",
		Current: "1.0.0",
		Client:  newStubClient(t, body),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCheckAgainstRealReleaseShape(t *testing.T) {
	svc := newReleaseStubService(t, releaseJSONV1001)
	st := svc.Check(context.Background())

	t.Run("版本比较", func(t *testing.T) {
		if !st.Comparable {
			t.Errorf("1.0.0 应可参与比较，Err=%q", st.Err)
		}
		if !st.UpdateAvailable {
			t.Errorf("应判定存在可用更新，Err=%q", st.Err)
		}
		if st.Latest != "v1.0.01" {
			t.Errorf("Latest = %q", st.Latest)
		}
	})

	t.Run("发布时间解析", func(t *testing.T) {
		if st.PublishedAt.IsZero() {
			t.Error("发布时间不应为零值")
		}
	})

	if runtime.GOOS == "linux" {
		t.Run("Linux 上匹配到裸二进制", func(t *testing.T) {
			if st.Asset == nil {
				t.Fatalf("应匹配到安装包，Err=%q", st.Err)
			}
			want := "inventory-server-" + runtime.GOARCH
			if st.Asset.Name != want {
				t.Errorf("Asset = %q，期望 %q", st.Asset.Name, want)
			}
			if st.Asset.Kind() != KindBinary {
				t.Errorf("Kind = %v，期望 KindBinary", st.Asset.Kind())
			}
			if st.ChecksumKind != ChecksumDigest {
				t.Errorf("ChecksumKind = %q，期望 %q", st.ChecksumKind, ChecksumDigest)
			}
			if st.Err != "" {
				t.Errorf("不应有错误信息，实际 %q", st.Err)
			}
		})
	} else {
		t.Run("非 Linux 平台明确提示缺少安装包", func(t *testing.T) {
			if st.Asset != nil {
				t.Fatalf("该发布只有 Linux 产物，不应匹配到 %q", st.Asset.Name)
			}
			if !strings.Contains(st.Err, "安装包") {
				t.Errorf("应提示缺少安装包，实际 %q", st.Err)
			}
		})
	}
}

func TestCheckReportsDevBuild(t *testing.T) {
	svc := New(Options{
		Enabled: true,
		Repo:    "jinhuaitao/inventory",
		Current: "dev",
		Client:  newStubClient(t, releaseJSONV1001),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	st := svc.Check(context.Background())

	if st.Comparable {
		t.Error("dev 版本不应参与比较")
	}
	if !strings.Contains(st.Err, "开发构建") {
		t.Errorf("应提示开发构建，实际 %q", st.Err)
	}
	if st.UpdateAvailable {
		t.Error("开发构建不应判定有可用更新")
	}
}
