package balance

import (
	"context"
	"time"

	"github.com/orkait/keypooler/internal/keypool"
	"github.com/rs/zerolog"
)

type Pool interface {
	Holding(feature string) []keypool.Held
	SetBalance(id string, b keypool.Balance)
	MarkExhausted(id string, until time.Time) bool
}

type Syncer struct {
	pool    Pool
	sources map[string]Source
	period  time.Duration
	logger  zerolog.Logger
	now     func() time.Time
}

func NewSyncer(pool Pool, sources map[string]Source, period time.Duration, logger zerolog.Logger) *Syncer {
	return &Syncer{pool: pool, sources: sources, period: period, logger: logger.With().Str("component", "balance").Logger(), now: time.Now}
}

func (s *Syncer) Run(ctx context.Context) {
	ticker := time.NewTicker(s.period)
	defer ticker.Stop()
	for {
		s.sync(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Syncer) sync(ctx context.Context) {
	for feature, source := range s.sources {
		for _, key := range s.pool.Holding(feature) {
			if ctx.Err() != nil {
				return
			}
			s.check(ctx, source, key)
		}
	}
}

func (s *Syncer) check(ctx context.Context, source Source, key keypool.Held) {
	b, err := source.Check(ctx, key.KeyValue)
	if err != nil {
		s.logger.Warn().Err(err).Str("key_id", key.ID).Msg("balance not checked")
		return
	}
	now := s.now()
	b.CheckedAt = now
	s.pool.SetBalance(key.ID, b)
	if !b.Spent() {
		return
	}
	until := now.Add(s.period)
	if b.ResetsAt != nil {
		until = *b.ResetsAt
	}
	s.pool.MarkExhausted(key.ID, until)
	s.logger.Info().Str("key_id", key.ID).Float64("used", b.Used).Float64("limit", b.Limit).Time("until", until).Msg("spent; benched")
}
