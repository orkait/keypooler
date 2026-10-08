package writeback

import (
	"context"
	"sync"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/rs/zerolog"
)

const (
	DefaultMaxEvents  = 10_000
	finalFlushTimeout = 10 * time.Second
)

type Store interface {
	AddUsage(ctx context.Context, keyID string, n int) error
	ResetUsageWindow(ctx context.Context, keyID string, start time.Time, count int) error
	RecordUsageEvents(ctx context.Context, events []*db.UsageEvent) error
}

type usage struct {
	n     int
	reset *time.Time
}

type Writer struct {
	mu        sync.Mutex
	usage     map[string]*usage
	events    []*db.UsageEvent
	maxEvents int
	store     Store
	logger    zerolog.Logger
}

func New(store Store, logger zerolog.Logger) *Writer {
	return &Writer{
		usage:     map[string]*usage{},
		maxEvents: DefaultMaxEvents,
		store:     store,
		logger:    logger.With().Str("component", "writeback").Logger(),
	}
}

func (w *Writer) Served(keyID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	u, ok := w.usage[keyID]
	if !ok {
		u = &usage{}
		w.usage[keyID] = u
	}
	u.n++
}

func (w *Writer) WindowReset(keyID string, start time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.usage[keyID] = &usage{n: 1, reset: &start}
}

func (w *Writer) Event(e *db.UsageEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.keepEvents(append(w.events, e))
}

func (w *Writer) keepEvents(events []*db.UsageEvent) {
	w.events = events[max(len(events)-w.maxEvents, 0):]
}

func (w *Writer) Flush(ctx context.Context) {
	w.mu.Lock()
	served, events := w.usage, w.events
	w.usage, w.events = map[string]*usage{}, nil
	w.mu.Unlock()

	for keyID, u := range served {
		var err error
		if u.reset != nil {
			err = w.store.ResetUsageWindow(ctx, keyID, *u.reset, u.n)
		} else {
			err = w.store.AddUsage(ctx, keyID, u.n)
		}
		if err != nil {
			w.logger.Error().Err(err).Str("key_id", keyID).Msg("usage flush failed, kept for the next")
			w.requeueUsage(keyID, u)
		}
	}

	if len(events) == 0 {
		return
	}
	if err := w.store.RecordUsageEvents(ctx, events); err != nil {
		w.logger.Error().Err(err).Int("events", len(events)).Msg("event flush failed, kept for the next")
		w.mu.Lock()
		w.keepEvents(append(events, w.events...))
		w.mu.Unlock()
	}
}

func (w *Writer) requeueUsage(keyID string, failed *usage) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now, ok := w.usage[keyID]
	if !ok {
		w.usage[keyID] = failed
		return
	}
	if now.reset != nil {
		return
	}
	now.n += failed.n
	now.reset = failed.reset
}

func (w *Writer) Run(ctx context.Context, period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.Flush(ctx)
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), finalFlushTimeout)
			w.Flush(final)
			cancel()
			return
		}
	}
}
