package handlers

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"inventory/internal/database"
	"inventory/internal/middleware"
	"inventory/internal/utils"
)

// maxRestoreUpload 限制上传备份文件的大小。
//
// 与中间件里 multipart 请求的全局上限保持一致：body 的体积在 CSRF 中间件
// 解析 multipart 时就已经被 MaxBytesReader 卡住，这里再用文件头里声明的
// 大小复核一次，给出更明确的提示。
const maxRestoreUpload = middleware.MaxUploadBytes

// maintenancePath 是数据维护页的地址。
const maintenancePath = "/admin/maintenance"

// ---------------------------------------------------------------------------
// 页面
// ---------------------------------------------------------------------------

// MaintenancePage 展示数据维护页面（仅管理员）。
func (h *Handler) MaintenancePage(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}
	h.renderMaintenance(w, r, http.StatusOK, nil)
}

// renderMaintenance 渲染数据维护页，并汇总页面所需的全部状态。
func (h *Handler) renderMaintenance(w http.ResponseWriter, r *http.Request, status int, errs map[string]string) {
	ctx := r.Context()

	backups, err := database.ListBackups(h.cfg.BackupDir)
	if err != nil {
		h.logger.Error("读取备份列表失败", "目录", h.cfg.BackupDir, "错误", err)
	}

	pending, err := database.PendingRestore(ctx, h.cfg.DBPath)
	if err != nil {
		h.logger.Error("读取待恢复文件失败", "错误", err)
	}

	// 预演一次演示数据清理，让页面直接显示当前库里有多少演示数据。
	// DryRun 在事务内完成统计后回滚，不会产生任何改动。
	preview, err := database.PurgeDemoData(ctx, h.store.DB(), database.PurgeOptions{DryRun: true}, nil)
	if err != nil {
		h.logger.Error("统计演示数据失败", "错误", err)
		// 传零值而不是 nil：模板里要读它的字段，nil 指针会在渲染时炸掉。
		preview = &database.PurgeResult{}
	}

	h.render(w, r, status, "maintenance/index.html", "数据维护", "maintenance", map[string]any{
		"Backups":     backups,
		"BackupDir":   h.cfg.BackupDir,
		"BackupKeep":  h.cfg.BackupKeep,
		"DBPath":      h.cfg.DBPath,
		"Pending":     pending,
		"Preview":     preview,
		"HasDemoData": !preview.Empty(),
		"Errors":      errs,
	})
}

// ---------------------------------------------------------------------------
// 备份
// ---------------------------------------------------------------------------

// BackupCreate 立即创建一份备份，并按配置轮转旧备份。
func (h *Handler) BackupCreate(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	path, err := database.Backup(r.Context(), h.store.DB(), h.cfg.BackupDir, false)
	if err != nil {
		h.logger.Error("创建备份失败", "错误", err)
		h.redirectWith(w, r, maintenancePath, "error", "创建备份失败："+err.Error())
		return
	}

	// 立刻回读校验，确认拿到的不是「看起来像备份」的空壳文件。
	sum, err := database.InspectBackup(r.Context(), path)
	if err != nil {
		h.logger.Error("备份校验失败", "文件", path, "错误", err)
		h.redirectWith(w, r, maintenancePath, "error",
			fmt.Sprintf("备份已生成但校验失败，请勿使用：%s", err.Error()))
		return
	}

	msg := fmt.Sprintf("备份完成：%s（%s）", filepath.Base(path), utils.FormatBytes(sum.Size))
	if !sum.IntegrityOK {
		msg = fmt.Sprintf("备份 %s 未通过完整性检查，请勿使用", filepath.Base(path))
		h.redirectWith(w, r, maintenancePath, "error", msg)
		return
	}
	msg += "，完整性检查通过"

	if removed, err := database.PruneBackups(h.cfg.BackupDir, h.cfg.BackupKeep); err != nil {
		h.logger.Warn("清理旧备份失败", "错误", err)
	} else if len(removed) > 0 {
		msg += fmt.Sprintf("，已轮转清理 %d 份旧备份", len(removed))
	}

	h.redirectWith(w, r, maintenancePath, "success", msg)
}

