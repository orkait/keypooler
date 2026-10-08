package keypool

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/orkait/keypooler/internal/db"
)

const (
	UnitUSD     = "usd"
	UnitCredits = "credits"

	firstResetDay = 1
	lastResetDay  = 28
)

var units = []string{UnitUSD, UnitCredits}

var lifetime = time.Unix(0, 0).UTC()

var (
	ErrUnknownKey   = errors.New("key not found")
	ErrUnitMismatch = errors.New("unit does not match the key's budget")
	ErrBadBudget    = errors.New("invalid budget")
	ErrBadSpend     = errors.New("invalid spend")
)

func ValidateBudget(b db.Budget) error {
	if err := validAmount(b.Amount, b.Unit); err != nil {
		return fmt.Errorf("%w: %w", ErrBadBudget, err)
	}
	if b.ResetDay != nil && (*b.ResetDay < firstResetDay || *b.ResetDay > lastResetDay) {
		return fmt.Errorf("%w: reset_day must be %d to %d, or absent for a lifetime budget", ErrBadBudget, firstResetDay, lastResetDay)
	}
	return nil
}

func ValidateSpend(amount float64, unit, requestID string) error {
	if err := validAmount(amount, unit); err != nil {
		return fmt.Errorf("%w: %w", ErrBadSpend, err)
	}
	if requestID == "" {
		return fmt.Errorf("%w: request_id is required", ErrBadSpend)
	}
	return nil
}

func validAmount(amount float64, unit string) error {
	switch {
	case amount <= 0:
		return errors.New("amount must be above 0")
	case !slices.Contains(units, unit):
		return fmt.Errorf("unit must be one of %v", units)
	}
	return nil
}

func periodStart(b db.Budget, now time.Time) time.Time {
	if b.ResetDay == nil {
		return lifetime
	}
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), *b.ResetDay, 0, 0, 0, 0, time.UTC)
	if now.Before(start) {
		start = start.AddDate(0, -1, 0)
	}
	return start
}

func nextReset(b db.Budget, now time.Time) *time.Time {
	if b.ResetDay == nil {
		return nil
	}
	next := periodStart(b, now).AddDate(0, 1, 0)
	return &next
}

type Spend struct {
	KeyID     string
	Budget    *db.Budget
	Spent     float64
	ResetsAt  *time.Time
	Duplicate bool
}

func Remaining(b *db.Budget, spent float64) *float64 {
	if b == nil {
		return nil
	}
	left := max(b.Amount-spent, 0)
	return &left
}

func (k *PoolKey) spentNow(now time.Time) float64 {
	if k.Budget == nil || k.SpentPeriodStart == nil || !k.SpentPeriodStart.Equal(periodStart(*k.Budget, now)) {
		return 0
	}
	return k.Spent
}

func (k *PoolKey) holdSpent(spent float64, start time.Time) {
	if k.SpentPeriodStart != nil && k.SpentPeriodStart.Equal(start) && spent < k.Spent {
		return
	}
	k.Spent, k.SpentPeriodStart = spent, &start
}

func (k *PoolKey) withinBudget(now time.Time) bool {
	return k.Budget == nil || k.spentNow(now) < k.Budget.Amount
}
