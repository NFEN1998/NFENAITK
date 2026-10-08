// Command nfentik-go runs the Go rewrite of the nfentik question bank service.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nfentik/nfentik-go/internal/cache"
	"github.com/nfentik/nfentik-go/internal/config"
	"github.com/nfentik/nfentik-go/internal/importer"
	"github.com/nfentik/nfentik-go/internal/server"
	"github.com/nfentik/nfentik-go/internal/store"
)

//go:embed web/templates web/static
var webFS embed.FS

func main() {
	// Subcommands: everything after the program name starting with a known
	// keyword is dispatched here; otherwise the service starts normally.
	if len(os.Args) > 1 && os.Args[1] == "migrate-sqlite" {
		migrateCmd := flag.NewFlagSet("migrate-sqlite", flag.ExitOnError)
		srcPath := migrateCmd.String("src", "", "path to the legacy SQLite database.db")
		_ = migrateCmd.Parse(os.Args[2:])
		if err := runMigrate(*srcPath); err != nil {
			log.Fatalf("nfentik-go migrate-sqlite: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "check-db" {
		if err := runCheckDB(); err != nil {
			log.Fatalf("nfentik-go check-db: %v", err)
		}
		return
	}

	var (
		port      = flag.Int("port", 0, "HTTP port (overrides the stored setting)")
		bind      = flag.String("bind", "", "bind address (overrides the stored setting)")
		lanAccess = flag.Bool("lan", false, "allow access from the local network")
	)
	flag.Parse()

	if err := run(*port, *bind, *lanAccess); err != nil {
		log.Fatalf("nfentik-go: %v", err)
	}
}

func run(port int, bind string, lanAccess bool) error {
	bootstrap, err := config.LoadBootstrap()
	if err != nil {
		return fmt.Errorf("读取引导配置失败: %w", err)
	}
	log.Printf("引导配置: %s", config.BootstrapPath())

	assets, err := fs.Sub(webFS, "web")
	if err != nil {
		return fmt.Errorf("embed assets: %w", err)
	}

	dsn := bootstrap.Database.URL
	if dsn == "" {
		// No database configured yet: run the first-run installation wizard.
		return serveSetup(assets, bootstrap, nil, nil, "尚未配置数据库连接", port, bind, lanAccess)
	}
	st, err := store.Open(dsn)
	if err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer st.Close()
	log.Printf("已连接 PostgreSQL: %s", redactDSN(dsn))

	// First run after an upgrade: pull settings out of any legacy files. The
	// bootstrap file is (re)written so the connection strings survive the next
	// start even though the legacy files were removed.
	if imported, err := config.ImportLegacyFiles(st, config.DataDir(), config.ConfigDir()); err != nil {
		log.Printf("警告: 导入旧配置失败: %v", err)
	} else if len(imported) > 0 {
		if err := config.WriteBootstrap(bootstrap); err != nil {
			log.Printf("警告: 写入引导配置失败: %v", err)
		} else {
			log.Printf("已写入引导配置: %s", config.BootstrapPath())
		}
	}

	cfg, err := config.New(st, bootstrap)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	log.Printf("配置已从数据库加载")

	// A database without an admin token is still unconfigured: route the
	// operator through the wizard so the instance is never left open or
	// unadministrable.
	if strings.TrimSpace(cfg.Settings().AdminToken) == "" {
		return serveSetup(assets, bootstrap, st, cfg, "尚未设置管理员令牌", port, bind, lanAccess)
	}

	// Redis is optional: when unavailable the service keeps working with the
	// database alone.
	var cacheClient *cache.Client
	redisCfg := cfg.RedisConfig()
	if redisCfg.Enabled && redisCfg.URL != "" {
		cacheClient, err = cache.New(cache.Options{
			URL:    redisCfg.URL,
			Prefix: redisCfg.Prefix,
			LogSink: func(entries []json.RawMessage) error {
				return flushLogs(st, entries)
			},
			CountSink: st.AddDailyRequestCount,
		})
		if err != nil {
			log.Printf("警告: Redis 不可用，已退化为仅使用数据库: %v", err)
			cacheClient = nil
		} else {
			defer cacheClient.Close()
			log.Printf("已连接 Redis，查询缓存与日志队列已启用")
		}
	} else {
		log.Printf("未启用 Redis，直接读写 PostgreSQL")
	}

	srv, err := server.New(server.Deps{
		Store:       st,
		Config:      cfg,
		Cache:       cacheClient,
		StorageInfo: redactDSN(dsn),
		WebFS:       assets,
	})
	if err != nil {
		return err
	}
	defer srv.CloseSegmenter()

	settings := cfg.Settings()
	addr := settings.Network.BindAddress
	if bind != "" {
		addr = bind
	}
	if !settings.Network.EnableLan && !lanAccess {
		addr = "127.0.0.1"
	}
	if addr == "" {
		addr = "0.0.0.0"
	}

	listenPort := settings.Network.ServerPort
	if port > 0 {
		listenPort = port
	}
	if listenPort == 0 {
		listenPort = 3000
	}

	httpServer := &http.Server{
		Addr:              net.JoinHostPort(addr, fmt.Sprint(listenPort)),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", httpServer.Addr, err)
	}

	log.Printf("nfentik-go 服务已启动: http://%s", httpServer.Addr)
	log.Printf("OCS 查询地址: http://%s/query", httpServer.Addr)
	log.Printf("管理控制台:   http://%s/console", httpServer.Addr)

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-stop:
		log.Println("正在关闭服务...")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(ctx)
}

// serveSetup runs the first-run installation wizard in place of the normal
// service. It keeps the same bind/port logic so the operator reaches it at the
// usual address, then restarts the program after saving.
func serveSetup(assets fs.FS, bootstrap config.BootstrapFile, st *store.Store, cfg *config.Store, reason string, port int, bind string, lanAccess bool) error {
	setup, err := server.NewSetupServer(assets, bootstrap, st, cfg, reason)
	if err != nil {
		return err
	}

	addr := "127.0.0.1"
	if bind != "" {
		addr = bind
	}
	if lanAccess {
		addr = "0.0.0.0"
	}
	if addr == "" {
		addr = "0.0.0.0"
	}

	listenPort := 3000
	if port > 0 {
		listenPort = port
	}

	httpServer := &http.Server{
		Addr:              net.JoinHostPort(addr, fmt.Sprint(listenPort)),
		Handler:           setup.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", httpServer.Addr, err)
	}

	log.Printf("检测到首次运行（%s），已进入安装配置向导", reason)
	log.Printf("请在浏览器中打开: http://%s/setup", httpServer.Addr)
	log.Printf("保存配置后，请手动重启程序使配置生效")

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-stop:
		log.Println("正在关闭服务...")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(ctx)
}

// flushLogs turns queued JSON log entries into store rows. It is injected into
// the cache client so the cache package stays free of storage concerns.
func flushLogs(st *store.Store, entries []json.RawMessage) error {
	logs := make([]store.RequestLog, 0, len(entries))
	for _, raw := range entries {
		var entry store.RequestLog
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		logs = append(logs, entry)
	}
	return st.InsertRequestLogs(logs, 1000)
}

// redactDSN removes the password from a PostgreSQL connection string so it is
// safe to print in the startup log.
func redactDSN(dsn string) string {
	// Only redact URL style DSNs; key=value DSNs are shown as-is.
	if !strings.Contains(dsn, "://") {
		return dsn
	}
	schemeEnd := strings.Index(dsn, "://")
	rest := dsn[schemeEnd+3:]
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return dsn
	}
	userinfo := rest[:at]
	hostPart := rest[at:]
	if colon := strings.Index(userinfo, ":"); colon >= 0 {
		userinfo = userinfo[:colon] + ":***"
	}
	return dsn[:schemeEnd+3] + userinfo + hostPart
}

// runCheckDB performs a read-only database integrity check and prints a human
// readable report without changing anything. It exits non-zero when the schema
// is incomplete, so it can gate a deployment.
func runCheckDB() error {
	bootstrap, err := config.LoadBootstrap()
	if err != nil {
		return fmt.Errorf("读取引导配置失败: %w", err)
	}
	dsn := bootstrap.Database.URL
	if dsn == "" {
		return errors.New("未配置 PostgreSQL 连接，请设置环境变量 DATABASE_URL 或在 config/config.json 中填写")
	}
	report, err := store.InspectDSN(dsn)
	if err != nil {
		return err
	}

	fmt.Printf("数据库完整性检测报告\n")
	fmt.Printf("  数据库:       %s\n", redactDSN(dsn))
	fmt.Printf("  版本表:       %s\n", presence(report.HasVersionTable))
	fmt.Printf("  当前版本:     %d\n", report.CurrentVersion)
	fmt.Printf("  目标版本:     %d\n", report.TargetVersion)
	if report.Empty {
		fmt.Printf("  状态:         空库（首次启动将初始化全部表结构）\n")
		return nil
	}
	fmt.Printf("  缺失表:       %s\n", listOrNone(report.MissingTables))
	if len(report.MissingColumns) == 0 {
		fmt.Printf("  缺失字段:     无\n")
	} else {
		cols := make([]string, len(report.MissingColumns))
		for i, c := range report.MissingColumns {
			cols[i] = c.Table + "." + c.Column
		}
		fmt.Printf("  缺失字段:     %s\n", strings.Join(cols, ", "))
	}
	fmt.Printf("  待应用迁移:   %s\n", intsOrNone(report.PendingMigrations))
	if report.Healthy() {
		fmt.Printf("  状态:         通过（结构与迁移均为最新）\n")
		return nil
	}
	fmt.Printf("  状态:         需修复（启动服务时将自动补齐，或运行迁移）\n")
	return fmt.Errorf("数据库结构不完整")
}

func presence(v bool) string {
	if v {
		return "存在"
	}
	return "不存在"
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "无"
	}
	return strings.Join(items, ", ")
}

func intsOrNone(items []int) string {
	if len(items) == 0 {
		return "无"
	}
	parts := make([]string, len(items))
	for i, v := range items {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ", ")
}

// runMigrate imports a legacy SQLite database into PostgreSQL.
func runMigrate(srcPath string) error {
	if srcPath == "" {
		return errors.New("请通过 -src 指定旧 SQLite 数据库路径")
	}
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("找不到源数据库 %s: %w", srcPath, err)
	}
	bootstrap, err := config.LoadBootstrap()
	if err != nil {
		return fmt.Errorf("读取引导配置失败: %w", err)
	}
	dsn := bootstrap.Database.URL
	if dsn == "" {
		return errors.New("未配置 PostgreSQL 连接，请设置环境变量 DATABASE_URL 或在 config/config.json 中填写")
	}
	dst, err := store.Open(dsn)
	if err != nil {
		return fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}
	defer dst.Close()

	log.Printf("开始从 %s 迁移数据到 PostgreSQL...", srcPath)
	res, err := importer.FromSQLite(srcPath, dst)
	if err != nil {
		return err
	}
	log.Printf("迁移完成: 文件夹 %d 个, 题目 %d 条, 跳过空答案 %d 条", res.Folders, res.Questions, res.Skipped)
	return nil
}
