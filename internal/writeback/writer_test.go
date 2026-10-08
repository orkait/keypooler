package writeback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/db"
	"github.com/rs/zerolog"
)

type call struct {
	key   string
	n     int
	start time.Time
	reset bool
}

type fakeStore struct {
	mu     sync.Mutex
	fail   int
	calls  []call
	events [][]*db.UsageEvent
}

func (f *fakeStore) failing() error {
	if f.fail > 0 {
		f.fail--
		return errors.New("postgres is down")
	}
	return nil
}

func (f *fakeStore) AddUsage(_ context.Context, key string, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing(); err != nil {
		return err
	}
	f.calls = append(f.calls, call{key: key, n: n})
	return nil
}

func (f *fakeStore) ResetUsageWindow(_ context.Context, key string, start time.Time, count int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing(); err != nil {
		return err
	}
	f.calls = append(f.calls, call{key: key, n: count, start: start, reset: true})
	return nil
}

func (f *fakeStore) RecordUsageEvents(_ context.Context, events []*db.UsageEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.failing(); err != nil {
		return err
	}
	f.events = append(f.events, events)
	return nil
}

func newWriter(s *fakeStore) *Writer { return New(s, zerolog.Nop()) }

var ctx = context.Background()

func TestServesOfOneKeyLandAsOneIncrement(t *testing.T) {
	s := &fakeStore{}
	w := newWriter(s)
	for range 3 {
		w.Served("k")
	}
	w.Flush(ctx)
	w.Flush(ctx)
	if len(s.calls) != 1 || s.calls[0] != (call{key: "k", n: 3}) {
		t.Fatalf("calls %+v", s.calls)
	}
}

func TestAWindowResetCarriesTheServesMadeSince(t *testing.T) {
	s := &fakeStore{}
	w := newWriter(s)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	w.Served("k")
	w.WindowReset("k", start)
	w.Served("k")
	w.Served("k")
	w.Flush(ctx)
	if len(s.calls) != 1 || s.calls[0] != (call{key: "k", n: 3, start: start, reset: true}) {
		t.Fatalf("calls %+v", s.calls)
	}
}

func TestAFailedFlushKeepsItsUsageForTheNext(t *testing.T) {
	s := &fakeStore{fail: 1}
	w := newWriter(s)
	w.Served("k")
	w.Flush(ctx)
	w.Served("k")
	w.Flush(ctx)
	if len(s.calls) != 1 || s.calls[0] != (call{key: "k", n: 2}) {
		t.Fatalf("calls %+v", s.calls)
	}
}

func TestAResetAfterAFailedIncrementSupersedesIt(t *testing.T) {
	s := &fakeStore{fail: 1}
	w := newWriter(s)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	w.Served("k")
	w.Flush(ctx)
	w.WindowReset("k", start)
	w.Flush(ctx)
	if len(s.calls) != 1 || s.calls[0] != (call{key: "k", n: 1, start: start, reset: true}) {
		t.Fatalf("calls %+v", s.calls)
	}
}

func TestEventsLandInOneBatchAndSurviveAFailedFlush(t *testing.T) {
	s := &fakeStore{fail: 1}
	w := newWriter(s)
	w.Event(db.NewUsageEvent("k", "c", "chat"))
	w.Flush(ctx)
	w.Event(db.NewUsageEvent("k", "", "chat"))
	w.Flush(ctx)
	if len(s.events) != 1 || len(s.events[0]) != 2 {
		t.Fatalf("batches %+v", s.events)
	}
}

func TestPendingEventsAreBoundedOldestDroppedFirst(t *testing.T) {
	s := &fakeStore{}
	w := newWriter(s)
	w.maxEvents = 2
	for _, feature := range []string{"a", "b", "c"} {
		w.Event(db.NewUsageEvent("k", "", feature))
	}
	w.Flush(ctx)
	if got := s.events[0]; len(got) != 2 || got[0].Feature != "b" || got[1].Feature != "c" {
		t.Fatalf("kept %+v", got)
	}
}

func TestRunFlushesWhatIsLeftWhenStopped(t *testing.T) {
	s := &fakeStore{}
	w := newWriter(s)
	stop, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { w.Run(stop, time.Hour); close(done) }()
	w.Served("k")
	cancel()
	<-done
	if len(s.calls) != 1 {
		t.Fatalf("calls %+v", s.calls)
	}
}
