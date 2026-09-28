// Package updater 实现在线版本检查与自更新。
//
// 版本信息来自 GitHub Releases API，自更新流程为：
// 检查 → 下载当前平台对应的安装包 → 校验 SHA-256 → 解出可执行文件 →
// 原子替换当前文件 → 重启进程。
//
// 安装包同时兼容两种发布形态：tar.gz / zip 归档，以及未经压缩的裸可执行文件。
package updater

import (
	"strconv"
	"strings"
)

// Version 表示一个语义化版本号。
type Version struct {
	Major int
	Minor int
	Patch int
	Pre   string // 预发布标识，例如 beta.1
	Raw   string // 原始字符串
}

// String 返回规范化的版本文本。
func (v Version) String() string {
	out := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Pre != "" {
		out += "-" + v.Pre
	}
	return out
}

// ParseVersion 解析版本号，支持 v1.2.3、1.2.3、v1.2、v1.2.3-beta.1、v1.2.3+build 等形式。
// 解析失败返回 false，调用方应据此放弃版本比较。
func ParseVersion(s string) (Version, bool) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Version{}, false
	}

	t := raw
	if len(t) > 0 && (t[0] == 'v' || t[0] == 'V') {
		t = t[1:]
	}

	// 剥离预发布标识与构建元数据
	pre := ""
	if i := strings.IndexAny(t, "-+"); i >= 0 {
		pre = t[i+1:]
		t = t[:i]
	}

	parts := strings.Split(t, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return Version{}, false
	}

	nums := [3]int{}
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return Version{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, false
		}
		nums[i] = n
	}

	return Version{
		Major: nums[0],
		Minor: nums[1],
		Patch: nums[2],
		Pre:   pre,
		Raw:   raw,
	}, true
}

// Compare 比较两个版本：a > b 返回正数，a < b 返回负数，相等返回 0。
//
// 遵循语义化版本规则：主次修订号逐位比较；数字相同时，
// 无预发布标识的版本视为更新（1.0.0 > 1.0.0-beta）。
func Compare(a, b Version) int {
	if c := compareInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := compareInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := compareInt(a.Patch, b.Patch); c != 0 {
		return c
	}

	switch {
	case a.Pre == "" && b.Pre == "":
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	default:
		return comparePreRelease(a.Pre, b.Pre)
	}
}

// IsNewer 判断 latest 是否比 current 更新。
// 任一版本无法解析时返回 false，避免把开发版本误判为可更新。
func IsNewer(current, latest string) bool {
	cur, ok1 := ParseVersion(current)
	lat, ok2 := ParseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	return Compare(lat, cur) > 0
}

// compareInt 比较两个整数。
func compareInt(a, b int) int {
	switch {
	case a > b:
		return 1
	case a < b:
		return -1
	default:
		return 0
	}
}

// comparePreRelease 比较预发布标识，按 . 分段后逐段比较。
// 纯数字段按数值比较，否则按字典序比较；段数少的视为更小。
func comparePreRelease(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")

	n := len(as)
	if len(bs) < n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		x, y := as[i], bs[i]
		xn, xErr := strconv.Atoi(x)
		yn, yErr := strconv.Atoi(y)

		switch {
		case xErr == nil && yErr == nil:
			if c := compareInt(xn, yn); c != 0 {
				return c
			}
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}

	return compareInt(len(as), len(bs))
}
