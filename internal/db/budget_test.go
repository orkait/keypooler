package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func spend(t *testing.T, a *PostgresAdapter, keyID, requestID string, amount float64, period *time.Time) (float64, bool) {
	t.Helper()
	spent, duplicate, err := a.RecordSpend(context.Background(), &SpendEvent{
		ID: requestID + "-event", KeyID: keyID, ConsumerID: "c1", Amount: amount, Unit: "usd", RequestID: requestID, CreatedAt: time.Now(),
	}, period)
	if err != nil {
		t.Fatalf("spend %s: %v", requestID, err)
	}
	return spent, duplicate
}

func TestSpendCountsOncePerRequestAndRestartsEachPeriod(t *testing.T) {
	a := freshDB(t)
	seedTier(t, a, "t")
	day := 1
	if err := a.CreateKey(context.Background(), &Key{ID: "k", Name: "k", KeyValue: "v", TierID: "t", IsActive: true,
		Budget: &Budget{Amount: 200, Unit: "usd", ResetDay: &day}, Metadata: map[string]any{}}, nil); err != nil {
		t.Fatal(err)
	}
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	november := october.AddDate(0, 1, 0)

	steps := []struct {
		request   string
		amount    float64
		period    time.Time
		spent     float64
		duplicate bool
	}{
		{"r1", 2.5, october, 2.5, false},
		{"r1", 2.5, october, 2.5, true},
		{"r2", 1, october, 3.5, false},
		{"r3", 1, november, 1, false},
		{"r2", 1, november, 1, true},
	}
	for _, s := range steps {
		spent, duplicate := spend(t, a, "k", s.request, s.amount, &s.period)
		if spent != s.spent || duplicate != s.duplicate {
			t.Fatalf("%s in %s: spent %v duplicate %v, want %v %v", s.request, s.period.Month(), spent, duplicate, s.spent, s.duplicate)
		}
	}
	k := keyByID(t, a, "k")
	if k.Budget == nil || k.Budget.Amount != 200 || k.Budget.Unit != "usd" || *k.Budget.ResetDay != 1 ||
		k.Spent != 1 || !k.SpentPeriodStart.Equal(november) {
		t.Fatalf("stored %+v budget %+v", k, k.Budget)
	}
}

func TestSpendOnAKeyWithoutABudgetIsRecordedButNotCounted(t *testing.T) {
	a := freshDB(t)
	seedTier(t, a, "t")
	if err := a.CreateKey(context.Background(), &Key{ID: "k", Name: "k", KeyValue: "v", TierID: "t", IsActive: true, Metadata: map[string]any{}}, nil); err != nil {
		t.Fatal(err)
	}
	if spent, duplicate := spend(t, a, "k", "r1", 3, nil); spent != 0 || duplicate {
		t.Fatalf("spent %v duplicate %v", spent, duplicate)
	}
	var events int
	if err := a.Pool().QueryRow(context.Background(), "SELECT count(*) FROM spend_events WHERE key_id = 'k'").Scan(&events); err != nil || events != 1 {
		t.Fatalf("events %d err %v", events, err)
	}
	if k := keyByID(t, a, "k"); k.Budget != nil || k.Spent != 0 || k.SpentPeriodStart != nil {
		t.Fatalf("stored %+v", k)
	}
}

func TestABudgetChangeKeepsSpendUnlessTheUnitChanges(t *testing.T) {
	a := freshDB(t)
	seedTier(t, a, "t")
	ctx := context.Background()
	day := 1
	if err := a.CreateKey(ctx, &Key{ID: "k", Name: "k", KeyValue: "v", TierID: "t", IsActive: true,
		Budget: &Budget{Amount: 200, Unit: "usd", ResetDay: &day}, Metadata: map[string]any{}}, nil); err != nil {
		t.Fatal(err)
	}
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	spend(t, a, "k", "r1", 5, &october)

	cases := []struct {
		budget *Budget
		spent  float64
	}{
		{&Budget{Amount: 300, Unit: "usd", ResetDay: &day}, 5},
		{&Budget{Amount: 1000, Unit: "credits"}, 0},
		{nil, 0},
	}
	for _, c := range cases {
		if err := a.UpdateKeyBudget(ctx, "k", c.budget); err != nil {
			t.Fatal(err)
		}
		k := keyByID(t, a, "k")
		if (k.Budget == nil) != (c.budget == nil) || k.Spent != c.spent {
			t.Fatalf("after %+v: budget %+v spent %v", c.budget, k.Budget, k.Spent)
		}
	}
	if err := a.UpdateKeyBudget(ctx, "missing", nil); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("missing key: %v", err)
	}
}

func TestATierIsDeletedWithItsFeaturesAndScopesOnlyWhenNoKeyUsesIt(t *testing.T) {
	a := freshDB(t)
	ctx := context.Background()
	for _, id := range []string{"empty", "used"} {
		if err := a.CreateTier(ctx, &Tier{ID: id, Name: id}, []*TierFeature{{TierID: id, Feature: id + "_f", RateLimit: 1, WindowSeconds: 60}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.CreateConsumer(ctx, &Consumer{ID: "c", Name: "c", TokenHash: "h", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.AddConsumerScope(ctx, "c", "empty"); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateKey(ctx, &Key{ID: "k", Name: "k", KeyValue: "v", TierID: "used", IsActive: true, Metadata: map[string]any{}}, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id  string
		err error
	}{
		{"used", ErrTierInUse},
		{"empty", nil},
		{"empty", ErrTierNotFound},
	}
	for _, c := range cases {
		if err := a.DeleteTier(ctx, c.id); !errors.Is(err, c.err) {
			t.Fatalf("delete %s: %v, want %v", c.id, err, c.err)
		}
	}
	if scopes, _ := a.GetConsumerScopes(ctx, "c"); len(scopes) != 0 {
		t.Fatalf("scopes left %v", scopes)
	}
}
