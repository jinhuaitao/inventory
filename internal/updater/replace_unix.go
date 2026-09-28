//go:build !windows

package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// replaceExecutable 在类 Unix 系统上原子替换正在运行的可执行文件。
//
// 原理：正在运行的进程持有的是旧文件的 inode，因此可以先把旧文件改名，
// 再把新文件放到原路径。替换失败时会尝试回滚，保证服务仍可启动。
func replaceExecutable(newBinary string, st *Status) (*ApplyResult, error) {
	exe, err := resolveExecutable()
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(exe)
	staged := filepath.Join(dir, ".inventory-server.new")
	backup := exe + ".old"

	// 1) 先把新版本复制到同目录，避免跨文件系统 rename 失败
	if err := copyFile(newBinary, staged, 0o755); err != nil {
		return nil, fmt.Errorf("准备新版本失败（请确认对 %s 有写权限）: %w", dir, err)
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return nil, fmt.Errorf("设置可执行权限失败: %w", err)
	}

	// 2) 清理上一次更新留下的备份
	if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("清理旧备份失败: %w", err)
	}

	// 3) 旧文件改名为备份
	if err := os.Rename(exe, backup); err != nil {
		_ = os.Remove(staged)
		return nil, fmt.Errorf("备份当前版本失败（请确认对 %s 有写权限）: %w", dir, err)
	}

	// 4) 新文件就位；失败则回滚
	if err := os.Rename(staged, exe); err != nil {
		if rbErr := os.Rename(backup, exe); rbErr != nil {
			return nil, fmt.Errorf("替换失败且回滚失败，请手动将 %s 改回 %s：%w", backup, exe, rbErr)
		}
		_ = os.Remove(staged)
		return nil, fmt.Errorf("替换可执行文件失败，已回滚到原版本: %w", err)
	}

	return &ApplyResult{
		BinaryPath: exe,
		BackupPath: backup,
		Restarted:  false,
	}, nil
}

// restartProcess 用新版本替换当前进程映像。
//
// 使用 syscall.Exec：进程号不变、监听端口由内核在 exec 时释放并重新绑定，
// 因此不需要外部进程管理器配合。成功时本函数不会返回。
func restartProcess() error {
	exe, err := resolveExecutable()
	if err != nil {
		return err
	}
	//nolint:gosec // 用自身路径与原始参数重启，参数来自 os.Args，非外部输入
	return syscall.Exec(exe, os.Args, os.Environ())
}
