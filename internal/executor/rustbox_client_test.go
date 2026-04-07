package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"key-pool-system/internal/contract"
	"key-pool-system/internal/db"
)

func TestRustboxClientExecuteSuccess(t *testing.T) {
	t.Parallel()

	type submitRequest struct {
		Language string `json:"language"`
		Code     string `json:"code"`
		Stdin    string `json:"stdin"`
	}

	var got submitRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/submit" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.RawQuery != "wait=true" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		if auth := r.Header.Get("X-API-Key"); auth != "rb_local_test" {
			t.Fatalf("unexpected X-API-Key: %s", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                "job-1",
			"language":          "python",
			"job_status":        "completed",
			"schema_version":    "1.0",
			"verdict":           "AC",
			"stdout":            `{"success":true,"data":{"ok":true}}`,
			"stderr":            "",
			"error_message":     nil,
			"cpu_time_secs":     0.01,
			"wall_time_secs":    0.02,
			"memory_peak_bytes": 1024,
		})
	}))
	defer server.Close()

	client := NewRustboxClient(server.URL, "rb_local_test", 5*time.Second)
	version := &db.IntegrationVersion{
		IntegrationName: "demo-service",
		FunctionName:    "generate",
		Version:         3,
		Runtime:         "python",
		Code:            `print("hello")`,
	}
	fn := &contract.Function{Timeout: "15s"}

	result, err := client.Execute(context.Background(), version, fn, "selected-key", `{"prompt":"hi"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success result, got %#v", result)
	}
	if got.Language != "python" {
		t.Fatalf("expected language python, got %q", got.Language)
	}
	if got.Code != version.Code {
		t.Fatalf("expected submitted code to match version code")
	}

	var stdinEnvelope map[string]any
	if err := json.Unmarshal([]byte(got.Stdin), &stdinEnvelope); err != nil {
		t.Fatalf("failed to decode stdin envelope: %v", err)
	}
	if stdinEnvelope["api_key"] != "selected-key" {
		t.Fatalf("expected api_key in stdin envelope")
	}
	if stdinEnvelope["integration"] != "demo-service" {
		t.Fatalf("expected integration in stdin envelope")
	}
}
