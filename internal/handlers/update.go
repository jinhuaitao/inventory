package handlers

import (
	"net/http"
	"time"
)

// 重启前留给浏览器渲染「正在重启」页面的时间。
const restartDelay = 1200 * time.Millisecond

// restartCountdown 返回「正在重启」页面上自动刷新的倒计时秒数。
//
// ⚠️ 必须显著大于 restartDelay。页面在倒计时结束前不会去探测就绪探针，
// 而 restartDelay 这段时间里旧进程仍在正常响应 /readyz —— 倒计时若小于
// 该延迟，探测会命中尚未退出的旧进程，页面「刷新成功」但看到的还是
// 旧版本 / 恢复前的数据。这里用延迟 + 4 秒的余量，并保底 5 秒。
func restartCountdown() int {
	secs := int(restartDelay.Seconds()) + 4
	if secs < 5 {
		secs = 5
	}
	return secs
}

// UpdatePage 展示系统更新页面（仅管理员）。
func (h *Handler) UpdatePage(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}
	h.renderUpdate(w, r, http.StatusOK, map[string]string{})
}

// UpdateCheck 手动触发一次版本检查。
func (h *Handler) UpdateCheck(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	status := h.updater.Check(r.Context())
	if status.Err != "" {
		h.logger.Warn("手动检查更新未成功", "原因", status.Err)
		h.redirectWith(w, r, "/admin/update", "warning", status.Err)
		return
	}
	if status.UpdateAvailable {
		h.redirectWith(w, r, "/admin/update", "info",
			"发现新版本 "+status.Latest+"，可查看下方说明后立即更新")
		return
	}
	h.redirectWith(w, r, "/admin/update", "success", "当前已是最新版本（"+status.Current+"）")
}

// UpdateApply 下载并应用新版本，随后重启服务。
func (h *Handler) UpdateApply(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	status := h.updater.Cached()
	if !status.UpdateAvailable {
		h.redirectWith(w, r, "/admin/update", "warning", "当前没有可用的更新，请先检查更新")
		return
	}

	result, err := h.updater.Apply(r.Context(), status)
	if err != nil {
		h.logger.Error("应用更新失败", "从", status.Current, "到", status.Latest, "错误", err)
		h.renderUpdate(w, r, http.StatusInternalServerError, map[string]string{"form": err.Error()})
		return
	}

	// Windows 等无法运行时替换的平台：给出手动操作指引
	if result.ManualHint != "" {
		h.redirectWith(w, r, "/admin/update", "warning", result.ManualHint)
		return
	}

	// 已替换文件，渲染重启提示页后重启进程
	h.render(w, r, http.StatusOK, "update/restarting.html", "正在重启", "update", map[string]any{
		"From":      result.FromVersion,
		"To":        result.ToVersion,
		"Backup":    result.BackupPath,
		"Refresh":   "/admin/update",
		"Countdown": restartCountdown(),
	})

	// 把响应推回浏览器后再替换进程映像
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		// 必须传入替换后的正式路径：此刻 /proc/self/exe 已指向 .old 备份，
		// 若让 Restart 自行解析会重启回旧版本。
		if err := h.updater.Restart(restartDelay, result.BinaryPath); err != nil {
			h.logger.Error("重启服务失败，请手动重启以完成更新", "错误", err)
		}
	}()
}

// renderUpdate 渲染更新页面。
func (h *Handler) renderUpdate(w http.ResponseWriter, r *http.Request, status int, errs map[string]string) {
	status2 := h.updater.Cached()

	h.render(w, r, status, "update/index.html", "系统更新", "update", map[string]any{
		"Status":       status2,
		"Enabled":      h.updater.Enabled(),
		"Repo":         h.updater.Repo(),
		"Platform":     status2.Platform,
		"CurrentBuild": Version,
		"Errors":       errs,
	})
}
