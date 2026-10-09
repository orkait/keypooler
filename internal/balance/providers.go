package balance

import (
	"context"
	"net/http"
	"time"

	"github.com/orkait/keypooler/internal/keypool"
)

const (
	firecrawlBase = "https://api.firecrawl.dev"
	firecrawlPath = "/v1/team/credit-usage"
	tavilyBase    = "https://api.tavily.com"
	tavilyPath    = "/usage"
	apifyBase     = "https://api.apify.com"
	apifyPath     = "/v2/users/me/limits"
)

type Firecrawl struct {
	base   string
	client *http.Client
}

func (f *Firecrawl) Check(ctx context.Context, apiKey string) (keypool.Balance, error) {
	var body struct {
		Data struct {
			Remaining float64    `json:"remaining_credits"`
			Plan      float64    `json:"plan_credits"`
			PeriodEnd *time.Time `json:"billing_period_end"`
		} `json:"data"`
	}
	if err := getJSON(ctx, f.client, f.base+firecrawlPath, apiKey, &body); err != nil {
		return keypool.Balance{}, err
	}
	d := body.Data
	return keypool.Balance{Used: max(d.Plan-d.Remaining, 0), Limit: d.Plan, Unit: keypool.UnitCredits, ResetsAt: d.PeriodEnd}, nil
}

type Tavily struct {
	base   string
	client *http.Client
}

func (t *Tavily) Check(ctx context.Context, apiKey string) (keypool.Balance, error) {
	var body struct {
		Key struct {
			Usage float64  `json:"usage"`
			Limit *float64 `json:"limit"`
		} `json:"key"`
		Account struct {
			Usage float64 `json:"plan_usage"`
			Limit float64 `json:"plan_limit"`
		} `json:"account"`
	}
	if err := getJSON(ctx, t.client, t.base+tavilyPath, apiKey, &body); err != nil {
		return keypool.Balance{}, err
	}
	if body.Key.Limit != nil {
		return keypool.Balance{Used: body.Key.Usage, Limit: *body.Key.Limit, Unit: keypool.UnitCredits}, nil
	}
	return keypool.Balance{Used: body.Account.Usage, Limit: body.Account.Limit, Unit: keypool.UnitCredits}, nil
}

type Apify struct {
	base   string
	client *http.Client
}

func (a *Apify) Check(ctx context.Context, apiKey string) (keypool.Balance, error) {
	var body struct {
		Data struct {
			Cycle struct {
				EndAt *time.Time `json:"endAt"`
			} `json:"monthlyUsageCycle"`
			Limits struct {
				MaxUSD float64 `json:"maxMonthlyUsageUsd"`
			} `json:"limits"`
			Current struct {
				UsedUSD float64 `json:"monthlyUsageUsd"`
			} `json:"current"`
		} `json:"data"`
	}
	if err := getJSON(ctx, a.client, a.base+apifyPath, apiKey, &body); err != nil {
		return keypool.Balance{}, err
	}
	d := body.Data
	return keypool.Balance{Used: d.Current.UsedUSD, Limit: d.Limits.MaxUSD, Unit: keypool.UnitUSD, ResetsAt: d.Cycle.EndAt}, nil
}
