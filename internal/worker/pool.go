package worker

import (
	"context"
	"sync"
	"time"

	"key-pool-system/internal/db"
	"key-pool-system/internal/keypool"
	"key-pool-system/internal/queue"

	"github.com/rs/zerolog"
)

// Pool manages a group of worker goroutines.
type Pool struct {
	workers []*Worker
	wg      sync.WaitGroup
	logger  zerolog.Logger
}

// NewPool creates a pool of workers.
func NewPool(
	count int,
	q *queue.Queue,
	keyPool *keypool.Manager,
	dbAdap db.DBAdapter,
	getContracts ContractsFunc,
	encKey string,
	logger zerolog.Logger,
) *Pool {
	workers := make([]*Worker, count)
	for i := 0; i < count; i++ {
		workers[i] = NewWorker(i, q, keyPool, dbAdap, getContracts, encKey, logger)
	}
	return &Pool{
		workers: workers,
		logger:  logger.With().Str("component", "pool").Logger(),
	}
}

// Start launches all workers with staggered startup.
func (p *Pool) Start(ctx context.Context, warmupPeriod time.Duration) {
	delay := warmupPeriod / time.Duration(len(p.workers))

	for i, w := range p.workers {
		p.wg.Add(1)
		go func(w *Worker) {
			defer p.wg.Done()
			w.Start(ctx)
		}(w)

		if i < len(p.workers)-1 && delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}
}

// Wait blocks until all workers finish.
func (p *Pool) Wait() {
	p.wg.Wait()
}
