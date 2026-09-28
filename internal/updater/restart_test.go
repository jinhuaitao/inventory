//go:build !windows

package updater

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 重启路径解析
//
// 回归背景：自更新会先把正在运行的二进制改名为 <路径>.old，再把新版本放到原路径。
// 此后 /proc/self/exe（以及 os.Executable）指向的已经是 .old。如果重启时重新解析
// 路径，就会 exec 回旧版本 —— 表现为「更新成功但版本没变」。
// ---------------------------------------------------------------------------

// resolvedTempDir 返回解析过符号链接的临时目录。
// macOS 上 TMPDIR 位于 /var/folders，而 /var 是指向 /private/var 的符号链接，
// 统一解析后断言才不会因路径差异而误报。
func resolvedTempDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("解析临时目录失败: %v", err)
	}
	return resolved
}

// stubExecutable 临时替换可执行文件路径来源，返回恢复函数。
func stubExecutable(t *testing.T, path string) {
	t.Helper()

	prev := executablePath
	executablePath = func() (string, error) { return path, nil }
	t.Cleanup(func() { executablePath = prev })
}

// captureExec 临时替换进程替换实现，返回捕获参数与恢复函数。
func captureExec(t *testing.T) (*string, *[]string) {
	t.Helper()

	var gotExe string
	var gotArgv []string

	prev := execProcess
	execProcess = func(exe string, argv, env []string) error {
		gotExe = exe
		gotArgv = argv
		return nil
	}
	t.Cleanup(func() { execProcess = prev })

	return &gotExe, &gotArgv
}

func TestRestartUsesExplicitBinaryPath(t *testing.T) {
	svc := testService()
	gotExe, gotArgv := captureExec(t)

	target := "/opt/inventory/inventory-server"
	if err := svc.Restart(0, target); err != nil {
		t.Fatalf("Restart 返回错误: %v", err)
	}

	if *gotExe != target {
		t.Errorf("exec 路径 = %q, 期望 %q", *gotExe, target)
	}
	if len(*gotArgv) == 0 {
		t.Fatal("argv 不应为空")
	}
	if (*gotArgv)[0] != target {
		t.Errorf("argv[0] = %q, 期望绝对路径 %q", (*gotArgv)[0], target)
	}
	if want := os.Args[1:]; len(*gotArgv)-1 != len(want) {
		t.Errorf("透传参数个数 = %d, 期望 %d", len(*gotArgv)-1, len(want))
	}
}

func TestRestartFallsBackToResolvedPath(t *testing.T) {
	svc := testService()
	gotExe, _ := captureExec(t)

	want, err := resolveExecutable()
	if err != nil {
		t.Fatalf("resolveExecutable 失败: %v", err)
	}

	if err := svc.Restart(0, ""); err != nil {
		t.Fatalf("Restart 返回错误: %v", err)
	}
	if *gotExe != want {
		t.Errorf("exec 路径 = %q, 期望 %q", *gotExe, want)
	}
}

func TestRestartHonorsDelay(t *testing.T) {
	svc := testService()
	captureExec(t)

	const delay = 40 * time.Millisecond
	start := time.Now()
	if err := svc.Restart(delay, "/tmp/inventory-server"); err != nil {
		t.Fatalf("Restart 返回错误: %v", err)
	}
	if elapsed := time.Since(start); elapsed < delay {
		t.Errorf("延迟未生效，仅等待 %v", elapsed)
	}
}

func TestRestartPropagatesExecError(t *testing.T) {
	svc := testService()

	prev := execProcess
	execProcess = func(string, []string, []string) error { return errors.New("exec 被拒绝") }
	t.Cleanup(func() { execProcess = prev })

	err := svc.Restart(0, "/tmp/inventory-server")
	if err == nil || !strings.Contains(err.Error(), "exec 被拒绝") {
		t.Errorf("应向上返回 exec 错误，实际 %v", err)
	}
}

// ---------------------------------------------------------------------------
// .old 自愈
// ---------------------------------------------------------------------------

