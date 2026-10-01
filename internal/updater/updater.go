package updater

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 与 GitHub API 交互时的常量。
const (
	apiBase = "https://api.github.com"

	// maxReleaseBodyLen 限制 Release 说明的长度，避免超长文本拖垮页面
	maxReleaseBodyLen = 8000

	// checkTimeout 单次版本检查的超时时间
	checkTimeout = 20 * time.Second

	// sha256HexLen 是 SHA-256 十六进制摘要的字符长度
	sha256HexLen = 64
)

// Asset 是 Release 中的一个附件。
type Asset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`

	// Digest 是 GitHub 在附件上传时计算的摘要，形如 "sha256:<64 位十六进制>"。
	// 老版本 API 或自建服务可能不返回该字段，此时为空字符串。
	Digest string `json:"digest"`
}

// Kind 描述安装包的封装形式。
//
// 本项目同时兼容两种发布形态：
//   - 归档：inventory-server-<tag>-<goos>-<goarch>.tar.gz（Windows 为 .zip）
//   - 裸可执行文件：inventory-server-amd64 / inventory-server-linux-amd64
type Kind int

const (
	// KindUnknown 表示无法从文件名判断封装形式。
	KindUnknown Kind = iota
	// KindTarGz 表示 tar.gz / tgz 归档。
	KindTarGz
	// KindZip 表示 zip 归档。
	KindZip
	// KindBinary 表示未经压缩的裸可执行文件。
	KindBinary
)

// String 返回封装形式的中文描述，用于界面展示。
func (k Kind) String() string {
	switch k {
	case KindTarGz:
		return "tar.gz 归档"
	case KindZip:
		return "zip 归档"
	case KindBinary:
		return "裸可执行文件"
	default:
		return "未知"
	}
}

// Kind 依据文件名推断安装包的封装形式。
func (a *Asset) Kind() Kind {
	if a == nil {
		return KindUnknown
	}
	name := strings.ToLower(a.Name)
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return KindTarGz
	case strings.HasSuffix(name, ".zip"):
		return KindZip
	case name == "":
		return KindUnknown
	default:
		return KindBinary
	}
}

// 校验和来源，用于界面提示与日志。
const (
	// ChecksumNone 表示该发布既没有 checksums.txt，也没有附件摘要。
	ChecksumNone = ""
	// ChecksumFile 表示校验和取自发布中的 checksums.txt。
	ChecksumFile = "file"
	// ChecksumDigest 表示校验和取自 GitHub API 的附件摘要字段。
	ChecksumDigest = "digest"
)

// Release 是 GitHub Releases API 返回的发布信息（只保留用得到的字段）。
type Release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Assets      []Asset `json:"assets"`
}

// Status 是一次版本检查的结果快照。
type Status struct {
	Current         string    // 当前运行的版本
	Latest          string    // 远端最新版本
	UpdateAvailable bool      // 是否存在可用更新
	Comparable      bool      // 当前版本是否可参与比较（开发构建为 false）
	CheckedAt       time.Time // 本次检查时间
	PublishedAt     time.Time // 最新版本发布时间
	ReleaseURL      string    // Release 页面地址
	Notes           string    // Release 说明（已截断）
	Asset           *Asset    // 当前平台对应的下载包
	ChecksumAsset   *Asset    // 校验和文件（可能为 nil）
	ChecksumKind    string    // 校验和来源：ChecksumFile / ChecksumDigest / ChecksumNone
	Platform        string    // 当前平台，例如 linux-amd64
	Err             string    // 检查过程中的错误（面向用户展示）
}

// Options 是创建 Service 所需的配置。
type Options struct {
	Enabled  bool
	Repo     string // owner/repo
	Token    string // 可选，访问私有仓库或提升 API 配额
	Current  string // 当前版本
	Interval time.Duration
	Client   *http.Client
}

// Service 负责查询与缓存版本信息。
type Service struct {
	opts   Options
	logger *slog.Logger
	client *http.Client

	mu     sync.RWMutex
	status *Status

	// applyMu 保证同一时刻只有一次更新在执行（TryLock 拒绝并发请求）。
	applyMu sync.Mutex
}

// New 创建更新服务。logger 可以为 nil。
func New(opts Options, logger *slog.Logger) *Service {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: checkTimeout}
	}
	if opts.Interval <= 0 {
		opts.Interval = 6 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}

	s := &Service{opts: opts, logger: logger, client: client}
	s.status = &Status{
		Current:  opts.Current,
		Platform: Platform(),
		Err:      "尚未检查更新",
	}
	return s
}

// Enabled 返回在线更新功能是否可用。
func (s *Service) Enabled() bool {
	return s.opts.Enabled && strings.Contains(s.opts.Repo, "/")
}

// Repo 返回配置的仓库地址。
func (s *Service) Repo() string { return s.opts.Repo }

// Current 返回当前版本。
func (s *Service) Current() string { return s.opts.Current }

// Platform 返回当前运行平台，例如 darwin-arm64。
func Platform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// Cached 返回最近一次检查结果（可能是零值状态）。
func (s *Service) Cached() *Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := *s.status
	return &cp
}

// setStatus 覆盖缓存状态。
func (s *Service) setStatus(st *Status) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
}

// Check 向 GitHub 查询最新 Release 并更新缓存。
//
// 无论成功与否都会返回一个 Status（失败时 Err 字段带说明），
// 调用方无需担心 nil。
func (s *Service) Check(ctx context.Context) *Status {
	now := time.Now().In(displayZone())

	base := &Status{
		Current:   s.opts.Current,
		Platform:  Platform(),
		CheckedAt: now,
	}

	if !s.Enabled() {
		base.Err = "在线更新未启用或未配置更新仓库"
		s.setStatus(base)
		return base
	}

	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	release, err := s.fetchLatest(ctx)
	if err != nil {
		base.Err = err.Error()
		s.logger.Warn("检查更新失败", "仓库", s.opts.Repo, "错误", err)
		s.setStatus(base)
		return base
	}

	base.Latest = release.TagName
	base.ReleaseURL = release.HTMLURL
	base.Notes = truncateNotes(release.Body)
	base.Asset = matchAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	base.ChecksumAsset = matchChecksumAsset(release.Assets)
	base.ChecksumKind = resolveChecksumKind(base.Asset, base.ChecksumAsset)
	if ts, err := time.Parse(time.RFC3339, release.PublishedAt); err == nil {
		base.PublishedAt = ts.In(displayZone())
	}

	var problems []string

	if _, ok := ParseVersion(s.opts.Current); ok {
		base.Comparable = true
		base.UpdateAvailable = IsNewer(s.opts.Current, release.TagName)
	} else {
		base.Comparable = false
		problems = append(problems, "当前为开发构建，无法比较版本号")
	}

	if base.Asset == nil {
		problems = append(problems, "该版本未提供适用于 "+Platform()+" 的安装包")
	}

	base.Err = strings.Join(problems, "；")

	s.setStatus(base)
	return base
}

// resolveChecksumKind 判断本次更新可用的校验和来源。
//
// 优先使用发布中的 checksums.txt；缺失时回退到 GitHub API 为附件提供的
// digest 字段。两者都没有则返回 ChecksumNone，此时不允许自动更新。
func resolveChecksumKind(asset, checksum *Asset) string {
	if checksum != nil {
		return ChecksumFile
	}
	if asset != nil {
		if _, ok := parseDigest(asset.Digest); ok {
			return ChecksumDigest
		}
	}
	return ChecksumNone
}

// parseDigest 解析 GitHub 附件摘要，形如 "sha256:<64 位十六进制>"。
//
// 只接受 SHA-256；算法缺失、长度不符或含非十六进制字符时返回 false。
func parseDigest(digest string) (string, bool) {
	algo, value, found := strings.Cut(strings.TrimSpace(digest), ":")
	if !found || !strings.EqualFold(strings.TrimSpace(algo), "sha256") {
		return "", false
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != sha256HexLen {
		return "", false
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", false
	}
	return value, true
}

// Start 在后台按固定间隔检查更新，直到 ctx 被取消。
// 启动时会立即检查一次。
func (s *Service) Start(ctx context.Context) {
	if !s.Enabled() {
		s.logger.Info("在线更新未启用")
		return
	}

	check := func() {
		st := s.Check(ctx)
		if st.UpdateAvailable {
			s.logger.Info("发现新版本",
				"当前版本", st.Current,
				"最新版本", st.Latest,
				"发布地址", st.ReleaseURL,
			)
		}
	}

	go func() {
		check()

		ticker := time.NewTicker(s.opts.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check()
			}
		}
	}()
}

// fetchLatest 调用 GitHub Releases API 获取最新正式版本。
func (s *Service) fetchLatest(ctx context.Context) (*Release, error) {
	url := apiBase + "/repos/" + strings.Trim(s.opts.Repo, "/") + "/releases/latest"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "inventory-server/"+s.opts.Current)
	if s.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接更新服务器: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// 继续解析
	case http.StatusNotFound:
		return nil, errors.New("仓库不存在或尚未发布任何正式版本")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("GitHub API 访问频率超限，请稍后重试或配置 INVENTORY_UPDATE_TOKEN")
	default:
		return nil, fmt.Errorf("更新服务器返回异常状态: %s", resp.Status)
	}

	var release Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析版本信息失败: %w", err)
	}
	if release.Draft || release.Prerelease {
		return nil, errors.New("最新发布为草稿或预发布版本，已跳过")
	}
	if strings.TrimSpace(release.TagName) == "" {
		return nil, errors.New("版本信息缺少标签名")
	}
	return &release, nil
}

// matchAsset 在当前平台的附件中找出安装包。
//
// 兼容两种发布形态，按优先级依次尝试：
//
//  1. 归档（含系统名）：inventory-server-<tag>-<goos>-<goarch>.tar.gz
//     Windows 为 .zip
//  2. 裸可执行文件（含系统名）：inventory-server-<goos>-<goarch>
//  3. 裸可执行文件（仅架构名）：inventory-server-<goarch>
//
// 第 3 种命名没有携带操作系统信息，而本项目的发布流水线只产出 Linux
// 产物，因此仅在 GOOS=linux 时启用该兜底，避免 macOS / Windows 误装
// Linux 二进制。真正落盘前还会再做一次可执行文件头校验（见 apply.go）。
func matchAsset(assets []Asset, goos, goarch string) *Asset {
	archiveSuffix := "-" + goos + "-" + goarch + ".tar.gz"
	if goos == "windows" {
		archiveSuffix = "-" + goos + "-" + goarch + ".zip"
	}
	plainSuffix := "-" + goos + "-" + goarch
	archSuffix := "-" + goarch

	// 1) 归档优先：既能校验完整性，解压后也一定是可执行文件
	for i := range assets {
		if strings.HasSuffix(assets[i].Name, archiveSuffix) {
			return &assets[i]
		}
	}

	// 2) 带系统名的裸可执行文件
	for i := range assets {
		if strings.HasSuffix(assets[i].Name, plainSuffix) {
			return &assets[i]
		}
	}

	// 3) 仅带架构名的裸可执行文件（仅 Linux 兜底）
	if goos == "linux" {
		for i := range assets {
			if strings.HasSuffix(assets[i].Name, archSuffix) {
				return &assets[i]
			}
		}
	}

	return nil
}

// matchChecksumAsset 找出发布中的校验和文件（约定名为 checksums.txt）。
//
// 该文件是可选的：缺失时会回退到 GitHub API 为附件提供的 digest 字段。
func matchChecksumAsset(assets []Asset) *Asset {
	for i := range assets {
		if assets[i].Name == "checksums.txt" {
			return &assets[i]
		}
	}
	// 退而求其次：匹配带平台后缀的校验和文件
	for i := range assets {
		if strings.HasPrefix(assets[i].Name, "checksums-") && strings.HasSuffix(assets[i].Name, ".txt") {
			return &assets[i]
		}
	}
	return nil
}

// truncateNotes 按字符截断 Release 说明。
func truncateNotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxReleaseBodyLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxReleaseBodyLen {
		return s
	}
	return string(runes[:maxReleaseBodyLen]) + "\n\n…（说明过长已截断，完整内容请查看 Release 页面）"
}

// displayZone 返回界面展示时区（与全局保持一致：UTC+8）。
func displayZone() *time.Location {
	return time.FixedZone("UTC+8", 8*3600)
}
