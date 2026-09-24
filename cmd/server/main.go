// Command server runs the ext.to to Telegram forwarder with its web UI.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/haoyu010/ext.to/internal/config"
	"github.com/haoyu010/ext.to/internal/forwarder"
	"github.com/haoyu010/ext.to/internal/store"
	"github.com/haoyu010/ext.to/internal/web"
)

// version is overridable at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		addr    = flag.String("addr", envOr("ADDR", ":8080"), "HTTP listen address")
		dataDir = flag.String("data", envOr("DATA_DIR", "/data"), "directory for settings and history")
	)
	flag.Parse()

	logger, ring := web.NewLogger(os.Stdout, version)
	log.SetOutput(logger.Writer())

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		logger.Fatalf("cannot create data directory %s: %v", *dataDir, err)
	}

	cfg, err := config.Load(filepath.Join(*dataDir, "config.json"))
	if err != nil {
		logger.Fatalf("load settings: %v", err)
	}
	state, err := store.Open(filepath.Join(*dataDir, "state.json"))
	if err != nil {
		logger.Fatalf("load history: %v", err)
	}

	fwd := forwarder.New(cfg, state, logger)
	if cfg.Get().Enabled {
		fwd.Start()
	} else {
		logger.Printf("监听当前为关闭状态，可在面板「抓取来源」中开启")
	}

	srv := web.New(web.Options{
		Config:  cfg,
		Store:   state,
		Forward: fwd,
		Logger:  logger,
		Version: version,
		LogRing: ring,
	})

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 15 * time.Second,
		// Listing scans can stream large upstream responses, but requests to
		// this server are small.
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  90 * time.Second,
	}

	go func() {
		logger.Printf("服务已启动：地址 %s，版本 %s，数据目录 %s", *addr, version, *dataDir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("http server: %v", err)
		}
	}()

	// Shut down cleanly so in-flight state is flushed.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Printf("正在退出")

	fwd.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		logger.Printf("HTTP 服务关闭失败：%v", err)
	}
	if err := state.Flush(); err != nil {
		logger.Printf("保存记录失败：%v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
