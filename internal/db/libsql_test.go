package db

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func tursoClosingEveryStream(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var pipeline struct {
			Requests []struct {
				Type string `json:"type"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(r.Body).Decode(&pipeline); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(10 * time.Millisecond)
		results := make([]map[string]any, len(pipeline.Requests))
		for i, request := range pipeline.Requests {
			response := map[string]any{"type": request.Type}
			if request.Type == "execute" {
				response["result"] = map[string]any{"cols": []any{}, "rows": []any{}, "affected_row_count": 0}
			}
			results[i] = map[string]any{"type": "ok", "response": response}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestLookupsInParallelSurviveAServerThatClosesEveryStream(t *testing.T) {
	adapter, err := NewLibsqlAdapter(tursoClosingEveryStream(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	for burst := 0; burst < 3; burst++ {
		var wg sync.WaitGroup
		failures := make(chan error, 20)
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := adapter.GetConsumerByTokenHash(context.Background(), "hash"); err != nil {
					failures <- err
				}
			}()
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Errorf("burst %d: %v", burst, err)
		}
	}
}
