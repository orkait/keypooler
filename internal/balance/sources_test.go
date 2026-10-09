package balance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/orkait/keypooler/internal/keypool"
)

func TestEachProviderReportsWhatAKeyHasUsedOfItsLimit(t *testing.T) {
	cycleEnd := time.Date(2026, time.November, 7, 14, 25, 47, 0, time.UTC)
	cases := map[string]struct {
		source  func(base string) Source
		path    string
		status  int
		body    string
		want    keypool.Balance
		failure bool
	}{
		"firecrawl": {
			func(base string) Source { return &Firecrawl{base: base, client: http.DefaultClient} }, firecrawlPath, http.StatusOK,
			`{"success":true,"data":{"remaining_credits":996,"plan_credits":1000,"billing_period_start":"2026-10-07T14:25:47Z","billing_period_end":"2026-11-07T14:25:47Z"}}`,
			keypool.Balance{Used: 4, Limit: 1000, Unit: keypool.UnitCredits, ResetsAt: &cycleEnd}, false,
		},
		"tavily key without its own limit": {
			func(base string) Source { return &Tavily{base: base, client: http.DefaultClient} }, tavilyPath, http.StatusOK,
			`{"key":{"usage":581,"limit":null},"account":{"current_plan":"Researcher","plan_usage":581,"plan_limit":1000}}`,
			keypool.Balance{Used: 581, Limit: 1000, Unit: keypool.UnitCredits}, false,
		},
		"tavily key with its own limit": {
			func(base string) Source { return &Tavily{base: base, client: http.DefaultClient} }, tavilyPath, http.StatusOK,
			`{"key":{"usage":90,"limit":100},"account":{"plan_usage":581,"plan_limit":1000}}`,
			keypool.Balance{Used: 90, Limit: 100, Unit: keypool.UnitCredits}, false,
		},
		"apify": {
			func(base string) Source { return &Apify{base: base, client: http.DefaultClient} }, apifyPath, http.StatusOK,
			`{"data":{"monthlyUsageCycle":{"startAt":"2026-10-07T14:25:47Z","endAt":"2026-11-07T14:25:47Z"},"limits":{"maxMonthlyUsageUsd":5},"current":{"monthlyUsageUsd":3.25}}}`,
			keypool.Balance{Used: 3.25, Limit: 5, Unit: keypool.UnitUSD, ResetsAt: &cycleEnd}, false,
		},
		"refused key": {
			func(base string) Source { return &Tavily{base: base, client: http.DefaultClient} }, tavilyPath, http.StatusUnauthorized, `{}`,
			keypool.Balance{}, true,
		},
		"unreadable answer": {
			func(base string) Source { return &Apify{base: base, client: http.DefaultClient} }, apifyPath, http.StatusOK, `not json`,
			keypool.Balance{}, true,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != c.path || r.Header.Get(headerAuthorization) != bearerPrefix+"the-key" {
					t.Errorf("asked %s with %q", r.URL.Path, r.Header.Get(headerAuthorization))
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer server.Close()
			got, err := c.source(server.URL).Check(context.Background(), "the-key")
			if (err != nil) != c.failure {
				t.Fatalf("err %v", err)
			}
			if c.failure {
				return
			}
			if got.Used != c.want.Used || got.Limit != c.want.Limit || got.Unit != c.want.Unit || !sameTime(got.ResetsAt, c.want.ResetsAt) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
