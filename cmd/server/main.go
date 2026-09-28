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
	flag.Parse()

	if *showVersion {
		fmt.Printf("inventory %s (commit %s, built %s)\n", version, commit, date)
		return nil
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

	// ---------- 数据库 ----------
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer setupCancel()

	if err := database.Migrate(setupCtx, db); err != nil {
		return err
	}
	logger.Info("数据库已就绪", "路径", cfg.DBPath)

	seedOpts := database.SeedOptions{
		AdminUsername: cfg.SeedAdminUsername,
		AdminEmail:    cfg.SeedAdminEmail,
		AdminPassword: cfg.SeedAdminPassword,
		WithDemoData:  !cfg.IsProduction(),
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
	// ---------- 后台任务 ----------
	cleanupCtx, stopCleanup := context.WithCancel(context.Background())
	defer stopCleanup()
	go runCleanupLoop(cleanupCtx, store, logger)

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
