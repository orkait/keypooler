package keypool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/db"
)

func day(d int) *int { return &d }

func at(month time.Month, d, hour int) time.Time {
	return time.Date(2026, month, d, hour, 0, 0, 0, time.UTC)
}

func TestABudgetPeriodRunsFromItsResetDayToTheSameDayNextMonth(t *testing.T) {
	cases := map[string]struct {
		resetDay *int
		now      time.Time
		start    time.Time
	}{
		"after the reset day":     {day(1), at(time.October, 9, 12), at(time.October, 1, 0)},
		"before the reset day":    {day(15), at(time.October, 9, 12), at(time.September, 15, 0)},
		"on the reset day":        {day(9), at(time.October, 9, 0), at(time.October, 9, 0)},
		"across a short month":    {day(28), at(time.March, 1, 0), at(time.February, 28, 0)},
		"across the year":         {day(1), time.Date(2027, time.January, 1, 3, 0, 0, 0, time.UTC), time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)},
		"lifetime has one period": {nil, at(time.October, 9, 12), lifetime},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := db.Budget{Amount: 200, Unit: UnitUSD, ResetDay: c.resetDay}
			if got := periodStart(b, c.now); !got.Equal(c.start) {
				t.Fatalf("period starts %s, want %s", got, c.start)
			}
			resets := nextReset(b, c.now)
			if (resets == nil) != (c.resetDay == nil) || (resets != nil && !resets.Equal(c.start.AddDate(0, 1, 0))) {
				t.Fatalf("resets %v", resets)
			}
		})
	}
}

func TestABudgetIsValidOnlyWithAPositiveAmountAKnownUnitAndAResetDayEveryMonthHas(t *testing.T) {
	cases := map[string]struct {
		budget db.Budget
		valid  bool
	}{
		"monthly usd":      {db.Budget{Amount: 200, Unit: UnitUSD, ResetDay: day(1)}, true},
		"lifetime credits": {db.Budget{Amount: 1000, Unit: UnitCredits}, true},
		"zero amount":      {db.Budget{Amount: 0, Unit: UnitUSD}, false},
		"unknown unit":     {db.Budget{Amount: 5, Unit: "eur"}, false},
		"day 29":           {db.Budget{Amount: 5, Unit: UnitUSD, ResetDay: day(29)}, false},
		"day 0":            {db.Budget{Amount: 5, Unit: UnitUSD, ResetDay: day(0)}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateBudget(c.budget); (err == nil) != c.valid {
				t.Fatalf("err %v, want valid=%v", err, c.valid)
			}
		})
	}
}

func budgeted(spent float64, period time.Time) *PoolKey {
	return &PoolKey{ID: "k", TierID: "t", IsActive: true, Features: map[string]FeatureLimit{"chat": {RateLimit: 100, WindowSeconds: 60}},
		Budget: &db.Budget{Amount: 200, Unit: UnitUSD, ResetDay: day(1)}, Spent: spent, SpentPeriodStart: &period}
}

func TestAKeyAtItsBudgetIsSkippedUntilItsPeriodRollsOver(t *testing.T) {
	this := periodStart(db.Budget{ResetDay: day(1)}, time.Now())
	cases := map[string]struct {
		key    *PoolKey
		served bool
	}{
		"under budget":           {budgeted(199.99, this), true},
		"at budget":              {budgeted(200, this), false},
		"over budget":            {budgeted(250, this), false},
		"spent in a past period": {budgeted(250, this.AddDate(0, -1, 0)), true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := pooled(c.key)
			if served := draw(m, "chat") != nil; served != c.served {
				t.Fatalf("served %v, want %v", served, c.served)
			}
		})
	}
}

func TestASpendReportIsCheckedAgainstTheKeysBudgetAndCountedInMemory(t *testing.T) {
	this := periodStart(db.Budget{ResetDay: day(1)}, time.Now())
	cases := map[string]struct {
		keyID   string
		unit    string
		stored  float64
		err     error
		inPool  float64
		counted bool
	}{
		"counted":     {"k", UnitUSD, 150, nil, 150, true},
		"wrong unit":  {"k", UnitCredits, 150, ErrUnitMismatch, 100, false},
		"unknown key": {"other", UnitUSD, 150, ErrUnknownKey, 100, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			key := budgeted(100, this)
			m, store := pooled(key)
			store.spent = c.stored
			_, err := m.RecordSpend(context.Background(), &db.SpendEvent{KeyID: c.keyID, Amount: 50, Unit: c.unit, RequestID: "r"})
			if !errors.Is(err, c.err) || key.Spent != c.inPool || (store.spends == 1) != c.counted {
				t.Fatalf("err %v spent %v store calls %d", err, key.Spent, store.spends)
			}
		})
	}
}

func TestAReportThatLandsLateNeverLowersTheSpendHeldForThePeriod(t *testing.T) {
	this := periodStart(db.Budget{ResetDay: day(1)}, time.Now())
	cases := map[string]struct {
		held   time.Time
		stored float64
		want   float64
	}{
		"higher total wins":       {this, 180, 180},
		"older lower total loses": {this, 120, 150},
		"new period starts over":  {this.AddDate(0, -1, 0), 20, 20},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			key := budgeted(150, c.held)
			m, store := pooled(key)
			store.spent = c.stored
			if _, err := m.RecordSpend(context.Background(), &db.SpendEvent{KeyID: "k", Amount: 1, Unit: UnitUSD, RequestID: "r"}); err != nil {
				t.Fatal(err)
			}
			if key.Spent != c.want || !key.SpentPeriodStart.Equal(this) {
				t.Fatalf("held %v from %s, want %v", key.Spent, key.SpentPeriodStart, c.want)
			}
		})
	}
}

func TestASpendReportOnAKeyWithoutABudgetIsRecordedOnly(t *testing.T) {
	key := &PoolKey{ID: "k", TierID: "t", IsActive: true}
	m, store := pooled(key)
	spend, err := m.RecordSpend(context.Background(), &db.SpendEvent{KeyID: "k", Amount: 3, Unit: UnitUSD, RequestID: "r"})
	if err != nil || store.spends != 1 || store.period != nil || spend.Budget != nil || key.SpentPeriodStart != nil {
		t.Fatalf("spend %+v err %v store %+v", spend, err, store)
	}
}
