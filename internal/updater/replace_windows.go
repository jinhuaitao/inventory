//go:build windows

package updater

import (
	"fmt"
)

// replaceExecutable 在 Windows 上的实现。
//
// Windows 不允许替换正在运行的可执行文件，因此这里把新版本放到
// `<当前程序>.new`，并返回手动操作提示，由用户停止服务后完成替换。
func replaceExecutable(newBinary string, st *Status) (*ApplyResult, error) {
	exe, err := resolveExecutable()
	if err != nil {
		return nil, err
	}

	dest := exe + ".new"
	if err := copyFile(newBinary, dest, 0o755); err != nil {
		return nil, fmt.Errorf("写入新版本失败: %w", err)
	}

	return &ApplyResult{
		BinaryPath: exe,
		BackupPath: dest,
		Restarted:  false,
		ManualHint: fmt.Sprintf(
			"新版本已下载到 %s。Windows 无法替换正在运行的程序，请先停止本服务，"+
				"再用该文件覆盖 %s，然后重新启动。覆盖前建议先备份原文件。", dest, exe),
	}, nil
}

// restartProcess 在 Windows 上无法就地重启，返回提示性错误。
func restartProcess() error {
	return ErrManualReplaceRequired
}
