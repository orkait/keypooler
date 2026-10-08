package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/orkait/keypooler/internal/api"
	"github.com/orkait/keypooler/internal/config"
	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/keypool"
	"github.com/orkait/keypooler/internal/util"
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
)

const (
	// How often usage counts and audit events land; a crash loses at most this much.
	writebackPeriod = time.Second
	// How long a resolved consumer token is trusted without a lookup. Admin writes
	// clear it sooner.
	authCacheTTL = time.Minute
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg)
	logger.Info().Msg("starting keypooler")

	dbAdapter, err := db.NewPostgresAdapter(cfg.DatabaseURL, cfg.DBMaxOpenConns)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize database")
	}
	defer dbAdapter.Close()

	ctx, cancel := util.DBContext(context.Background(), util.DBTimeoutLong)
	if err := db.RunMigrations(ctx, dbAdapter.Pool(), "./migrations"); err != nil {
		cancel()
		logger.Fatal().Err(err).Msg("failed to run migrations")
	}
	cancel()
	logger.Info().Msg("migrations completed")

	// Encryption sealer: empty key => plaintext storage (default); a configured
	// 32-byte hex key encrypts new key/secret writes (self-tagged enc:gcm:).
	sealer, err := crypto.NewSealer(cfg.EncryptionKey)
	if err != nil {
		logger.Fatal().Err(err).Msg("invalid encryption configuration")
	}

	// Usage counts and audit events are written behind the response.
	usage := writeback.New(dbAdapter, logger)
	flushCtx, stopFlushing := context.WithCancel(context.Background())
	flushed := make(chan struct{})
	go func() {
		usage.Run(flushCtx, writebackPeriod)
		close(flushed)
	}()

	// Key pool
	poolMgr, err := keypool.NewManager(dbAdapter, sealer, usage, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize key pool")
	}
	logger.Info().Int("pool_size", poolMgr.PoolSize()).Bool("encryption", sealer.Enabled()).Msg("key pool initialized")

	srv := &api.Server{
		DB:     dbAdapter,
		Pool:   poolMgr,
		Usage:  usage,
		Auth:   api.NewAuthCache(authCacheTTL),
		Cfg:    cfg,
		Sealer: sealer,
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
	// No request is in flight now; the last flush writes what they left.
	stopFlushing()
	<-flushed

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
