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
		if gotKey := r.Header.Get("Idempotency-Key"); gotKey != "exec-1" {
			t.Fatalf("unexpected Idempotency-Key: %s", gotKey)
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

	result, err := client.ExecuteWithID(context.Background(), "exec-1", version, fn, "selected-key", `{"prompt":"hi"}`)
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

func TestRustboxClientExecuteAcceptedPollsToCompletion(t *testing.T) {
	t.Parallel()

	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/submit":
			if r.Header.Get("Idempotency-Key") != "exec-2" {
				t.Fatalf("missing idempotency key")
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "job-2",
				"job_status": "pending",
			})
		case r.URL.Path == "/api/result/job-2":
			polls++
			w.Header().Set("Content-Type", "application/json")
			if polls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":         "job-2",
					"job_status": "running",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":            "job-2",
				"job_status":    "completed",
				"verdict":       "AC",
				"stdout":        `{"success":true,"data":{"done":true}}`,
				"stderr":        "",
				"error_message": nil,
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewRustboxClient(server.URL, "rb_local_test", 5*time.Second)
	version := &db.IntegrationVersion{
		IntegrationName: "demo-service",
		FunctionName:    "generate",
		Version:         1,
		Runtime:         "python",
		Code:            `print("hello")`,
	}

	result, err := client.ExecuteWithID(context.Background(), "exec-2", version, &contract.Function{Timeout: "15s"}, "selected-key", `{}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got %#v", result)
	}
	if polls < 2 {
		t.Fatalf("expected polling, got %d polls", polls)
	}
}

func TestRustboxClientPollResultHandlesHTTPErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/submit":
			w.WriteHeader(http.StatusRequestTimeout)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "job-3"})
		case "/api/result/job-3":
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid api key"})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewRustboxClient(server.URL, "rb_local_test", 5*time.Second)
	version := &db.IntegrationVersion{
		IntegrationName: "demo-service",
		FunctionName:    "generate",
		Version:         1,
		Runtime:         "python",
		Code:            `print("hello")`,
	}

	_, err := client.ExecuteWithID(context.Background(), "exec-3", version, &contract.Function{Timeout: "15s"}, "selected-key", `{}`)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got == "" || got == "context deadline exceeded" {
		t.Fatalf("expected direct http error, got %q", got)
	}
}
