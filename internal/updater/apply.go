package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 下载与解压的安全边界。
const (
	// maxArchiveSize 允许下载的安装包最大体积
	maxArchiveSize = 200 << 20 // 200 MiB
	// maxBinarySize 解压后单个可执行文件的最大体积
	maxBinarySize = 150 << 20 // 150 MiB
	// downloadTimeout 单次下载的总超时
	downloadTimeout = 5 * time.Minute
)

// ErrManualReplaceRequired 表示当前平台无法在运行时替换可执行文件。
var ErrManualReplaceRequired = errors.New("当前平台不支持运行时替换，请手动完成更新")

// ApplyResult 描述一次自更新的执行结果。
type ApplyResult struct {
	FromVersion string // 更新前版本
	ToVersion   string // 更新后版本
	BackupPath  string // 旧可执行文件的备份路径（可用于回滚）
	BinaryPath  string // 新的可执行文件路径
	Restarted   bool   // 是否已就地重启
	ManualHint  string // 需要手动操作时的提示
}

// Apply 执行一次完整的自更新。
//
// 流程：下载安装包 → 校验 SHA-256 → 解出可执行文件 → 原子替换当前文件 → 重启进程。
// 只有校验通过才会替换文件；替换失败会尝试回滚。
//
// 同一时刻只允许一次更新：两个管理员同时点「立即更新」（或双击提交）会
// 交叉执行替换 —— 后来者可能删掉前者刚写好的 .old 备份，或对同一个
// 暂存文件重复 rename，最坏情况把二进制弄丢。第二个请求直接被拒绝。
func (s *Service) Apply(ctx context.Context, st *Status) (*ApplyResult, error) {
	if !s.applyMu.TryLock() {
		return nil, errors.New("已有更新任务正在进行，请等待其完成后重试")
	}
	defer s.applyMu.Unlock()

	if st == nil {
		st = s.Cached()
	}
	if !st.UpdateAvailable {
		return nil, errors.New("当前没有可用的更新")
	}
	if st.Asset == nil {
		return nil, errors.New("未找到适用于 " + Platform() + " 的安装包")
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	// 1) 解析期望的 SHA-256：checksums.txt 优先，缺失时用 GitHub 附件摘要兜底
	expected, source, err := s.expectedHash(ctx, st)
	if err != nil {
		return nil, err
	}
	s.logger.Info("开始下载更新包",
		"版本", st.Latest,
		"文件", st.Asset.Name,
		"大小", st.Asset.Size,
		"形式", st.Asset.Kind().String(),
		"校验来源", source,
	)

	// 2) 下载安装包到临时目录
	tmpDir, err := os.MkdirTemp("", "inventory-update-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, st.Asset.Name)
	actual, err := s.download(ctx, st.Asset.DownloadURL, archivePath)
	if err != nil {
		return nil, err
	}

	// 3) 校验完整性
	if !strings.EqualFold(actual, expected) {
		return nil, fmt.Errorf("安装包校验失败：期望 %s，实际 %s", shortHash(expected), shortHash(actual))
	}
	s.logger.Info("安装包校验通过", "sha256", shortHash(actual), "来源", source)

	// 4) 解出可执行文件（归档则解压，裸二进制则直接落盘并校验文件头）
	newBinary := filepath.Join(tmpDir, "inventory-server.new")
	if err := extractBinary(archivePath, newBinary); err != nil {
		return nil, err
	}

	// 5) 原子替换当前可执行文件
	result, err := replaceExecutable(newBinary, st)
	if err != nil {
		return nil, err
	}
	result.FromVersion = st.Current
	result.ToVersion = st.Latest

	s.logger.Info("可执行文件已替换",
		"从", st.Current, "到", st.Latest,
		"路径", result.BinaryPath,
		"备份", result.BackupPath,
	)
	return result, nil
}

// expectedHash 解析目标安装包应当具有的 SHA-256，并返回其来源。
//
// 优先使用发布中的 checksums.txt；该文件缺失、下载失败或没有目标记录时，
// 回退到 GitHub API 为附件提供的 digest 字段（由 GitHub 在上传时计算，
// 与本仓库的 checksums.txt 具有同等可信度）。两者都不可用时中止更新。
func (s *Service) expectedHash(ctx context.Context, st *Status) (hash, source string, err error) {
	if st.ChecksumAsset != nil {
		h, fetchErr := s.fetchChecksum(ctx, st.ChecksumAsset.DownloadURL, st.Asset.Name)
		if fetchErr == nil {
			return h, ChecksumFile, nil
		}
		s.logger.Warn("读取 checksums.txt 失败，改用附件摘要校验", "错误", fetchErr)
	}

	if h, ok := parseDigest(st.Asset.Digest); ok {
		return h, ChecksumDigest, nil
	}

	return "", ChecksumNone, errors.New(
		"该发布既没有 checksums.txt，也没有可用的附件摘要，出于安全考虑已中止更新")
}

// fetchChecksum 下载校验和文件并取出指定文件的哈希。
func (s *Service) fetchChecksum(ctx context.Context, rawURL, target string) (string, error) {
	body, err := s.fetchBytes(ctx, rawURL, 1<<20)
	if err != nil {
		return "", fmt.Errorf("下载校验和文件失败: %w", err)
	}

	hash, ok := parseChecksum(string(body), target)
	if !ok {
		return "", fmt.Errorf("校验和文件中没有 %s 的记录", target)
	}
	return hash, nil
}

// parseChecksum 从 shasum 风格的文本中取出指定文件的 SHA-256。
//
// 每行形如 `<hash>  <filename>`，也兼容 `*` 前缀（二进制模式）与多余空白。
func parseChecksum(content, target string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := fields[0]
		name := strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, hash)), "*")
		if filepath.Base(name) == target {
			return hash, true
		}
	}
	return "", false
}

