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
	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/util"

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

	// Key pool
	poolMgr, err := keypool.NewManager(dbAdapter, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize key pool")
	}
	logger.Info().Int("pool_size", poolMgr.PoolSize()).Msg("key pool initialized")

	srv := &api.Server{
		DB:     dbAdapter,
		Pool:   poolMgr,
		Cfg:    cfg,
		Logger: logger,
	}

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

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	logger.Info().Str("signal", sig.String()).Msg("shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ServerShutdownTimeout)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}

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
