package updater

import (
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantOK  bool
		major   int
		minor   int
		patch   int
		preText string
	}{
		{name: "标准三段", in: "1.2.3", want: "1.2.3", wantOK: true, major: 1, minor: 2, patch: 3},
		{name: "带 v 前缀", in: "v1.2.3", want: "1.2.3", wantOK: true, major: 1, minor: 2, patch: 3},
		{name: "大写 V 前缀", in: "V2.0.1", want: "2.0.1", wantOK: true, major: 2, minor: 0, patch: 1},
		{name: "两段补零", in: "v1.5", want: "1.5.0", wantOK: true, major: 1, minor: 5, patch: 0},
		{name: "一段补零", in: "3", want: "3.0.0", wantOK: true, major: 3},
		{name: "预发布", in: "v1.2.3-beta.1", want: "1.2.3-beta.1", wantOK: true, major: 1, minor: 2, patch: 3, preText: "beta.1"},
		{name: "构建元数据被剥离", in: "v1.2.3+build.5", want: "1.2.3-build.5", wantOK: true, major: 1, minor: 2, patch: 3, preText: "build.5"},
		{name: "首尾空格", in: "  v1.2.3  ", want: "1.2.3", wantOK: true, major: 1, minor: 2, patch: 3},
		{name: "开发版本无法解析", in: "dev", wantOK: false},
		{name: "空字符串无法解析", in: "", wantOK: false},
		{name: "非数字无法解析", in: "v1.2.x", wantOK: false},
		{name: "段数过多无法解析", in: "1.2.3.4", wantOK: false},
		{name: "负数无法解析", in: "1.-2.3", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, ok := ParseVersion(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ParseVersion(%q) ok = %v, 期望 %v", tc.in, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if v.String() != tc.want {
				t.Errorf("ParseVersion(%q).String() = %q, 期望 %q", tc.in, v.String(), tc.want)
			}
			if v.Major != tc.major || v.Minor != tc.minor || v.Patch != tc.patch {
				t.Errorf("ParseVersion(%q) = %d.%d.%d, 期望 %d.%d.%d",
					tc.in, v.Major, v.Minor, v.Patch, tc.major, tc.minor, tc.patch)
			}
			if tc.preText != "" && v.Pre != tc.preText {
				t.Errorf("ParseVersion(%q).Pre = %q, 期望 %q", tc.in, v.Pre, tc.preText)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
		want int // 期望 Compare(a,b) 的符号：1 / 0 / -1
	}{
		{"主版本更高", "2.0.0", "1.9.9", 1},
		{"次版本更高", "1.2.0", "1.1.9", 1},
		{"修订号更高", "1.2.4", "1.2.3", 1},
		{"完全相同", "1.2.3", "1.2.3", 0},
		{"带 v 前缀视为相同", "v1.2.3", "1.2.3", 0},
		{"两段与三段等价", "1.2", "1.2.0", 0},
		{"更低", "1.2.2", "1.2.3", -1},
		{"正式版高于预发布", "1.0.0", "1.0.0-beta", 1},
		{"预发布低于正式版", "1.0.0-rc.1", "1.0.0", -1},
		{"预发布序号比较", "1.0.0-beta.2", "1.0.0-beta.1", 1},
		{"预发布段数少的更小", "1.0.0-beta", "1.0.0-beta.1", -1},
		{"数字段按数值比较", "1.0.0-rc.10", "1.0.0-rc.9", 1},
		{"大版本跨越", "10.0.0", "9.9.9", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, okA := ParseVersion(tc.a)
			b, okB := ParseVersion(tc.b)
			if !okA || !okB {
				t.Fatalf("解析失败: %q=%v, %q=%v", tc.a, okA, tc.b, okB)
			}

			got := Compare(a, b)
			switch {
			case tc.want > 0 && got <= 0:
				t.Errorf("Compare(%q, %q) = %d, 期望正数", tc.a, tc.b, got)
			case tc.want < 0 && got >= 0:
				t.Errorf("Compare(%q, %q) = %d, 期望负数", tc.a, tc.b, got)
			case tc.want == 0 && got != 0:
				t.Errorf("Compare(%q, %q) = %d, 期望 0", tc.a, tc.b, got)
			}

			// 反对称性
			if rev := Compare(b, a); rev != -got {
				t.Errorf("Compare(%q, %q) = %d，反向应为 %d", tc.b, tc.a, rev, -got)
			}
		})
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"有新版本", "v1.0.0", "v1.1.0", true},
		{"同版本", "v1.0.0", "v1.0.0", false},
		{"本地更新", "v2.0.0", "v1.9.0", false},
		{"当前为开发版不提示更新", "dev", "v1.0.0", false},
		{"当前无法解析", "unknown", "v1.0.0", false},
		{"远端无法解析", "v1.0.0", "nightly", false},
		{"补丁号升级", "1.0.0", "1.0.1", true},
		{"正式版替换预发布", "1.0.0-beta", "1.0.0", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNewer(tc.current, tc.latest); got != tc.want {
				t.Errorf("IsNewer(%q, %q) = %v, 期望 %v", tc.current, tc.latest, got, tc.want)
			}
		})
	}
}

