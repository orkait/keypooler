package balance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/keypool"
	"github.com/rs/zerolog"
)

type fakePool struct {
	held      map[string][]keypool.Held
	balances  map[string]keypool.Balance
	exhausted map[string]time.Time
}

func (p *fakePool) Holding(feature string) []keypool.Held { return p.held[feature] }

func (p *fakePool) SetBalance(id string, b keypool.Balance) { p.balances[id] = b }

func (p *fakePool) MarkExhausted(id string, until time.Time) bool {
	p.exhausted[id] = until
	return true
}

type fixedSource struct {
	balance keypool.Balance
	err     error
}

func (s fixedSource) Check(context.Context, string) (keypool.Balance, error) { return s.balance, s.err }

func TestASyncRecordsEachBalanceAndBenchesASpentKeyUntilItResets(t *testing.T) {
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	reset := now.Add(72 * time.Hour)
	cases := map[string]struct {
		source   fixedSource
		recorded bool
		benched  *time.Time
	}{
		"headroom":                {fixedSource{balance: keypool.Balance{Used: 4, Limit: 1000, ResetsAt: &reset}}, true, nil},
		"spent with a reset date": {fixedSource{balance: keypool.Balance{Used: 1000, Limit: 1000, ResetsAt: &reset}}, true, &reset},
		"spent, reset unknown":    {fixedSource{balance: keypool.Balance{Used: 1200, Limit: 1000}}, true, ptr(now.Add(period))},
		"no limit":                {fixedSource{balance: keypool.Balance{Used: 50}}, true, nil},
		"provider failed":         {fixedSource{err: errors.New("down")}, false, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			pool := &fakePool{
				held:      map[string][]keypool.Held{"probe": {{ID: "k1", KeyValue: "v1"}}},
				balances:  map[string]keypool.Balance{},
				exhausted: map[string]time.Time{},
			}
			s := &Syncer{pool: pool, sources: map[string]Source{"probe": c.source}, period: period, logger: zerolog.Nop(), now: func() time.Time { return now }}
			s.sync(context.Background())
			got, recorded := pool.balances["k1"]
			if recorded != c.recorded || (recorded && !got.CheckedAt.Equal(now)) {
				t.Fatalf("balance %+v recorded=%v", got, recorded)
			}
			until, benched := pool.exhausted["k1"]
			if benched != (c.benched != nil) || (benched && !until.Equal(*c.benched)) {
				t.Fatalf("benched until %v (%v), want %v", until, benched, c.benched)
			}
		})
	}
}

const period = 15 * time.Minute

func ptr(t time.Time) *time.Time { return &t }
