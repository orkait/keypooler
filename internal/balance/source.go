package balance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/orkait/keypooler/internal/keypool"
)

const (
	headerAuthorization = "Authorization"
	bearerPrefix        = "Bearer "
)

var ErrProvider = errors.New("provider balance unavailable")

type Source interface {
	Check(ctx context.Context, apiKey string) (keypool.Balance, error)
}

func Sources(client *http.Client) map[string]Source {
	return map[string]Source{
		"firecrawl_scrape": &Firecrawl{base: firecrawlBase, client: client},
		"tavily_search":    &Tavily{base: tavilyBase, client: client},
		"apify_actor":      &Apify{base: apifyBase, client: client},
	}
}

func getJSON(ctx context.Context, client *http.Client, url, apiKey string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProvider, err)
	}
	req.Header.Set(headerAuthorization, bearerPrefix+apiKey)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProvider, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: HTTP %d", ErrProvider, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrProvider, err)
	}
	return nil
}
