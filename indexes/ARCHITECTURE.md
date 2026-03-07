# Keypooler v2 — Architecture (LOCKED)

## What It Is

A key pool service that manages API keys with tiered feature rate limits and executes user scripts via subprocess.

## Key Model

```
Key "openai-prod-1" → tier "tier_3"

Tier "tier_3" {
  "completions": 500/min
  "embeddings": 1000/min
}
```

Key belongs to a tier. Tier defines features with rate limits per minute.

## Script Model

```
/scripts
  /openai-summarizer
    contract.yaml
    trigger.py
  /embedder
    contract.yaml
    trigger.js
```

### Contract

```yaml
name: openai-summarizer
runtime: python

functions:
  summarize:
    feature: completions
    timeout: 60s
    retry:
      enabled: true
      max_attempts: 3
    scheduling:
      enabled: true
      cron: "*/15 * * * *"
      input:
        prompt: "daily summary"
        max_tokens: 500
    input:
      prompt: string
      max_tokens: int?
    output:
      result: string
      tokens_used: int
```

Input/output schemas are documentation — the service doesn't validate at runtime. Scripts follow the interface by convention.

### Script Interface

```
python trigger.py --function=summarize --key=sk-xxx --input='{"prompt":"hello"}'
```

Stdout (one JSON line):
```json
{"success": true, "data": {"result": "...", "tokens_used": 42}}
```
```json
{"success": false, "error": "rate limited by upstream"}
```

---

## Data Model

### DB Tables (SQLite, WAL mode)

#### tiers
```sql
id    TEXT PK
name  TEXT UNIQUE NOT NULL
```

#### tier_features
```sql
tier_id         TEXT FK -> tiers.id
feature         TEXT NOT NULL
rate_per_minute INT NOT NULL
PRIMARY KEY(tier_id, feature)
```

#### keys
```sql
id            TEXT PK
name          TEXT NOT NULL
key_encrypted TEXT NOT NULL
tier_id       TEXT FK -> tiers.id
is_active     BOOL DEFAULT true
created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
```

#### executions
```sql
id            TEXT PK
script        TEXT NOT NULL
function_name TEXT NOT NULL
key_id        TEXT
status        TEXT DEFAULT 'pending'
trigger       TEXT DEFAULT 'api'
callback_url  TEXT
input         TEXT
output        TEXT
error         TEXT
attempts      INT DEFAULT 0
created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
completed_at  TIMESTAMP
```

Status values: pending, running, success, failed, retrying

#### dead_letter
```sql
id            TEXT PK
execution_id  TEXT FK -> executions.id
script        TEXT NOT NULL
function_name TEXT NOT NULL
input         TEXT
error         TEXT
attempts      INT
failed_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
```

Populated when execution fails after all retries exhausted.

### In-Memory Only

- **Scripts + functions** — scanned from filesystem on startup
- **Rate counters** — `(key_id, feature) → {count, window_start}` per-minute window
- **Schedules** — derived from contracts with `scheduling.enabled: true`

---

## Components

```
HTTP Server
  POST /api/execute
  GET  /api/executions/{id}
  POST /admin/tiers
  POST /admin/keys
  DELETE /admin/keys/{id}
  GET  /admin/keys
  GET  /admin/health
  POST /admin/scripts/scan
  GET  /admin/dead-letter
  POST /admin/dead-letter/{id}/retry
       │
       ├──────────────┐
       ▼              ▼
  Scheduler      Work Queue (bounded channel)
  (cron tick)──▶      │
                      ▼
                 Worker Pool ──▶ Key Selector ──▶ Rate Tracker
                 (N goroutines)  (RR / WRR)      (in-memory/min)
                      │
                      ▼
                 Script Runner (subprocess)
                      │
                      ▼
                 On completion ──▶ Webhook (if callback_url set)
```

### Key Selector

1. Filter keys: tier has requested feature, is_active, rate not exhausted
2. Apply strategy (round-robin or weighted round-robin)
3. No key available → requeue with backoff

### Rate Tracker

In-memory `map[(key_id, feature)] → {count, window_start}`. Per-key mutex. Window resets automatically when minute passes. No DB persistence — it's per-minute data, restart is fine.

### Script Runner

1. Resolve runtime (`python3`, `node`, `bun`, `deno run`)
2. Build command with `--function`, `--key`, `--input` args
3. Set working dir to script folder
4. Spawn with timeout context
5. Capture stdout, parse JSON
6. Return result or error

### Retry

Per-function opt-in. On failure:
- `retry.enabled && attempts < max_attempts` → backoff `min(1s * 2^attempt, 30s)` + jitter, pick new key, retry
- All retries exhausted → status=failed, insert into dead_letter

### Scheduler