func TestResolveExecutableSelfHealsFromBackup(t *testing.T) {
	dir := resolvedTempDir(t)
	official := filepath.Join(dir, "inventory-server")
	backup := official + backupSuffix

	for _, p := range []string{official, backup} {
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatalf("写入 %s 失败: %v", p, err)
		}
	}

	// 模拟：进程是从备份文件启动的
	stubExecutable(t, backup)

	got, err := resolveExecutable()
	if err != nil {
		t.Fatalf("resolveExecutable 失败: %v", err)
	}
	if got != official {
		t.Errorf("应自愈回正式路径 %q, 实际 %q", official, got)
	}
}

func TestResolveExecutableKeepsBackupWhenOfficialMissing(t *testing.T) {
	dir := resolvedTempDir(t)
	backup := filepath.Join(dir, "inventory-server"+backupSuffix)
	if err := os.WriteFile(backup, []byte("x"), 0o755); err != nil {
		t.Fatalf("写入备份失败: %v", err)
	}

	stubExecutable(t, backup)

	got, err := resolveExecutable()
	if err != nil {
		t.Fatalf("resolveExecutable 失败: %v", err)
	}
	if got != backup {
		t.Errorf("正式文件缺失时应保留备份路径 %q, 实际 %q", backup, got)
	}
}

func TestResolveExecutablePropagatesError(t *testing.T) {
	prev := executablePath
	executablePath = func() (string, error) { return "", errors.New("拿不到路径") }
	t.Cleanup(func() { executablePath = prev })

	if _, err := resolveExecutable(); err == nil {
		t.Error("上游报错时应返回错误")
	}
}

// ---------------------------------------------------------------------------
// 端到端：替换 → 重启，必须指向新版本
// ---------------------------------------------------------------------------

func TestRestartAfterReplaceRunsNewBinary(t *testing.T) {
	dir := resolvedTempDir(t)

	// 模拟正在运行的旧版本
	official := filepath.Join(dir, "inventory-server")
	if err := os.WriteFile(official, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("写入旧版本失败: %v", err)
	}

	// 模拟 /proc/self/exe 的语义：它返回「当前进程映像所在的路径」。
	// 替换前是正式路径；旧 inode 被改名成 .old 之后，同一个 inode 的路径就变成了 .old。
	runningPath := official
	prev := executablePath
	executablePath = func() (string, error) { return runningPath, nil }
	t.Cleanup(func() { executablePath = prev })

	// 准备待安装的新版本
	staged := filepath.Join(dir, "downloaded-new")
	if err := os.WriteFile(staged, []byte("NEW"), 0o755); err != nil {
		t.Fatalf("写入新版本失败: %v", err)
	}

	result, err := replaceExecutable(staged, nil)
	if err != nil {
		t.Fatalf("replaceExecutable 失败: %v", err)
	}
	if result.BinaryPath != official {
		t.Fatalf("BinaryPath = %q, 期望 %q", result.BinaryPath, official)
	}
	if result.BackupPath != official+backupSuffix {
		t.Errorf("BackupPath = %q", result.BackupPath)
	}

	// 替换生效后，进程映像所在的路径已经变成备份
	runningPath = result.BackupPath
	if runningPath == official {
		t.Fatal("测试前提不成立：替换后进程映像路径应指向备份")
	}

	gotExe, gotArgv := captureExec(t)
	svc := testService()
	if err := svc.Restart(0, result.BinaryPath); err != nil {
		t.Fatalf("Restart 返回错误: %v", err)
	}

	if *gotExe != official {
		t.Errorf("重启应指向 %q, 实际 %q（若为 %q 则等于又启动了旧版本）",
			official, *gotExe, result.BackupPath)
	}
	if len(*gotArgv) == 0 || (*gotArgv)[0] != official {
		t.Errorf("argv[0] 应为 %q, 实际 %v", official, *gotArgv)
	}

	// 正式路径上的内容必须是新版本
	content, err := os.ReadFile(*gotExe)
	if err != nil {
		t.Fatalf("读取待启动文件失败: %v", err)
	}
	if string(content) != "NEW" {
		t.Errorf("待启动文件内容 = %q, 期望 NEW", content)
	}

	// 备份仍是旧版本，可回滚
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("读取备份失败: %v", err)
	}
	if string(backup) != "OLD" {
		t.Errorf("备份内容 = %q, 期望 OLD", backup)
	}
}