// BackupDownload 下载指定的备份文件。
func (h *Handler) BackupDownload(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	name := r.URL.Query().Get("name")
	full, err := database.ResolveBackupPath(h.cfg.BackupDir, name)
	if err != nil {
		// 名字来自 URL，可能是手工拼的或已被篡改，这里只回提示不回显输入。
		h.logger.Warn("拒绝非法的备份下载请求", "名称", name, "错误", err)
		h.redirectWith(w, r, maintenancePath, "error", "备份文件名不合法："+err.Error())
		return
	}

	f, err := os.Open(full)
	if err != nil {
		h.redirectWith(w, r, maintenancePath, "error", "备份文件不存在或无法读取")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	if info.IsDir() {
		h.redirectWith(w, r, maintenancePath, "error", "备份文件名不合法")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	// 用 RFC 5987 的 filename* 形式，手工命名的中文备份名也能正确落盘。
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(info.Name()))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// BackupDelete 删除指定的备份文件。
func (h *Handler) BackupDelete(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	name := formString(r, "name")
	if err := database.DeleteBackup(h.cfg.BackupDir, name); err != nil {
		h.logger.Warn("删除备份失败", "名称", name, "错误", err)
		h.redirectWith(w, r, maintenancePath, "error", err.Error())
		return
	}

	h.logger.Info("已删除备份", "名称", name)
	h.redirectWith(w, r, maintenancePath, "success", "已删除备份 "+name)
}

// ---------------------------------------------------------------------------
// 恢复
// ---------------------------------------------------------------------------

// RestoreUpload 接收上传的备份文件，校验后暂存并重启服务使其生效。
//
// 不直接覆盖数据库文件：运行中的连接池持有该文件，就地替换会让连接指向
// 已被替换的 inode。改为「暂存 + 启动时换入」，由 ApplyPendingRestore 完成。
func (h *Handler) RestoreUpload(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.logger.Warn("解析上传表单失败", "错误", err)
		h.redirectWith(w, r, maintenancePath, "error",
			fmt.Sprintf("读取上传文件失败，单个备份不得超过 %s", utils.FormatBytes(maxRestoreUpload)))
		return
	}

	file, header, err := r.FormFile("backup")
	if err != nil {
		h.redirectWith(w, r, maintenancePath, "error", "请选择要恢复的备份文件")
		return
	}
	defer file.Close()

	if header.Size > maxRestoreUpload {
		h.redirectWith(w, r, maintenancePath, "error",
			fmt.Sprintf("备份文件过大（%s），上限为 %s",
				utils.FormatBytes(header.Size), utils.FormatBytes(maxRestoreUpload)))
		return
	}

	// 先落到临时文件：InspectBackup 需要按路径打开，而且上传内容不可信，
	// 校验通过之前不应进入数据库目录。
	tmp, err := os.CreateTemp("", "inventory-restore-*.db")
	if err != nil {
		h.serverError(w, r, err)
		return
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmp, file); err != nil {
		_ = tmp.Close()
		h.logger.Warn("写入上传文件失败", "错误", err)
		h.redirectWith(w, r, maintenancePath, "error", "写入上传文件失败："+err.Error())
		return
	}
	if err := tmp.Close(); err != nil {
		h.serverError(w, r, err)
		return
	}

	sum, err := database.StageRestore(r.Context(), h.cfg.DBPath, tmpPath)
	if err != nil {
		h.logger.Warn("拒绝恢复上传的备份", "来源", header.Filename, "错误", err)
		h.redirectWith(w, r, maintenancePath, "error",
			fmt.Sprintf("已拒绝恢复 %s：%s", header.Filename, err.Error()))
		return
	}

	h.logger.Warn("已暂存待恢复的数据库，重启后生效",
		"来源", header.Filename,
		"大小", sum.Size,
		"商品数", sum.Counts["products"],
		"用户数", sum.Counts["users"],
	)

	h.render(w, r, http.StatusOK, "maintenance/restoring.html", "正在恢复", "maintenance", map[string]any{
		"Source":  header.Filename,
		"Size":    sum.Size,
		"Counts":  sum.Counts,
		"Refresh": maintenancePath,
	})

	// 把响应推回浏览器后再替换进程映像，否则用户看不到这个页面。
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		// 这里没有替换过二进制，让 Restart 自行解析当前可执行文件是安全的。
		if err := h.updater.Restart(restartDelay, ""); err != nil {
			h.logger.Error("恢复后重启服务失败，请手动重启以完成恢复", "错误", err)
		}
	}()
}

