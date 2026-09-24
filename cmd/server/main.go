// 入口：把 Service 装配到 HTTP API（C1）。多人形态下仅服务端连 MySQL（D-013）。
package main

import (
	"flag"
	"log"
	"net/http"
	"strconv"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"app/internal/api"
	"app/internal/config"
	"app/internal/logging"
	"app/internal/migrate"
	"app/internal/paths"
	"app/internal/repository"
	"app/internal/service"
)

// version 由构建脚本用 -ldflags "-X main.version=..." 注入。
var version = "dev"

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	cfg := config.Load()
	paths.SetDataDir(cfg.DataDir)
	if err := logging.Init(paths.DataDir()); err != nil {
		log.Printf("init log file failed: %v", err)
	}

	db, err := sqlx.Connect("mysql", cfg.DB.DSN)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	if _, err := migrate.Run(db, migrate.Options{
		DSN:           cfg.DB.DSN,
		MysqldumpPath: cfg.MysqldumpPath,
		BackupDir:     paths.BackupDir(),
	}); err != nil {
		log.Fatalf("migration failed: %v", err)
	}

	apps := service.New(repository.New(db), cfg.DB.DSN, cfg.MysqldumpPath, cfg)
	srv := api.New(apps, api.WithVersion(version))

	log.Printf("RFERP API %s listening on %s (db %s@%s:%s/%s)",
		version, *addr, cfg.DB.User, cfg.DB.Host, strconv.Itoa(cfg.DB.Port), cfg.DB.DBName)
	// 公网部署前必须加 TLS（C4）；当前假设可信内网。
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
