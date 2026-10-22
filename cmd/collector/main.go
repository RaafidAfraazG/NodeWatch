package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/RaafidAfraazG/NodeWatch/internal/alerts"
	"github.com/RaafidAfraazG/NodeWatch/internal/collector"
	"github.com/RaafidAfraazG/NodeWatch/internal/config"
	"github.com/RaafidAfraazG/NodeWatch/internal/recovery"
	mysqlstore "github.com/RaafidAfraazG/NodeWatch/internal/storage/mysql"
	redisstore "github.com/RaafidAfraazG/NodeWatch/internal/storage/redis"
)

func main() { os.Exit(run()) }
func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if len(os.Args) > 2 || (mode != "serve" && mode != "migrate" && mode != "healthcheck") {
		logger.Error("usage: collector [serve|migrate|healthcheck]")
		return 1
	}
	if mode == "healthcheck" {
		return healthcheck()
	}
	c, err := config.LoadCollector(os.Getenv)
	if err != nil {
		logger.Error("invalid collector configuration", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := mysqlstore.Open(c)
	if err != nil {
		logger.Error("MySQL client initialization failed")
		return 1
	}
	defer db.Close()
	if mode == "migrate" || c.AutoMigrate {
		migrationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = db.Migrate(migrationCtx)
		cancel()
		if err != nil {
			logger.Error("MySQL migration failed; check database access and schema version")
			return 1
		}
		logger.Info("MySQL schema at version 1")
		if mode == "migrate" {
			return 0
		}
	}
	cache := redisstore.Open(c)
	defer cache.Close()
	if err = db.Ping(ctx); err != nil {
		logger.Error("MySQL not ready; check connectivity and run migrations")
		return 1
	}
	if err = cache.Ping(ctx); err != nil {
		logger.Error("Redis not ready; check connectivity")
		return 1
	}
	engine, err := alerts.New(alerts.Config{FilesystemWarning: c.FilesystemWarning, FilesystemCritical: c.FilesystemCritical, IOWaitWarning: c.IOWaitWarning, IOWaitCritical: c.IOWaitCritical, For: c.AlertFor, MaxHistory: 10000})
	if err != nil {
		logger.Error("invalid alert configuration")
		return 1
	}
	source := collector.NewHostSource(db, cache, c.FreshnessThreshold, time.Now)
	alertWorker, err := alerts.NewWorker(engine, source, c.AlertEvaluationInterval, time.Now)
	if err != nil {
		logger.Error("invalid alert worker configuration")
		return 1
	}
	reconciler, err := recovery.New(db, cache, recovery.Config{Interval: c.ReconciliationInterval, BatchSize: c.ReconciliationBatchSize, MaxBatches: c.ReconciliationMaxBatches, RetryAttempts: c.CacheRetryAttempts, InitialBackoff: 50 * time.Millisecond, MaxBackoff: time.Second})
	if err != nil {
		logger.Error("invalid reconciliation configuration")
		return 1
	}
	workers, stopWorkers := context.WithCancel(ctx)
	var workerWG sync.WaitGroup
	workerWG.Add(2)
	go func() {
		defer workerWG.Done()
		alertWorker.Run(workers, func(error) { logger.Warn("alert evaluation failed") })
	}()
	go func() {
		defer workerWG.Done()
		reconciler.Run(workers, func(error) { logger.Warn("cache reconciliation failed") })
	}()
	defer func() {
		stopWorkers()
		workerWG.Wait()
	}()
	server := &http.Server{Addr: c.Bind, Handler: collector.New(c, db, cache, logger, engine), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.RequestTimeout, WriteTimeout: c.RequestTimeout + 2*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	logger.Info("collector starting", "bind", c.Bind)
	select {
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed")
			return 1
		}
	case <-ctx.Done():
		stopWorkers()
		shutdown, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
		defer cancel()
		if err = server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			logger.Warn("shutdown deadline reached; closed active requests")
		}
		<-result
	}
	logger.Info("collector stopped")
	return 0
}
func healthcheck() int {
	addr := os.Getenv("NODEWATCH_BIND")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/readyz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 1
	}
	return 0
}
