package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"key-pool-system/internal/api"
	"key-pool-system/internal/config"
	"key-pool-system/internal/contract"
	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/queue"
	"key-pool-system/internal/runner"
	"key-pool-system/internal/scheduler"
	"key-pool-system/internal/util"
	"key-pool-system/internal/worker"

	"github.com/rs/zerolog"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg)
	logger.Info().Msg("starting keypooler")

	// Database
	dbAdapter, err := db.NewSQLiteAdapter(cfg.DBPath, cfg.DBMaxOpenConns, cfg.DBBusyTimeoutMS)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize database")
	}
	defer dbAdapter.Close()

	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutLong)
	if err := db.RunMigrations(ctx, dbAdapter.DB(), "./migrations"); err != nil {
		cancel()
		logger.Fatal().Err(err).Msg("failed to run migrations")
	}
	cancel()
	logger.Info().Msg("migrations completed")

	// Scan scripts
	contracts, err := api.ScanScriptsDir(cfg.ScriptsPath, logger)
	if err != nil {
		logger.Warn().Err(err).Msg("failed to scan scripts directory")
		contracts = make(map[string]*contract.Contract)
	}
	logger.Info().Int("scripts", len(contracts)).Msg("scripts loaded")

	// Key pool
	poolMgr, err := keypool.NewManager(dbAdapter, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize key pool")
	}
	logger.Info().Int("pool_size", poolMgr.PoolSize()).Msg("key pool initialized")

	// Queue
	q := queue.NewQueue(cfg.QueueMaxSize)

	// Scheduler
	sched := scheduler.New(q, dbAdapter, logger)
	sched.LoadFromContracts(contracts)

	// Root context
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// HTTP server (created before workers so workers can use srv.GetContracts)
	srv := &api.Server{
		DB:        dbAdapter,
		Queue:     q,
		Pool:      poolMgr,
		Cfg:       cfg,
		Scheduler: sched,
		Logger:    logger,
	}
	srv.SetContracts(contracts)

	// Runner
	r, runnerWarnings := runner.New(cfg.RunnerMode, cfg.RunnerImage)
	for _, w := range runnerWarnings {
		logger.Warn().Msg(w)
	}
	logger.Info().Str("mode", r.Mode).Str("image", r.Image).Msg("runner configured")

	if r.Mode == "local" {
		runtimes := runner.CheckLocalRuntimes()
		missing := false
		for _, rt := range runtimes {
			if rt.Available {
				logger.Info().Str("runtime", rt.Name).Str("version", rt.Version).Msg("runtime found")
			} else {
				missing = true
				logger.Warn().Str("runtime", rt.Name).Str("install", rt.Install).Msg("runtime not found — scripts using this runtime will fail")
			}
		}
		if missing {
			logger.Warn().Msg("install missing runtimes above, or set RUNNER_MODE=docker and build the runtime image: docker build -f Dockerfile.runtime -t keypooler-runtime .")
		}
	}

	// Worker pool
	workerPool := worker.NewPool(
		cfg.WorkerCount, q, poolMgr, dbAdapter, srv.GetContracts, r, cfg.EncryptionKey, logger,
	)
	workerPool.Start(rootCtx, cfg.WorkerWarmupPeriod)
	logger.Info().Int("workers", cfg.WorkerCount).Msg("worker pool started")

	// Start scheduler
	go sched.Start(rootCtx)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.ServerPort),
		Handler:      api.NewRouter(srv),
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
		IdleTimeout:  cfg.ServerIdleTimeout,
	}

	go func() {
		logger.Info().Int("port", cfg.ServerPort).Msg("HTTP server listening")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	logger.Info().Str("signal", sig.String()).Msg("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}

	rootCancel()
	workerPool.Wait()
	q.Close()
	logger.Info().Msg("keypooler stopped")
}

func setupLogger(cfg *config.Config) zerolog.Logger {
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}

	if cfg.LogFormat == "pretty" {
		return zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).
			Level(level).
			With().Timestamp().Logger()
	}

	return zerolog.New(os.Stdout).
		Level(level).
		With().Timestamp().Logger()
}
