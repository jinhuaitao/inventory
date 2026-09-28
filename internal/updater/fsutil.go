package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Restart 在延迟一段时间后重启进程。
//
// 延迟是为了让调用方有机会把 HTTP 响应完整写回浏览器；
// 重启成功时本函数不会返回（进程映像被替换）。
func (s *Service) Restart(delay time.Duration) error {
	if delay > 0 {
		time.Sleep(delay)
	}
	s.logger.Info("正在重启服务以应用更新")
	return restartProcess()
}

// resolveExecutable 返回当前可执行文件的绝对路径（解析符号链接）。
func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法定位当前可执行文件: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", fmt.Errorf("无法解析可执行文件路径: %w", err)
	}
	return abs, nil
}

// copyFile 复制文件并设置权限。
// 用于把临时目录中的新版本搬到与当前可执行文件同一目录，
// 避免跨文件系统 rename 失败（EXDEV）。
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件失败: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("复制文件失败: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("刷新目标文件失败: %w", err)
	}
	return nil
}
