# Keypooler - Codebase Overview

## What It Is

A generic API key pooling proxy in Go. It sits between your application and any Bearer-token-authenticated external API, managing a pool of API keys with automatic rotation, failure handling, rate limiting, and retry logic.

## Core Architecture

```
Request Sources (HTTP API)
        |
  Priority Queue (3-level, heap-based, preemption)
        |
  Worker Pool (N goroutines, staggered warmup)
        |
  Key Pool Manager (Round Robin / Weighted RR, circuit breakers)
        |
  HTTP Client --> External API
        |
  SQLite (WAL mode, AES-256-GCM encrypted keys at rest)
```

## Key Design Patterns

| Pattern | Implementation | Purpose |
|---------|---------------|---------|
| Circuit Breaker | `keypool/circuitbreaker.go` | 3-state (Closed/Open/Half-Open) per key, auto-recovery |
| Strategy Pattern | `keypool/strategy.go` | Pluggable key selection (RR, Weighted RR) |
| Priority Queue | `queue/priority_queue.go` | Min-heap with 3 levels + FIFO within level |
| Preemption | `queue/queue.go` | High-priority evicts low-priority when full |
| Exponential Backoff | `worker/retry.go` | `min(base * 2^attempt, max) + jitter` |
| Load Shedding | `worker/worker.go` | 2-threshold rejection based on healthy key ratio |
| Hot Reload | `config/reload.go` | DB-driven runtime config changes without restart |
| Producer-Consumer | `queue/queue.go` + `worker/worker.go` | Queue dispatch via channel to worker goroutines |
| Idempotency | `api/handlers.go` | Deduplication via idempotency_key |
| Encryption at Rest | `crypto/crypto.go` | AES-256-GCM for stored API keys |

## Module & Dependencies

- **Module:** `key-pool-system`
- **Go Version:** 1.22.2
- **Direct deps:** `go-sqlite3` (CGO)
- **Indirect deps:** `zerolog` (logging), `google/uuid`, `go-colorable`, `go-isatty`

## Package Map

```
cmd/main.go              Entrypoint: wires all components, graceful shutdown
internal/
  api/                   HTTP handlers, routing, middleware (stdlib net/http)
  config/                Env-based config loading, validation, hot reload
  crypto/                AES-256-GCM encrypt/decrypt for API keys
  db/                    SQLite adapter, migrations runner, CRUD operations
  httpclient/            HTTP client wrapper for downstream API calls
  keypool/               Key pool manager, selection strategies, circuit breaker
  metrics/               Prometheus-format metrics (atomic counters/gauges)
  queue/                 Bounded priority queue with preemption + dispatch loop
  util/                  Context timeouts, JSON parsing helpers
  worker/                Worker pool with warmup, retry logic, request processing
migrations/              SQL migration files (001-005)
```

## Request Lifecycle

1. `POST /api/requests` -> validate, save to DB (status=pending), check idempotency
2. Enqueue `Item{RequestID, Priority, CreatedAt}` into priority queue
3. `dispatchLoop` pops from heap, sends to `dequeue` channel
4. Worker receives item, loads full request from DB
5. `Manager.GetKey()` -> filters by circuit breaker + rate limits + concurrency -> applies strategy
6. Decrypt key (AES-256-GCM), build HTTP request, execute via `HTTPClient.Do()`
7. On 2xx: mark key healthy, persist success result
8. On 429/5xx: mark key failed (circuit breaker), retry with backoff+jitter if attempts remain
9. On 4xx (not 429): permanent failure, no retry, key stays healthy
10. If no key available: requeue with backoff delay

## Startup Sequence

```
Config.Load() -> Logger -> SQLite(WAL) -> RunMigrations
-> HotReloadConfig -> KeyPool Manager -> Priority Queue
-> HTTP Client -> Metrics -> Worker Pool (staggered warmup)
-> Key Reload Loop -> Config Reload Loop
-> HTTP Server -> Signal handler (SIGINT/SIGTERM) -> Graceful shutdown
```

## Security Model

- **Encryption:** API keys stored as AES-256-GCM ciphertext (hex), 32-byte key from env
- **Admin Auth:** Bearer token with constant-time comparison (`crypto/subtle`)
- **Secrets:** ENCRYPTION_KEY (64-char hex) and ADMIN_TOKEN required, never logged
- **DB:** SQLite single-writer, WAL mode, busy timeout for contention

## Configuration

All via environment variables with sensible defaults. Hot-reloadable subset stored in `system_config` DB table and refreshed every `CONFIG_RELOAD_INTERVAL_SECONDS` (default 30s).

**Required env vars:** `ENCRYPTION_KEY` (64 hex chars), `ADMIN_TOKEN`

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.22 |
| Database | SQLite (go-sqlite3, CGO) |
| HTTP | stdlib `net/http` (no framework) |
| Logging | zerolog (structured JSON) |
| IDs | google/uuid |
| Encryption | stdlib crypto/aes + crypto/cipher |
| Containers | Docker multi-stage + Compose |
| Metrics | Custom Prometheus text format |
