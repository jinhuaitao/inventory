// Command server 启动库存管理系统 HTTP 服务。
//
// 构建：
//
//	go build -o inventory-server ./cmd/server
//
// 运行：
//
//	./inventory-server
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"inventory/internal/auth"
	"inventory/internal/config"
	"inventory/internal/database"
	"inventory/internal/handlers"
	"inventory/internal/middleware"
	"inventory/internal/services"
	"inventory/internal/updater"
	"inventory/internal/utils"
	"inventory/web"
)

// 构建信息，由 -ldflags 注入。
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	// updateRepo 是默认的更新仓库（owner/repo），由 CI 注入 github.repository。
	updateRepo = ""
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "打印版本信息后退出")
	checkUpdate := flag.Bool("check-update", false, "检查是否有新版本后退出")
	purgeDemo := flag.Bool("purge-demo-data", false, "删除内置演示数据（分类 / 供应商 / 商品）后退出")
	purgeDryRun := flag.Bool("dry-run", false, "配合 --purge-demo-data：只统计将删除的数据，不做实际改动")
	purgeLegacy := flag.Bool("purge-legacy-demo", false,
		"配合 --purge-demo-data：额外把「SKU 命中内置演示 SKU 但无 is_demo 标记」的商品也视为演示数据（旧库兼容，请先用 --dry-run 确认）")
	backupTo := flag.String("backup-db", "", "备份数据库到指定文件或目录后退出（服务运行中执行也安全）")
	backupForce := flag.Bool("force", false, "配合 --backup-db：目标文件已存在时覆盖")
	backupKeep := flag.Int("keep", 0, "配合 --backup-db 且目标是目录：只保留最新 N 份自动命名的备份，0 表示不清理")
	flag.Parse()

	if *showVersion {
		fmt.Printf("inventory %s (commit %s, built %s)\n", version, commit, date)
		return nil
	}

	if *backupForce && *backupTo == "" {
		return errors.New("--force 需要与 --backup-db 一起使用")
	}
	if *backupKeep != 0 && *backupTo == "" {
		return errors.New("--keep 需要与 --backup-db 一起使用")
	}

	if *purgeLegacy && !*purgeDemo {
		return errors.New("--purge-legacy-demo 需要与 --purge-demo-data 一起使用")
	}

	// --purge-demo-data / --backup-db 是离线维护命令，只需要能定位数据库，
	// 不依赖会话密钥等运行期配置，因此在 config.Load() 之前处理。
	if *purgeDemo || *backupTo != "" {
		sc := config.LoadStorageOnly()
		if *purgeDemo {
			return runPurgeDemoData(sc, newLogger(sc), *purgeDryRun, *purgeLegacy)
		}
		return runBackupDB(sc, newLogger(sc), *backupTo, *backupForce, *backupKeep)
	}

	// 让配置层知道编译时注入的默认更新仓库
	config.DefaultUpdateRepo = updateRepo

	// ---------- 配置 ----------
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)

	if *checkUpdate {
		return runUpdateCheck(cfg, logger)
	}

	logger.Info("正在启动库存管理系统",
		"版本", version,
		"环境", cfg.Env,
		"监听地址", cfg.Addr,
	)

	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer setupCancel()

	// ---------- 应用待恢复的数据库 ----------
	// Web「数据维护」页上传的备份会先暂存，在这里才真正换入。
	// ⚠️ 必须早于 database.Open：一旦有连接持有数据库文件，
	//    替换它会让连接指向已被替换的 inode。
	if result, err := database.ApplyPendingRestore(setupCtx, cfg.DBPath, logger); err != nil {
		// 恢复失败不阻止启动 —— 服务照常起来，管理员可以进页面重新上传。
		logger.Error("应用待恢复的数据库失败，已沿用原数据库", "错误", err)
	} else if result != nil && result.Applied {
		logger.Warn("已应用待恢复的数据库",
			"原库归档", result.Archived,
			"商品数", result.Counts["products"],
			"用户数", result.Counts["users"],
		)
	}

	// ---------- 数据库 ----------
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := database.Migrate(setupCtx, db); err != nil {
		return err
	}
	logger.Info("数据库已就绪", "路径", cfg.DBPath)

	seedOpts := database.SeedOptions{
		AdminUsername: cfg.SeedAdminUsername,
		AdminEmail:    cfg.SeedAdminEmail,
		AdminPassword: cfg.SeedAdminPassword,
		WithDemoData:  cfg.SeedDemoData,
	}
	if err := database.Seed(setupCtx, db, seedOpts, logger); err != nil {
		return err
	}

	// ---------- 依赖装配 ----------
	store := services.New(db, logger)

	renderer, err := utils.NewRenderer(web.FS, !cfg.IsProduction(), logger)
	if err != nil {
		return err
	}

	sessions := auth.NewSessionManager(store, cfg)

	updaterSvc := updater.New(updater.Options{
		Enabled:  cfg.UpdateEnabled,
		Repo:     cfg.UpdateRepo,
		Token:    cfg.UpdateToken,
		Current:  version,
		Interval: cfg.UpdateInterval,
	}, logger)

	mw := middleware.New(sessions, cfg, logger)

	handlers.Version = version
	h := handlers.New(store, renderer, sessions, updaterSvc, mw, cfg, logger)

	// 载入运行期设置：自助注册开关（页面上的设置优先于环境变量默认值）。
	// 必须早于对外提供 HTTP 服务 —— 内存里的零值是「关闭」，
	// 漏掉这一步会让一个本来开放注册的站点静默变成关闭。
	if err := h.InitRegistration(setupCtx); err != nil {
		return err
	}

	// ---------- HTTP 服务 ----------
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	// 启动提示
	if cfg.SeedAdminPassword == "Admin@12345" && !cfg.IsProduction() {
		logger.Info("默认管理员账号已就绪",
			"用户名", cfg.SeedAdminUsername,
			"密码", cfg.SeedAdminPassword,
			"提醒", "生产环境请通过 INVENTORY_ADMIN_PASSWORD 覆盖，并登录后立即修改",
		)
	}

	// 自助注册的生效值可能已被管理员在页面上覆盖，与当前环境变量并不一致。
	// 明确打印出来，免得排查「注册入口怎么不见了」时绕弯路。
	regEnabled, regExplicit := h.RegistrationStatus()
	regSource := "环境变量 INVENTORY_ALLOW_REGISTRATION"
	if regExplicit {
		regSource = "用户管理页的设置"
	}
	logger.Info("自助注册状态已就绪", "是否开放", regEnabled, "来源", regSource)
	// ---------- 后台任务 ----------
	cleanupCtx, stopCleanup := context.WithCancel(context.Background())
	defer stopCleanup()
	go runCleanupLoop(cleanupCtx, store, logger)

	// 定期回源自助注册开关，吸收本进程之外的改动（多实例部署、或直接改数据库）。
	// 进程内自己改的会立刻回填缓存，所以这里只影响外部改动的收敛速度。
	go h.RunRegistrationRefresh(cleanupCtx, handlers.RegistrationRefreshInterval)

	// 定时检查新版本（未配置更新仓库时自动跳过）
	updaterSvc.Start(cleanupCtx)

	// ---------- 启动与优雅关闭 ----------
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务已启动", "访问地址", displayURL(cfg.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		return err
	case sig := <-quit:
		logger.Info("收到退出信号，开始优雅关闭", "信号", sig.String())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("优雅关闭超时，强制退出", "错误", err)
		return err
	}

	logger.Info("服务已安全退出")
	return nil
}