func TestMatchAsset(t *testing.T) {
	assets := []Asset{
		{Name: "inventory-server-v1.2.0-linux-amd64.tar.gz"},
		{Name: "inventory-server-v1.2.0-linux-arm64.tar.gz"},
		{Name: "inventory-server-v1.2.0-darwin-amd64.tar.gz"},
		{Name: "inventory-server-v1.2.0-darwin-arm64.tar.gz"},
		{Name: "inventory-server-v1.2.0-windows-amd64.zip"},
		{Name: "checksums.txt"},
	}

	cases := []struct {
		goos   string
		goarch string
		want   string
	}{
		{"linux", "amd64", "inventory-server-v1.2.0-linux-amd64.tar.gz"},
		{"linux", "arm64", "inventory-server-v1.2.0-linux-arm64.tar.gz"},
		{"darwin", "arm64", "inventory-server-v1.2.0-darwin-arm64.tar.gz"},
		{"windows", "amd64", "inventory-server-v1.2.0-windows-amd64.zip"},
	}

	for _, tc := range cases {
		t.Run(tc.goos+"-"+tc.goarch, func(t *testing.T) {
			got := matchAsset(assets, tc.goos, tc.goarch)
			if got == nil {
				t.Fatalf("未匹配到 %s-%s 的安装包", tc.goos, tc.goarch)
			}
			if got.Name != tc.want {
				t.Errorf("匹配到 %q, 期望 %q", got.Name, tc.want)
			}
		})
	}

	t.Run("不支持的平台返回 nil", func(t *testing.T) {
		if got := matchAsset(assets, "freebsd", "riscv64"); got != nil {
			t.Errorf("不支持的平台应返回 nil，实际 %q", got.Name)
		}
	})

	t.Run("Windows 不会匹配到 tar.gz", func(t *testing.T) {
		got := matchAsset(assets, "windows", "arm64")
		if got != nil {
			t.Errorf("Windows/arm64 无对应包，应返回 nil，实际 %q", got.Name)
		}
	})
}

func TestMatchChecksumAsset(t *testing.T) {
	t.Run("优先匹配汇总文件", func(t *testing.T) {
		assets := []Asset{
			{Name: "checksums-linux-amd64.txt"},
			{Name: "checksums.txt"},
		}
		got := matchChecksumAsset(assets)
		if got == nil || got.Name != "checksums.txt" {
			t.Fatalf("应优先匹配 checksums.txt，实际 %v", got)
		}
	})

	t.Run("退回到平台校验和", func(t *testing.T) {
		assets := []Asset{{Name: "checksums-darwin-arm64.txt"}}
		got := matchChecksumAsset(assets)
		if got == nil || got.Name != "checksums-darwin-arm64.txt" {
			t.Fatalf("应匹配平台校验和文件，实际 %v", got)
		}
	})

	t.Run("没有校验和文件返回 nil", func(t *testing.T) {
		assets := []Asset{{Name: "inventory-server-v1.0.0-linux-amd64.tar.gz"}}
		if got := matchChecksumAsset(assets); got != nil {
			t.Errorf("应返回 nil，实际 %q", got.Name)
		}
	})
}

func TestTruncateNotes(t *testing.T) {
	t.Run("短文本原样返回", func(t *testing.T) {
		in := "  修复若干问题  "
		if got := truncateNotes(in); got != "修复若干问题" {
			t.Errorf("truncateNotes = %q", got)
		}
	})

	t.Run("超长文本按字符截断", func(t *testing.T) {
		long := make([]rune, maxReleaseBodyLen+500)
		for i := range long {
			long[i] = '好'
		}
		got := truncateNotes(string(long))
		runes := []rune(got)
		if len(runes) <= maxReleaseBodyLen {
			t.Errorf("截断后长度 %d，应包含截断提示", len(runes))
		}
		if !strings.Contains(got, "已截断") {
			t.Error("截断后应附带提示文字")
		}
	})
}
