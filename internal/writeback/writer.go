// Package writeback keeps the database off the /key path. A serve only touches
// memory here; a background flush writes usage counts (coalesced per key) and audit
// events (one COPY) every period and once more on shutdown. A failed flush keeps its
// rows for the next one, bounded, so an outage costs history and never latency.
package writeback

import (
	"context"
	"sync"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/rs/zerolog"
)

const (
	// DefaultMaxEvents bounds the events held while the database is unreachable.
	DefaultMaxEvents = 10_000
	// finalFlushTimeout bounds the shutdown flush, so a dead database cannot hold the exit.
	finalFlushTimeout = 10 * time.Second
)

// Store is the slice of the database a flush writes to.
type Store interface {
	AddUsage(ctx context.Context, keyID string, n int) error
	ResetUsageWindow(ctx context.Context, keyID string, start time.Time, count int) error
	RecordUsageEvents(ctx context.Context, events []*db.UsageEvent) error
}

// usage is one key's unwritten serves: n since the last flush, or, when the key's
// window rolled over (reset set), n since that reset.
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

// Served counts one serve of a key with a usage budget.
func (w *Writer) Served(keyID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending(keyID).n++
}

// WindowReset records that a key's budget window rolled over at start, with the
// serve that triggered it as the first of the new window.
func (w *Writer) WindowReset(keyID string, start time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.usage[keyID] = &usage{n: 1, reset: &start}
}

// Event queues an audit row, dropping the oldest once maxEvents are waiting.
func (w *Writer) Event(e *db.UsageEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, e)
	if over := len(w.events) - w.maxEvents; over > 0 {
		w.events = w.events[over:]
	}
}

func (w *Writer) pending(keyID string) *usage {
	u, ok := w.usage[keyID]
	if !ok {
		u = &usage{}
		w.usage[keyID] = u
	}
	return u
}

// Flush writes everything pending. What fails to land is merged back.
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
		w.events = append(events, w.events...)
		if over := len(w.events) - w.maxEvents; over > 0 {
			w.events = w.events[over:]
		}
		w.mu.Unlock()
	}
}

// requeueUsage folds an unwritten entry under whatever arrived since. A reset that
// arrived since supersedes it: the new window's count already stands on its own.
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

// Run flushes every period until ctx ends, then flushes once more.
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