// runCleanupLoop 周期清理过期会话、重置令牌与陈旧的登录记录。
func runCleanupLoop(ctx context.Context, store *services.Store, logger *slog.Logger) {
	run := func() {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		result, err := store.Cleanup(cctx)
		if err != nil {
			logger.Warn("清理过期数据失败", "错误", err)
			return
		}
		total := int64(0)
		for _, n := range result {
			total += n
		}
		if total > 0 {
			logger.Debug("清理过期数据完成",
				"会话", result["sessions"],
				"重置令牌", result["password_resets"],
				"登录记录", result["login_attempts"],
			)
		}
	}

	run()

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// runUpdateCheck 处理 --check-update 参数：查询一次版本信息并打印结果后退出。
// 适合放进部署脚本或定时任务里做版本巡检。
func runUpdateCheck(cfg *config.Config, logger *slog.Logger) error {
	if !cfg.UpdateConfigured() {
		fmt.Println("未配置更新仓库，无法检查更新。")
		fmt.Println("请设置环境变量 INVENTORY_UPDATE_REPO=owner/repo，")
		fmt.Println("或在构建时通过 -ldflags \"-X main.updateRepo=owner/repo\" 注入。")
		return nil
	}

	svc := updater.New(updater.Options{
		Enabled: cfg.UpdateEnabled,
		Repo:    cfg.UpdateRepo,
		Token:   cfg.UpdateToken,
		Current: version,
	}, logger)

	st := svc.Check(context.Background())

	fmt.Printf("当前版本: %s\n", st.Current)
	fmt.Printf("运行平台: %s\n", st.Platform)
	if st.Latest != "" {
		fmt.Printf("最新版本: %s\n", st.Latest)
	}
	if st.Err != "" {
		fmt.Printf("检查结果: %s\n", st.Err)
	}

	switch {
	case st.UpdateAvailable:
		fmt.Println("发现新版本，可登录后在「系统更新」页面一键升级。")
		if st.ReleaseURL != "" {
			fmt.Printf("发布说明: %s\n", st.ReleaseURL)
		}
	case st.Err != "":
		// 错误信息已在上面打印，此处不再重复结论
	case st.Comparable && st.Latest != "":
		fmt.Println("当前已是最新版本。")
	}
	return nil
}

// runPurgeDemoData 处理 --purge-demo-data：删除首次启动时写入的内置演示数据。
//
// 演示数据只在「数据库为空」时写入，所以旧版本部署升级上来之后并不会自动消失，
// 需要显式执行一次本命令。建议先加 --dry-run 确认将要删除的内容。
//
// 默认只删带 is_demo 标记的行。SKU 命中内置演示 SKU 但**没有**标记的商品
// 只会被报告出来（见 PurgeResult.LegacySKUOnly），不会被删 ——
// 因为 SKU 是用户可自由填写的字段，无条件按 SKU 删除会连带清掉用户自建的商品
// 及其全部库存流水。确认那些确实是旧版遗留数据后，再加 --purge-legacy-demo。
func runPurgeDemoData(cfg *config.Config, logger *slog.Logger, dryRun, legacySKU bool) error {
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := database.Migrate(ctx, db); err != nil {
		return err
	}

	res, err := database.PurgeDemoData(ctx, db,
		database.PurgeOptions{DryRun: dryRun, LegacySKUMatch: legacySKU}, logger)
	if err != nil {
		return err
	}

	fmt.Printf("数据库: %s\n", cfg.DBPath)
	if dryRun {
		fmt.Println("模式: --dry-run（以下数据**将被**删除，当前未做任何改动）")
	} else {
		fmt.Println("模式: 实际清理")
	}

	if res.Empty() {
		fmt.Println("结果: 未发现内置演示数据，无需清理。")
		reportLegacyCandidates(res, legacySKU)
		return nil
	}

	fmt.Println("结果:")
	fmt.Printf("  商品     %d\n", res.Products)
	fmt.Printf("  库存流水 %d\n", res.Movements)
	fmt.Printf("  分类     %d\n", res.Categories)
	fmt.Printf("  供应商   %d\n", res.Suppliers)

	if len(res.SkippedCategories) > 0 {
		fmt.Printf("  保留分类（仍被商品引用）: %s\n", strings.Join(res.SkippedCategories, "、"))
	}
	if len(res.SkippedSuppliers) > 0 {
		fmt.Printf("  保留供应商（仍被商品引用）: %s\n", strings.Join(res.SkippedSuppliers, "、"))
	}

	reportLegacyCandidates(res, legacySKU)

	if dryRun {
		fmt.Println("确认无误后去掉 --dry-run 再执行一次即可完成清理。")
	} else {
		fmt.Println("如需回滚，请使用清理前备份的数据库文件。")
	}
	return nil
}

// reportLegacyCandidates 提示那些「SKU 命中但没有 is_demo 标记」的商品。
//
// 这批数据默认不动：它们既可能是旧版遗留的演示商品，也可能是用户自己
// 用同样编号建的正常商品，程序无法分辨，只能交给管理员判断。
func reportLegacyCandidates(res *database.PurgeResult, legacySKU bool) {
	if !res.HasLegacyCandidates() {
		return
	}
	fmt.Println()
	if legacySKU {
		fmt.Printf("  另有 %d 个 SKU 命中内置演示编号的商品已按 --purge-legacy-demo 一并处理。\n",
			len(res.LegacySKUOnly))
		return
	}
	fmt.Println("注意: 发现以下商品的 SKU 与内置演示编号相同，但**没有**演示标记，因此未被删除：")
	for _, sku := range res.LegacySKUOnly {
		fmt.Printf("  - %s\n", sku)
	}
	fmt.Println("  如果确认这些是旧版本遗留的演示数据，请加 --purge-legacy-demo 重跑；")
	fmt.Println("  如果是您自己创建的商品，请忽略本提示（默认行为不会动它们）。")
}

// runBackupDB 处理 --backup-db：把数据库备份成一个可直接拷走的单文件。
//
// 备份用的是 SQLite 的 VACUUM INTO，取读事务快照，因此**不需要停服务**，
// 产出的文件已经把 WAL 合并进去，不会出现「只拷了 inventory.db 却丢了最近数据」。
func runBackupDB(cfg *config.Config, logger *slog.Logger, dest string, force bool, keep int) error {
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := database.Migrate(ctx, db); err != nil {
		return err
	}

	path, err := database.Backup(ctx, db, dest, force)
	if err != nil {
		return err
	}

	// 立刻回读校验，确认备份真的可用，而不是只生成了一个文件
	sum, err := database.InspectBackup(ctx, path)
	if err != nil {
		return fmt.Errorf("备份已生成，但校验失败：%w", err)
	}

	fmt.Printf("源数据库: %s\n", cfg.DBPath)
	fmt.Printf("备份文件: %s\n", sum.Path)
	fmt.Printf("文件大小: %.2f MB\n", float64(sum.Size)/(1024*1024))
	if sum.IntegrityOK {
		fmt.Println("完整性检查: ok")
	} else {
		fmt.Println("完整性检查: 未通过（请勿使用该文件）")
	}

	fmt.Println("记录数:")
	for _, table := range []string{"users", "categories", "suppliers", "products", "stock_movements"} {
		fmt.Printf("  %-16s %d\n", table, sum.Counts[table])
	}

	if keep > 0 {
		removed, err := database.PruneBackups(filepath.Dir(sum.Path), keep)
		if err != nil {
			return err
		}
		if len(removed) > 0 {
			fmt.Printf("已清理旧备份 %d 份: %s\n", len(removed), strings.Join(removed, ", "))
		}
	}

	fmt.Println()
	fmt.Println("换机器时把该文件拷过去即可，恢复步骤见 deploy/README.md 的「数据库备份与迁移」。")
	return nil
}

// newLogger 依据环境构造结构化日志器：生产用 JSON，开发用可读文本。
func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.IsProduction() {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(handler)
}

// displayURL 把监听地址转换成可点击的访问地址。
func displayURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "http://localhost:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return "http://" + addr
}