// RestoreDiscard 丢弃已暂存但尚未生效的待恢复文件。
func (h *Handler) RestoreDiscard(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	if err := database.DiscardPendingRestore(h.cfg.DBPath); err != nil {
		h.logger.Error("丢弃待恢复文件失败", "错误", err)
		h.redirectWith(w, r, maintenancePath, "error", err.Error())
		return
	}

	h.logger.Info("已丢弃待恢复的数据库")
	h.redirectWith(w, r, maintenancePath, "success", "已放弃待恢复的数据库，当前数据未受影响")
}

// ---------------------------------------------------------------------------
// 演示数据清理
// ---------------------------------------------------------------------------

// PurgeDemoData 删除内置演示数据。
//
// 清理不可回滚，因此在真正删除之前会先自动做一份安全备份，
// 让用户随时能把数据找回来。
func (h *Handler) PurgeDemoData(w http.ResponseWriter, r *http.Request) {
	if !h.canManageUsers(w, r) {
		return
	}

	ctx := r.Context()

	// 先预演一次：没有任何演示数据时直接返回，避免无谓地产生备份文件。
	preview, err := database.PurgeDemoData(ctx, h.store.DB(), database.PurgeOptions{DryRun: true}, nil)
	if err != nil {
		h.logger.Error("统计演示数据失败", "错误", err)
		h.redirectWith(w, r, maintenancePath, "error", "统计演示数据失败："+err.Error())
		return
	}
	if preview.Empty() {
		h.redirectWith(w, r, maintenancePath, "info", "未发现内置演示数据，无需清理")
		return
	}

	// 安全备份：清理不可回滚，先留一个还原点。
	safety, err := database.Backup(ctx, h.store.DB(), h.cfg.BackupDir, false)
	if err != nil {
		h.logger.Error("清理前的安全备份失败，已中止清理", "错误", err)
		h.redirectWith(w, r, maintenancePath, "error",
			"清理前的安全备份失败，为安全起见已中止清理："+err.Error())
		return
	}

	res, err := database.PurgeDemoData(ctx, h.store.DB(), database.PurgeOptions{}, h.logger)
	if err != nil {
		h.logger.Error("清理演示数据失败", "错误", err)
		h.redirectWith(w, r, maintenancePath, "error", "清理演示数据失败："+err.Error())
		return
	}

	msg := fmt.Sprintf("已清理演示数据：商品 %d、库存流水 %d、分类 %d、供应商 %d；安全备份 %s",
		res.Products, res.Movements, res.Categories, res.Suppliers, filepath.Base(safety))

	if kept := strings.Join(append(append([]string{}, res.SkippedCategories...), res.SkippedSuppliers...), "、"); kept != "" {
		msg += "；仍被商品引用的 " + kept + " 已保留"
	}

	if removed, err := database.PruneBackups(h.cfg.BackupDir, h.cfg.BackupKeep); err != nil {
		h.logger.Warn("清理旧备份失败", "错误", err)
	} else if len(removed) > 0 {
		msg += fmt.Sprintf("；已轮转清理 %d 份旧备份", len(removed))
	}

	h.redirectWith(w, r, maintenancePath, "success", msg)
}
