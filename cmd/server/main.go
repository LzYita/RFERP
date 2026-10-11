// 入口：把 Service 装配到 HTTP API（C1）。多人形态下仅服务端连 MySQL（D-013）。
//
// 装配逻辑已下沉到 internal/bootstrap，与 cmd/desktop 共用同一份实现；
// 本入口只负责监听地址与进程生命周期。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"app/internal/api"
	"app/internal/bootstrap"
	"app/internal/config"
	"app/internal/logging"
	"app/internal/paths"
)

var version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	cfg := config.Load()
	paths.SetDataDir(cfg.DataDir)
	if err := logging.Init(paths.DataDir()); err != nil {
		log.Printf("init log file failed: %v", err)
	}

	res, err := bootstrap.Build(cfg, bootstrap.Options{Logf: log.Printf})
	if err != nil {
		if bootstrap.IsNotConfigured(err) {
			log.Fatalf("server 尚未配置数据库：%v（先以桌面端完成配置，或改用 cmd/desktop 初始化）", err)
		}
		log.Fatalf("bootstrap failed: %v", err)
	}

	srv := api.New(res.Apps, api.WithVersion(version))

	// http.Server 补上超时：裸 ListenAndServe 没有读/写上限，
	// 一个慢连接就能占住 goroutine（D-005 / Round 1）。
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// 会话只在内存里靠惰性删除回收：不再被使用的 token 会永久驻留。
	// 长期运行的 server 必须有清扫协程（D-005）。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.StartSessionJanitor(ctx)

	log.Printf("RFERP API %s listening on %s (storage=%s target=%s)",
		version, *addr, res.Kind, res.Target)

	// 公网部署前必须加 TLS（C4）；当前假设可信内网。
	//
	// 不写成 log.Fatal(httpSrv.ListenAndServe())：log.Fatal 走 os.Exit，
	// defer 不执行，数据库句柄不会关闭、优雅关闭也不会发生。
	go func() {
		<-ctx.Done()
		log.Println("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown: %v", err)
		}
	}()

	err = httpSrv.ListenAndServe()
	if cerr := res.Close(); cerr != nil {
		log.Printf("close database: %v", cerr)
	}
	log.Fatal(err)
}
