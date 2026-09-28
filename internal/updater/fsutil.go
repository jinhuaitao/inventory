package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// backupSuffix 是自更新时旧版本被改名后使用的后缀。
const backupSuffix = ".old"

// executablePath 是 os.Executable 的可替换实现。
// 抽成变量是为了让单元测试能模拟「进程正从 .old 备份启动」这种自更新后的状态。
var executablePath = os.Executable

// Restart 在延迟一段时间后重启进程。
//
// 延迟是为了让调用方有机会把 HTTP 响应完整写回浏览器；
// 重启成功时本函数不会返回（进程映像被替换）。
//
// binaryPath 必须是**替换之后**的可执行文件路径，通常直接传
// ApplyResult.BinaryPath。传空字符串时会退化为重新解析当前进程路径，
// 但那种做法在自更新场景下会指向备份文件，详见 resolveExecutable 的说明。
func (s *Service) Restart(delay time.Duration, binaryPath string) error {
	if delay > 0 {
		time.Sleep(delay)
	}

	exe := binaryPath
	if exe == "" {
		resolved, err := resolveExecutable()
		if err != nil {
			return err
		}
		exe = resolved
	}

	s.logger.Info("正在重启服务以应用更新", "可执行文件", exe)
	return restartProcess(exe)
}

// resolveExecutable 返回当前可执行文件的绝对路径（解析符号链接）。
//
// ⚠️ 自更新会先把正在运行的二进制改名为 <路径>.old，再把新版本放到原路径。
// 此后 /proc/self/exe 指向的已经是 .old，因此**替换完成之后**再调用本函数
// 会拿到备份路径，用它去 exec 等于又启动了旧版本。
// 更新流程请一律使用 Apply 返回的 ApplyResult.BinaryPath。
func resolveExecutable() (string, error) {
	exe, err := executablePath()
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

	// 自愈：早先的实现会在替换后重新解析路径，可能让进程从 .old 备份启动。
	// 若正式路径存在就回到正式路径，避免一直在备份文件上做更新。
	if strings.HasSuffix(abs, backupSuffix) {
		official := strings.TrimSuffix(abs, backupSuffix)
		if _, statErr := os.Stat(official); statErr == nil {
			return official, nil
		}
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
