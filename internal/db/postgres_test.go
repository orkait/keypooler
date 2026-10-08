package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func freshDB(t *testing.T) *PostgresAdapter {
	t.Helper()
	dsn := os.Getenv("KEYPOOLER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("KEYPOOLER_TEST_DATABASE_URL unset")
	}
	a, err := NewPostgresAdapter(dsn, 4)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	ctx := context.Background()
	if _, err := a.Pool().Exec(ctx, `DROP TABLE IF EXISTS usage_events, consumer_scopes, consumers,
		key_secrets, keys, tier_features, tiers, schema_migrations`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	for range 2 {
		if err := RunMigrations(ctx, a.Pool(), "../../migrations"); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return a
}

func seedTier(t *testing.T, a *PostgresAdapter, id string) {
	t.Helper()
	if err := a.CreateTier(context.Background(), &Tier{ID: id, Name: id}, nil); err != nil {
		t.Fatalf("tier: %v", err)
	}
}

func keyByID(t *testing.T, a *PostgresAdapter, id string) *Key {
	t.Helper()
	keys, err := a.GetAllKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if k.ID == id {
			return k
		}
	}
	return nil
}

func TestTiersAndFeaturesRoundTrip(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	seedTier(t, a, "groq_chat")
	if err := a.SetTierFeatures(ctx, "groq_chat", []*TierFeature{{Feature: "embed", RateLimit: 5, WindowSeconds: 3600}, {Feature: "chat", RateLimit: 30, WindowSeconds: 60}}); err != nil {
		t.Fatal(err)
	}
	byTier, err := a.TierFeaturesByTier(ctx)
	if chat := byTier["groq_chat"]; err != nil || len(chat) != 2 || chat[0].Feature != "chat" || chat[1].WindowSeconds != 3600 {
		t.Fatalf("features %+v err %v", byTier, err)
	}
	if err := a.UpdateTierDescription(ctx, "groq_chat", "free tier"); err != nil {
		t.Fatal(err)
	}
	if tier, err := a.GetTierByName(ctx, "groq_chat"); err != nil || tier.Description != "free tier" {
		t.Fatalf("tier %+v err %v", tier, err)
	}
	if missing, err := a.GetTierByName(ctx, "nope"); missing != nil || err != nil {
		t.Fatalf("missing tier %+v err %v", missing, err)
	}
}

func TestATierOrKeyLandsWithItsChildrenOrNotAtAll(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	if err := a.CreateTier(ctx, &Tier{ID: "t", Name: "t"}, []*TierFeature{{Feature: "chat", RateLimit: 1}, {Feature: "chat", RateLimit: 2}}); err == nil {
		t.Fatal("a feature that cannot be stored must fail the tier with it")
	}
	if tier, _ := a.GetTierByName(ctx, "t"); tier != nil {
		t.Fatal("the tier of a failed create was left behind")
	}
	seedTier(t, a, "t")
	twice := []*KeySecret{{KeyID: "k", Name: "dup", Value: "1"}, {KeyID: "k", Name: "dup", Value: "2"}}
	if err := a.CreateKey(ctx, &Key{ID: "k", Name: "k", KeyValue: "v", TierID: "t"}, twice); err == nil {
		t.Fatal("a secret that cannot be stored must fail the key with it")
	}
	if keyByID(t, a, "k") != nil {
		t.Fatal("the key of a failed create was left behind")
	}
}

func TestATakenNameIsADuplicate(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	seedTier(t, a, "t")
	if err := a.CreateConsumer(ctx, &Consumer{ID: "c", Name: "c", TokenHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateTier(ctx, &Tier{ID: "t2", Name: "t"}, nil); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("tier err %v, want ErrDuplicate", err)
	}
	if err := a.CreateConsumer(ctx, &Consumer{ID: "c2", Name: "c", TokenHash: "h2"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("consumer err %v, want ErrDuplicate", err)
	}
}

func TestKeysKeepNullableFieldsMetadataAndCounters(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	seedTier(t, a, "t")
	limit, window := 1000, 2592000
	expires := time.Date(2027, 1, 2, 3, 4, 5, 123456000, time.UTC)
	secrets := []*KeySecret{{KeyID: "k1", Name: "a", Value: "1"}, {KeyID: "k1", Name: "b", Value: "2"}}
	if err := a.CreateKey(ctx, &Key{ID: "k1", Name: "one", KeyValue: "enc:gcm:x", TierID: "t", IsActive: true,
		ExpiresAt: &expires, UsageLimit: &limit, UsageWindowSeconds: &window, Metadata: map[string]any{"account": "a1"}}, secrets); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateKey(ctx, &Key{ID: "k2", Name: "two", KeyValue: "v", TierID: "t"}, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, err := range []error{
		a.AddUsage(ctx, "k1", 3),
		a.ResetUsageWindow(ctx, "k2", start, 2),
		a.SetKeyExhausted(ctx, "k2", until),
		a.AddUsage(ctx, "deleted", 1),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	k1 := keyByID(t, a, "k1")
	if !k1.IsActive || k1.UsageCount != 3 || *k1.UsageLimit != 1000 || *k1.UsageWindowSeconds != window ||
		!k1.ExpiresAt.Equal(expires) || k1.Metadata["account"] != "a1" || k1.ExhaustedUntil != nil {
		t.Fatalf("k1 %+v", k1)
	}
	k2 := keyByID(t, a, "k2")
	if k2.UsageCount != 2 || !k2.UsageWindowStart.Equal(start) || !k2.ExhaustedUntil.Equal(until) || k2.ExpiresAt != nil || len(k2.Metadata) != 0 {
		t.Fatalf("k2 %+v", k2)
	}
	if byKey, err := a.KeySecretsByKey(ctx); err != nil || len(byKey["k1"]) != 2 || byKey["k1"][0].Name != "a" {
		t.Fatalf("secrets %+v err %v", byKey, err)
	}
	if err := a.DeleteKey(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if left, _ := a.KeySecretsByKey(ctx); len(left) != 0 {
		t.Fatalf("orphan secrets %+v", left)
	}
}

func TestConsumersScopesAreIdempotentAndInactiveNeverAuthenticates(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	seedTier(t, a, "t")
	if err := a.CreateConsumer(ctx, &Consumer{ID: "c1", Name: "ai-gateway", TokenHash: "h1", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateConsumer(ctx, &Consumer{ID: "c2", Name: "old", TokenHash: "h2"}); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateConsumer(ctx, &Consumer{ID: "c3", Name: "twin", TokenHash: "h1", IsActive: true}); err == nil {
		t.Fatal("a second consumer with the same token hash must be refused")
	}
	for range 2 {
		if err := a.AddConsumerScope(ctx, "c1", "t"); err != nil {
			t.Fatal(err)
		}
	}
	if scopes, err := a.GetConsumerScopes(ctx, "c1"); err != nil || len(scopes) != 1 {
		t.Fatalf("scopes %v err %v", scopes, err)
	}
	if all, err := a.ConsumerScopesByConsumer(ctx); err != nil || len(all) != 1 || all["c1"][0] != "t" {
		t.Fatalf("all scopes %v err %v", all, err)
	}
	if c, err := a.GetConsumerByTokenHash(ctx, "h1"); err != nil || c == nil || c.Name != "ai-gateway" {
		t.Fatalf("active %+v err %v", c, err)
	}
	if c, err := a.GetConsumerByTokenHash(ctx, "h2"); err != nil || c != nil {
		t.Fatalf("inactive %+v err %v", c, err)
	}
	if err := a.DeleteConsumer(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if left, _ := a.GetConsumerScopes(ctx, "c1"); len(left) != 0 {
		t.Fatalf("orphan scopes %v", left)
	}
}

func TestUsageEventsLandInOneBatchKeepingWhenTheyHappened(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	base := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	var batch []*UsageEvent
	for i, consumer := range []string{"c1", "", "c2"} {
		e := NewUsageEvent("k", consumer, "chat")
		e.CreatedAt = base.Add(time.Duration(i) * time.Second)
		batch = append(batch, e)
	}
	if err := a.RecordUsageEvents(ctx, batch); err != nil {
		t.Fatal(err)
	}
	events, err := a.ListUsageEvents(ctx, 2)
	if err != nil || len(events) != 2 || !events[0].CreatedAt.Equal(base.Add(2*time.Second)) || events[0].ConsumerID != "c2" {
		t.Fatalf("want the two newest first with their times kept: %+v err %v", events, err)
	}
	if all, _ := a.ListUsageEvents(ctx, 10); len(all) != 3 || all[1].ConsumerID != "" {
		t.Fatalf("all %+v", all)
	}
}
