# Change Summary — keypooler refactor

**Branch:** `feat/rustbox-db-integrations` → merged to `main` via PR #1

## What changed

### Removed (extracted to pulse)
- `internal/queue/` — work queue
- `internal/scheduler/` — cron scheduler
- `internal/worker/` — worker pool
- `internal/executor/` — rustbox HTTP client
- `internal/contract/` — contract.yaml parser
- `internal/webhook/` — callback delivery
- `internal/runner/` — local/Docker runner (dead code, not moved)
- `internal/db/sqlite_executions.go` — execution CRUD
- `internal/db/sqlite_integrations.go` — integration version CRUD
- `migrations/003` through `migrations/007`
- `internal/api/handlers_test.go` — moved to pulse

### Modified
- `cmd/main.go` — stripped to: DB → migrations → key pool → HTTP server only
- `internal/api/handlers.go` — kept tiers/keys/health; added `GetKey` handler
- `internal/api/router.go` — removed execution/integration/dead-letter routes; added `GET /key`
- `internal/config/config.go` — removed WorkerCount, QueueMaxSize, ScriptsPath, RustboxURL, RustboxAPIKey, RustboxTimeoutSec
- `internal/db/adapter.go` — trimmed to tiers + keys only

### Added
- `GET /key?feature=X` — round-robin selects a key for the requested feature, checks rate limit, decrypts and returns plaintext value. Returns `429` if no key is available. Admin-protected.

## Before / after

| Before | After |
|--------|-------|
| Monolithic: key pool + job runtime in one binary | Pure key-pool service |
| Scripts/executions managed internally | No concept of executions |
| Workers needed `ENCRYPTION_KEY` | Decryption at boundary — callers get plaintext from `/key` |
| 17 internal packages | 6 internal packages |