// download 下载文件到磁盘，返回其 SHA-256。
func (s *Service) download(ctx context.Context, rawURL, dest string) (string, error) {
	if err := s.checkHost(rawURL); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("构造下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "inventory-server/"+s.opts.Current)
	req.Header.Set("Accept", "application/octet-stream")
	if s.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	}

	client := s.downloadClient()
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败，服务器返回: %s", resp.Status)
	}
	if resp.ContentLength > maxArchiveSize {
		return "", fmt.Errorf("安装包体积异常（%d 字节），已中止", resp.ContentLength)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}
	defer f.Close()

	hasher := sha256.New()
	// 双重限制：既限制 Content-Length，也限制实际写入量，防止流式攻击
	limited := io.LimitReader(resp.Body, maxArchiveSize+1)
	written, err := io.Copy(io.MultiWriter(f, hasher), limited)
	if err != nil {
		return "", fmt.Errorf("写入安装包失败: %w", err)
	}
	if written > maxArchiveSize {
		return "", errors.New("安装包超出体积上限，已中止")
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// fetchBytes 下载小文件到内存（用于校验和文件）。
func (s *Service) fetchBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if err := s.checkHost(rawURL); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "inventory-server/"+s.opts.Current)
	if s.opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.opts.Token)
	}

	resp, err := s.downloadClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("服务器返回: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// downloadClient 返回用于下载的 HTTP 客户端，并对重定向目标做主机白名单校验。
func (s *Service) downloadClient() *http.Client {
	base := s.client
	clone := *base
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("重定向次数过多")
		}
		return s.checkHost(req.URL.String())
	}
	return &clone
}

// checkHost 校验下载地址的主机名，防止被诱导去请求内网地址（SSRF）。
func (s *Service) checkHost(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("下载地址不合法: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("下载地址必须使用 HTTPS，实际为 %q", u.Scheme)
	}

	host := strings.ToLower(u.Hostname())
	allowed := host == "github.com" ||
		host == "api.github.com" ||
		host == "codeload.github.com" ||
		strings.HasSuffix(host, ".githubusercontent.com")

	if !allowed {
		return fmt.Errorf("拒绝从非 GitHub 域名下载：%s", host)
	}
	return nil
}

// extractBinary 把下载到的安装包还原成可执行文件。
//
// 依据文件名判断封装形式：tar.gz / zip 走解压，其余按裸可执行文件直接落盘。
func extractBinary(archivePath, dest string) error {
	switch kind := (&Asset{Name: filepath.Base(archivePath)}).Kind(); kind {
	case KindZip:
		return extractFromZip(archivePath, dest)
	case KindTarGz:
		return extractFromTarGz(archivePath, dest)
	case KindBinary:
		return extractFromRawBinary(archivePath, dest)
	default:
		return fmt.Errorf("无法识别的安装包格式: %s", filepath.Base(archivePath))
	}
}

