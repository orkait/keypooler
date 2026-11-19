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
	"github.com/orkait/keypooler/internal/balance"
	"github.com/orkait/keypooler/internal/config"
	"github.com/orkait/keypooler/internal/crypto"
	"github.com/orkait/keypooler/internal/db"
	"github.com/orkait/keypooler/internal/keypool"
	"github.com/orkait/keypooler/internal/writeback"

	"github.com/rs/zerolog"
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

	sealer, err := crypto.NewSealer(cfg.EncryptionKey, cfg.PreviousEncryptionKey)
	if err != nil {
		logger.Fatal().Err(err).Msg("invalid encryption configuration")
	}
	sealedNow, err := keypool.SealAtRest(context.Background(), store, sealer)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to seal keys and secrets under the current key")
	}
	logger.Info().Int("sealed", sealedNow).Bool("rotating", cfg.PreviousEncryptionKey != "").Msg("keys and secrets sealed under the current key")
	usage, stopWriteback := startWriteback(store, logger)

	pool, err := keypool.NewManager(store, sealer, usage, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize key pool")
	}
	logger.Info().Int("pool_size", pool.PoolSize()).Bool("encryption", sealer.Enabled()).Msg("key pool initialized")
	stopBalances := startBalances(pool, logger)
	stopRetention := startRetention(store, logger)

	handler := api.NewRouter(&api.Server{
		DB:         store,
		Pool:       pool,
		Usage:      usage,
		Auth:       api.NewAuthCache(config.AuthCacheTTL),
		AdminToken: cfg.AdminToken,
		Sealer:     sealer,
		Logger:     logger,
	})
	serveUntilSignalled(handler, logger)
	stopRetention()
	stopBalances()
	stopWriteback()
	logger.Info().Msg("keypooler stopped")
}

func openStore(cfg *config.Config, logger zerolog.Logger) *db.PostgresAdapter {
	store, err := db.NewPostgresAdapter(cfg.DatabaseURL, config.MaxDBConns)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialize database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.MigrationTimeout)
	defer cancel()
	if err := db.RunMigrations(ctx, store.Pool(), config.MigrationsDir); err != nil {
		logger.Fatal().Err(err).Msg("failed to run migrations")
	}
	logger.Info().Msg("migrations completed")
	return store
}

func inBackground(run func(context.Context)) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		run(ctx)
		close(done)
	}()
	return func() {
		cancel()
		<-done
	}
}

func startWriteback(store writeback.Store, logger zerolog.Logger) (*writeback.Writer, func()) {
	usage := writeback.New(store, logger)
	return usage, inBackground(func(ctx context.Context) { usage.Run(ctx, config.WritebackPeriod) })
}

func startBalances(pool *keypool.Manager, logger zerolog.Logger) func() {
	sources := balance.Sources(&http.Client{Timeout: config.BalanceTimeout})
	return inBackground(balance.NewSyncer(pool, sources, config.BalancePeriod, logger).Run)
}

func startRetention(store *db.PostgresAdapter, logger zerolog.Logger) func() {
	return inBackground(func(ctx context.Context) {
		ticker := time.NewTicker(config.PrunePeriod)
		defer ticker.Stop()
		for {
			removed, err := store.PruneEvents(ctx, time.Now().Add(-config.EventRetention))
			if err != nil {
				logger.Error().Err(err).Msg("events past retention not pruned")
			} else {
				logger.Info().Int64("removed", removed).Msg("events past retention pruned")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
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
