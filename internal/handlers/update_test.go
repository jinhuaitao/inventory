package handlers

import (
	"testing"
	"time"
)

// 「正在重启」页面在倒计时结束前不会去探测就绪探针，而 restartDelay 期间
// 旧进程仍在正常响应 /readyz。倒计时若不大于该延迟，探测就会命中尚未退出的
// 旧进程 —— 页面「刷新成功」，但看到的还是旧版本 / 恢复前的数据。
func TestRestartCountdownExceedsRestartDelay(t *testing.T) {
	got := time.Duration(restartCountdown()) * time.Second

	if got < 5*time.Second {
		t.Errorf("倒计时 = %v，至少应为 5s", got)
	}
	if got <= restartDelay {
		t.Errorf("倒计时 = %v，必须大于重启延迟 %v", got, restartDelay)
	}
}