Single goroutine, ticks every second:
1. Check which schedules are due (`next_run_at <= now`)
2. Create execution (trigger=schedule), enqueue
3. Compute next_run_at from cron expression

Schedules live in memory, derived from contract scan. No DB table.

### Scanner

On startup + `POST /admin/scripts/scan`:
1. Walk `/scripts`, find `contract.yaml` files
2. Parse YAML into Go structs
3. Hold in memory map: `script_name → Contract`
4. Build schedule entries for functions with `scheduling.enabled`

### Webhook

On execution completion (success or final failure):
- If `callback_url` is set on the execution, POST result to that URL
- Fire-and-forget, single attempt, log errors
- Payload matches `GET /api/executions/{id}` response shape

### Dead Letter

- Executions that fail after all retries → copied to `dead_letter`
- `GET /admin/dead-letter` — list failed executions
- `POST /admin/dead-letter/{id}/retry` — creates new execution with same script/function/input

---

## API

### POST /api/execute
```json
{
  "script": "openai-summarizer",
  "function": "summarize",
  "input": {"prompt": "hello"},
  "callback_url": "https://myapp.com/hook"
}
```
`callback_url` is optional.

→ 202 `{"execution_id": "uuid", "status": "pending"}`

### GET /api/executions/{id}
→ 200 `{"id": "...", "script": "...", "function": "...", "status": "success", "output": {...}, "attempts": 1, "created_at": "...", "completed_at": "..."}`

### POST /admin/tiers
```json
{"name": "tier_3", "features": {"completions": 500, "embeddings": 1000}}
```

### POST /admin/keys
```json
{"name": "openai-prod-1", "key": "sk-xxx", "tier": "tier_3"}
```

### DELETE /admin/keys/{id}

### GET /admin/keys
Returns keys with current rate usage per feature.

### GET /admin/health
```json
{"pool_size": 5, "queue_size": 12, "active_schedules": 3, "dead_letter_count": 2}
```

### POST /admin/scripts/scan
Rescans `/scripts` directory, reloads contracts and schedules.

### GET /admin/dead-letter
```json
[
  {
    "id": "uuid",
    "execution_id": "uuid",
    "script": "openai-summarizer",
    "function": "summarize",
    "error": "timeout after 60s",
    "attempts": 3,
    "failed_at": "..."
  }
]
```

### POST /admin/dead-letter/{id}/retry
→ 201 `{"execution_id": "uuid", "status": "pending"}`

---

## Execution Flow

```
1. POST /api/execute {script, function, input, callback_url?}
2. Validate script + function exist in memory
3. Create execution record (DB)
4. Enqueue to work channel
5. Worker picks up
6. Key Selector → find key with rate available for feature
   No key → requeue with backoff
7. Increment rate counter (in-memory)
8. Decrypt key
9. Spawn: python3 scripts/X/trigger.py --function=F --key=K --input='{...}'
10. Parse stdout JSON
11. Success → status=success, persist
12. Failure + retry enabled + attempts left → backoff, retry with new key
13. Failure + done → status=failed, persist, insert dead_letter
14. If callback_url → POST result to webhook
```

---

## Project Structure

```
keypooler/
├── cmd/main.go
├── internal/
│   ├── api/           # handlers, routing, middleware
│   ├── config/        # env config
│   ├── contract/      # contract YAML types + parsing
│   ├── crypto/        # AES-256-GCM
│   ├── db/            # SQLite adapter, migrations
│   ├── keypool/       # key selector, rate tracker, strategies
│   ├── runner/        # subprocess execution
│   ├── scheduler/     # cron tick loop
│   ├── queue/         # bounded work channel
│   ├── webhook/       # callback delivery
│   ├── worker/        # worker pool, retry, dead letter
│   └── util/          # helpers
├── migrations/
├── scripts/
└── Dockerfile
```

---

## Scope — LOCKED

### In scope
- Key management (CRUD, tiers, features, rate limits per minute)
- Key selection strategies (round-robin, weighted round-robin)
- Script execution via subprocess (Python/JS/TS)
- Contract-based function mapping (YAML)
- Retry with exponential backoff + jitter (per-function opt-in)
- Cron scheduling (per-function opt-in)
- Webhooks (callback_url on execute)
- Dead letter queue (inspect + retry failed executions)
- AES-256-GCM key encryption at rest
- Admin auth (Bearer token)
- Graceful shutdown

### Out of scope
- Input/output schema validation at runtime
- Script sandboxing / isolation
- Per-script concurrency caps
- Dependency management (setup commands)
- Delayed one-shot execution (run_at)
- Priority queue / preemption
- Circuit breakers
- Rate usage DB persistence
- Script/function DB tables (filesystem is source of truth)