// extractFromRawBinary 把裸可执行文件直接落盘，并校验其文件头。
//
// 这一步是必要的安全边界：inventory-server-<goarch> 这类命名不含操作系统
// 信息，若不校验，macOS / Windows 上可能把 Linux 二进制装成自己的可执行文件。
func extractFromRawBinary(path, dest string) error {
	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开安装包失败: %w", err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("读取安装包信息失败: %w", err)
	}
	switch {
	case info.Size() == 0:
		return errors.New("安装包内容为空")
	case info.Size() > maxBinarySize:
		return errors.New("安装包体积异常，已中止")
	}

	if err := verifyExecutableHeader(src); err != nil {
		return err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("读取安装包失败: %w", err)
	}

	return writeExecutable(dest, io.LimitReader(src, maxBinarySize))
}

// executableHeaderLen 是足以覆盖 ELF / PE / Mach-O 魔数的文件头长度。
const executableHeaderLen = 4

// verifyExecutableHeader 校验文件头是否为当前平台的可执行格式。
func verifyExecutableHeader(r io.Reader) error {
	var header [executableHeaderLen]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return errors.New("安装包内容为空或过短，已中止")
	}
	if !matchesExecutableHeader(header, runtime.GOOS) {
		return fmt.Errorf("安装包不是 %s 平台的可执行文件，已中止更新", runtime.GOOS)
	}
	return nil
}

// matchesExecutableHeader 判断文件头是否符合指定操作系统的可执行格式。
func matchesExecutableHeader(h [executableHeaderLen]byte, goos string) bool {
	switch goos {
	case "linux":
		// ELF：0x7F 'E' 'L' 'F'
		return h[0] == 0x7f && h[1] == 'E' && h[2] == 'L' && h[3] == 'F'
	case "windows":
		// PE / DOS MZ
		return h[0] == 'M' && h[1] == 'Z'
	case "darwin":
		// Mach-O 32/64 位（大端与小端）以及通用二进制
		return (h[0] == 0xfe && h[1] == 0xed && h[2] == 0xfa) ||
			(h[0] == 0xce && h[1] == 0xfa && h[2] == 0xed && h[3] == 0xfe) ||
			(h[0] == 0xcf && h[1] == 0xfa && h[2] == 0xed && h[3] == 0xfe) ||
			(h[0] == 0xca && h[1] == 0xfe && h[2] == 0xba && h[3] == 0xbe)
	default:
		// 未覆盖的平台不做限制，避免误伤小众目标
		return true
	}
}

// extractFromTarGz 从 tar.gz 中取出第一个常规文件。
func extractFromTarGz(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("打开安装包失败: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("安装包不是有效的 gzip 文件: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取安装包失败: %w", err)
		}
		if !isRegularFile(hdr.Typeflag) || hdr.Size <= 0 {
			continue
		}
		if hdr.Size > maxBinarySize {
			return errors.New("安装包中的可执行文件体积异常，已中止")
		}
		return writeExecutable(dest, io.LimitReader(tr, maxBinarySize))
	}
	return errors.New("安装包中未找到可执行文件")
}

// extractFromZip 从 zip 中取出第一个常规文件。
func extractFromZip(archivePath, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开安装包失败: %w", err)
	}
	defer zr.Close()

	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		if int64(zf.UncompressedSize64) > maxBinarySize {
			return errors.New("安装包中的可执行文件体积异常，已中止")
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("读取安装包失败: %w", err)
		}
		err = writeExecutable(dest, io.LimitReader(rc, maxBinarySize))
		rc.Close()
		return err
	}
	return errors.New("安装包中未找到可执行文件")
}

// isRegularFile 判断 tar 条目是否为常规文件（含旧版 TypeRegA）。
func isRegularFile(flag byte) bool {
	return flag == tar.TypeReg || flag == tar.TypeRegA
}

// writeExecutable 把内容写入目标路径并设置可执行权限。
func writeExecutable(dest string, src io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("创建可执行文件失败: %w", err)
	}
	defer out.Close()

	// 直接用 io.Copy 的写入字节数判空。早先把全部内容再抄一份进
	// bytes.Buffer「只为知道是不是空的」，150 MiB 的上限意味着更新
	// 瞬间进程内存翻倍，小内存机器上会直接 OOM。
	written, err := io.Copy(out, src)
	if err != nil {
		return fmt.Errorf("写入可执行文件失败: %w", err)
	}
	if written == 0 {
		return errors.New("解压出的可执行文件为空")
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("刷新可执行文件失败: %w", err)
	}
	return nil
}

// shortHash 截断哈希用于日志与提示。
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12] + "…"
}
