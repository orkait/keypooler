package main

import (
	"context"
	"errors"
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
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
)

const (
	writebackPeriod  = time.Second
	authCacheTTL     = time.Minute
	migrationTimeout = 5 * time.Second
	migrationsDir    = "./migrations"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}
	logger := zerolog.New(os.Stdout).Level(zerolog.InfoLevel).With().Timestamp().Logger()
	logger.Info().Msg("starting keypooler")

	store := openStore(cfg, logger)
	defer store.Close()

	sealer, err := crypto.NewSealer(cfg.EncryptionKey)
	if err != nil {
		logger.Fatal().Err(err).Msg("invalid encryption configuration")
	}
	usage, stopWriteback := startWriteback(store, logger)

	pool, err := keypool.NewManager(store, sealer, usage, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize key pool")
	}
	logger.Info().Int("pool_size", pool.PoolSize()).Bool("encryption", sealer.Enabled()).Msg("key pool initialized")

	handler := api.NewRouter(&api.Server{
		DB:         store,
		Pool:       pool,
		Usage:      usage,
		Auth:       api.NewAuthCache(authCacheTTL),
		AdminToken: cfg.AdminToken,
		Sealer:     sealer,
		Logger:     logger,
	})
	serveUntilSignalled(handler, logger)
	stopWriteback()
	logger.Info().Msg("keypooler stopped")
}

func openStore(cfg *config.Config, logger zerolog.Logger) *db.PostgresAdapter {
	store, err := db.NewPostgresAdapter(cfg.DatabaseURL, config.MaxDBConns)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()
	if err := db.RunMigrations(ctx, store.Pool(), migrationsDir); err != nil {
		logger.Fatal().Err(err).Msg("failed to run migrations")
	}
	logger.Info().Msg("migrations completed")
	return store
}

func startWriteback(store writeback.Store, logger zerolog.Logger) (*writeback.Writer, func()) {
	usage := writeback.New(store, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		usage.Run(ctx, writebackPeriod)
		close(done)
	}()
	return usage, func() {
		cancel()
		<-done
	}
}

func serveUntilSignalled(handler http.Handler, logger zerolog.Logger) {
	server := &http.Server{
		Addr:         config.Addr,
		Handler:      handler,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}
	signalled, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info().Str("addr", config.Addr).Msg("HTTP server listening")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal().Err(err).Msg("HTTP server error")
		}
	}()
	<-signalled.Done()
	logger.Info().Msg("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error().Err(err).Msg("HTTP server shutdown error")
	}
}
