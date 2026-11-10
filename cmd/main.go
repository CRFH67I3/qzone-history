package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"qzone-history/internal/delivery/app"
	"qzone-history/internal/infrastructure/config"
	"qzone-history/internal/infrastructure/persistence"
	"qzone-history/internal/infrastructure/qzone_api"
	"qzone-history/internal/usecase"
	"qzone-history/pkg/database"
	"qzone-history/pkg/database/sqlite"
	"qzone-history/version"
	"syscall"
)

const banner = "" +
	"  ___  ___  ____  _  _  ____    _   _ ___ ____ ____ _____ ___  ______   __\n" +
	" / _ \\/ _ \\|__  /| \\| |/ ___|  | | | |_ _/ ___/ ___|_   _| _ \\|  _ \\ \\ / /\n" +
	"| (_) | |_| |/ /_| .' |  _|    | |_| || |\\___ \\___ \\ | | | |_) | |_) \\ V /\n" +
	" \\__\\_\\___, /____|_|\\_|_|       \\___/|___|____/____/ |_| |____/|____/ |_|\n" +
	"       |___/\n"

func main() {
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	dbPath := flag.String("db", "./app.db", "SQLite 数据库路径")
	outDir := flag.String("out", ".", "导出目录（JSON/CSV/HTML 都会写到这里）")
	refetch := flag.Bool("refetch", false, "清空本地说数据重新抓取（旧的脏数据会被删除）")
	activities := flag.Bool("activities", false, "同时抓取互动消息（点赞/浏览/留言记录，数据较脏，默认关闭）")
	exportOnly := flag.Bool("export-only", false, "不联网，只用本地数据库重新导出")
	force := flag.Bool("login", false, "强制重新扫码登录（忽略已保存的登录态）")
	listen := flag.String("listen", "", "指定二维码/Cookie 页面端口，如 127.0.0.1:8080（默认随机端口）")
	useCookie := flag.Bool("cookie", false, "不走扫码，改用粘贴 Cookie 登录（接口收紧时最稳）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "qzone-history %s — QQ空间说说备份\n\n用法: qzone-history [参数]\n\n参数:\n", version.Version)
		flag.PrintDefaults()
	}
	flag.Parse()

	fmt.Print(banner)
	log.Printf("qzone-history 版本: %s", version.Version)

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	// 命令行参数优先于配置文件
	if isFlagSet("db") {
		cfg.Database.DBName = *dbPath
	}
	if isFlagSet("out") {
		cfg.Output.Dir = *outDir
	}
	if dir := filepath.Dir(cfg.Database.DBName); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if cfg.Output.Dir != "" && cfg.Output.Dir != "." {
		if err := os.MkdirAll(cfg.Output.Dir, 0o755); err != nil {
			log.Fatalf("创建导出目录失败: %v", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("数据库: %s", abs(cfg.Database.DBName))
	var db database.Database
	switch cfg.Database.Type {
	case "sqlite":
		db = sqlite.NewSQLiteDB()
	default:
		log.Fatalf("不支持的数据库类型: %s", cfg.Database.Type)
	}
	if err := db.Connect(&database.Config{DBName: cfg.Database.DBName}); err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer db.Close()
	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	userRepo := persistence.NewUserRepository(db)
	momentRepo := persistence.NewMomentRepository(db)
	activityRepo := persistence.NewActivityRepository(db)
	boardRepo := persistence.NewBoardMessageRepository(db)
	friendRepo := persistence.NewFriendRepository(db)

	api := qzone_api.NewQzoneAPIClient(cfg)
	iAPI, ok := api.(qzone_api.InternalAPI)
	if !ok {
		log.Fatalf("内部错误: API 客户端类型不匹配")
	}

	authUC := usecase.NewAuthUseCase(api, userRepo)
	momentUC := usecase.NewMomentUseCase(momentRepo)
	boardUC := usecase.NewBoardMessageUseCase(boardRepo)
	friendUC := usecase.NewFriendUseCase(friendRepo)
	exportUC := usecase.NewExportUseCase(momentRepo, boardRepo, friendRepo, cfg.Output.Dir)
	activityUC := usecase.NewActivityUseCase(api, activityRepo)
	reconUC := usecase.NewReconstructionUseCase(activityRepo, momentRepo, boardRepo)

	a := app.NewApp(app.Options{
		Auth:            authUC,
		Moment:          momentUC,
		BoardMessage:    boardUC,
		Friend:          friendUC,
		Export:          exportUC,
		Activity:        activityUC,
		Reconstruct:     reconUC,
		Moments:         iAPI,
		MomentRepo:      momentRepo,
		ActivityRepo:    activityRepo,
		UserRepo:        userRepo,
		ExportOnly:      *exportOnly,
		ForceLogin:      *force,
		Refetch:         *refetch,
		WantActivities:  *activities,
		ListenAddr:      *listen,
		WantCookieLogin: *useCookie,
	})

	if err := a.Run(ctx); err != nil {
		log.Fatalf("运行失败: %v", err)
	}
	log.Println("完成")
}

// isFlagSet 判断某个命令行参数是否被显式传入
func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
