package db

import (
	"context"
	"os"
	"testing"
	"time"
)

// A throwaway Postgres: every test drops what the migrations made and starts clean.
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
	if err := RunMigrations(ctx, a.Pool(), "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return a
}

func seedTier(t *testing.T, a *PostgresAdapter, id string) {
	t.Helper()
	if err := a.CreateTier(context.Background(), &Tier{ID: id, Name: id, Description: "d"}); err != nil {
		t.Fatalf("tier: %v", err)
	}
}

func TestMigrationsRunTwiceWithoutError(t *testing.T) {
	a := freshDB(t)
	if err := RunMigrations(context.Background(), a.Pool(), "../../migrations"); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

func TestTiersAndFeaturesRoundTrip(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	seedTier(t, a, "groq_chat")

	if err := a.SetTierFeatures(ctx, "groq_chat", []*TierFeature{{Feature: "chat", RateLimit: 30, WindowSeconds: 60}, {Feature: "embed", RateLimit: 5, WindowSeconds: 3600}}); err != nil {
		t.Fatal(err)
	}
	features, err := a.GetTierFeatures(ctx, "groq_chat")
	if err != nil || len(features) != 2 || features[0].WindowSeconds != 60 || features[1].WindowSeconds != 3600 {
		t.Fatalf("features %+v err %v", features, err)
	}
	if err := a.UpdateTierDescription(ctx, "groq_chat", "free tier"); err != nil {
		t.Fatal(err)
	}
	tier, err := a.GetTierByName(ctx, "groq_chat")
	if err != nil || tier.Description != "free tier" || tier.CreatedAt.IsZero() {
		t.Fatalf("tier %+v err %v", tier, err)
	}
	if missing, err := a.GetTierByName(ctx, "nope"); missing != nil || err != nil {
		t.Fatalf("missing tier %+v err %v", missing, err)
	}
	if err := a.DeleteTier(ctx, "groq_chat"); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteTier(ctx, "groq_chat"); err == nil {
		t.Fatal("deleting a deleted tier must fail")
	}
}

func TestKeysKeepNullableFieldsMetadataAndCounters(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	seedTier(t, a, "t")
	limit, window := 1000, 2592000
	expires := time.Date(2027, 1, 2, 3, 4, 5, 123456000, time.UTC)
	if err := a.CreateKey(ctx, &Key{ID: "k1", Name: "one", KeyValue: "enc:gcm:x", TierID: "t", IsActive: true,
		ExpiresAt: &expires, UsageLimit: &limit, UsageWindowSeconds: &window, Metadata: map[string]any{"account": "a1"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateKey(ctx, &Key{ID: "k2", Name: "two", KeyValue: "v", TierID: "t"}); err != nil {
		t.Fatal(err)
	}

	if err := a.AddUsage(ctx, "k1", 3); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if err := a.ResetUsageWindow(ctx, "k2", start, 2); err != nil {
		t.Fatal(err)
	}
	if err := a.SetKeyExhausted(ctx, "k2", until); err != nil {
		t.Fatal(err)
	}
	if err := a.SetKeyActive(ctx, "k1", false); err != nil {
		t.Fatal(err)
	}

	k1, err := a.GetKey(ctx, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if k1.IsActive || k1.UsageCount != 3 || *k1.UsageLimit != 1000 || *k1.UsageWindowSeconds != window ||
		!k1.ExpiresAt.Equal(expires) || k1.Metadata["account"] != "a1" || k1.ExhaustedUntil != nil {
		t.Fatalf("k1 %+v", k1)
	}
	k2, err := a.GetKey(ctx, "k2")
	if err != nil {
		t.Fatal(err)
	}
	if k2.UsageCount != 2 || !k2.UsageWindowStart.Equal(start) || !k2.ExhaustedUntil.Equal(until) || k2.ExpiresAt != nil || len(k2.Metadata) != 0 {
		t.Fatalf("k2 %+v", k2)
	}
	all, err := a.GetKeysByTier(ctx, "t")
	if err != nil || len(all) != 2 {
		t.Fatalf("by tier %d err %v", len(all), err)
	}
	if err := a.AddUsage(ctx, "nope", 1); err == nil {
		t.Fatal("adding usage to a missing key must fail")
	}
}

func TestSecretsAreReplacedWholeAndGoWithTheirKey(t *testing.T) {
	a, ctx := freshDB(t), context.Background()
	seedTier(t, a, "t")
	if err := a.CreateKey(ctx, &Key{ID: "k", Name: "k", KeyValue: "v", TierID: "t", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetKeySecrets(ctx, "k", []*KeySecret{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetKeySecrets(ctx, "k", []*KeySecret{{Name: "c", Value: "3"}}); err != nil {
		t.Fatal(err)
	}
	secrets, err := a.GetKeySecrets(ctx, "k")
	if err != nil || len(secrets) != 1 || secrets[0].Name != "c" {
		t.Fatalf("secrets %+v err %v", secrets, err)
	}
	if err := a.DeleteKey(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if left, _ := a.GetKeySecrets(ctx, "k"); len(left) != 0 {
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
	for range 2 {
		if err := a.AddConsumerScope(ctx, "c1", "t"); err != nil {
			t.Fatal(err)
		}
	}
	scopes, err := a.GetConsumerScopes(ctx, "c1")
	if err != nil || len(scopes) != 1 {
		t.Fatalf("scopes %v err %v", scopes, err)
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
	if err != nil || len(events) != 2 {
		t.Fatalf("events %d err %v", len(events), err)
	}
	if !events[0].CreatedAt.Equal(base.Add(2*time.Second)) || events[0].ConsumerID != "c2" {
		t.Fatalf("not newest first, or time not kept: %+v", events[0])
	}
	all, _ := a.ListUsageEvents(ctx, 10)
	blank := 0
	for _, e := range all {
		if e.ConsumerID == "" {
			blank++
		}
	}
	if len(all) != 3 || blank != 1 {
		t.Fatalf("all %d blank %d", len(all), blank)
	}
}
