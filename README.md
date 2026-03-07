# keypooler

You have N API keys across different providers with different rate limits. You have scripts that need those keys. keypooler sits in the middle — you tell it *what to run*, it picks the right key, runs your script, handles retries, and tells you when it's done.

```
POST /api/execute  →  queue  →  worker picks key  →  runs your script  →  result
                                     ↓
                              key pool (round-robin)
                              tier: pro (100 req/min)
                              tier: free (10 req/min)
```

## How it works

**Keys live in tiers.** A tier defines which features a key supports and how fast it can be used. A "pro" tier might allow 100 image-gen calls/min. A "free" tier might allow 10. Keys belong to tiers. The pool picks keys round-robin, skipping any that are rate-exhausted.

**Scripts live in folders.** Each folder has a `contract.yaml` that declares functions, features, timeouts, retry policy, and scheduling. keypooler calls your script as a subprocess:

```
python3 trigger.py --function=generate_image --input='{"prompt":"cat"}'
```

The API key is passed via `KEYPOOLER_API_KEY` env var (not CLI args — won't leak in `ps`).

Your script writes JSON to stdout:
```json
{"success": true, "data": {"url": "https://..."}}
```

That's the contract. keypooler doesn't care what language you use or what API you call.

## Script structure

```
scripts/
└── image-gen/
    ├── contract.yaml
    └── trigger.py
```

```yaml
# contract.yaml
name: image-gen
runtime: python        # python | node | bun | deno
functions:
  generate:
    feature: image-generation
    timeout: 30s
    retry:
      enabled: true
      max_attempts: 3
    scheduling:
      enabled: true
      cron: "*/30 * * * *"
      input:
        prompt: "daily cat"
```

## API

### Public

```
GET  /health                          → {"status": "ok"}
POST /api/execute                     → submit a script execution
GET  /api/executions/{id}             → check execution status
```

**Execute:**
```bash
curl -X POST localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{
    "script": "image-gen",
    "function": "generate",
    "input": {"prompt": "cat on a skateboard"},
    "callback_url": "https://your-app.com/webhook"
  }'
```

Returns `202` with an `execution_id`. Poll `/api/executions/{id}` or use `callback_url` to get notified.

### Admin (requires `Authorization: Bearer <ADMIN_TOKEN>`)

```
POST /admin/tiers                     → create a tier with features + rate limits
GET  /admin/tiers                     → list tiers
POST /admin/keys                      → add a key (encrypted at rest)
GET  /admin/keys                      → list keys with live rate usage
DELETE /admin/keys/{id}               → remove a key
GET  /admin/health                    → pool size, queue depth, active schedules
POST /admin/scripts/scan              → reload scripts from disk
GET  /admin/dead-letter               → view failed executions
POST /admin/dead-letter/{id}/retry    → retry a dead letter entry
```

**Create a tier:**
```bash
curl -X POST localhost:8080/admin/tiers \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "pro", "features": {"image-generation": 100, "text-completion": 200}}'
```

**Add a key:**
```bash
curl -X POST localhost:8080/admin/keys \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "openai-pro-1", "key": "sk-abc123...", "tier": "pro"}'
```

## Setup

```bash
# generate secrets
export ENCRYPTION_KEY=$(openssl rand -hex 32)
export ADMIN_TOKEN=$(openssl rand -hex 32)

# create dirs
mkdir -p data scripts

# run
CGO_ENABLED=1 go build -o keypooler ./cmd/
./keypooler
```

Or with Docker:
```bash
cp .env.example .env   # fill in ENCRYPTION_KEY and ADMIN_TOKEN
docker compose up --build
```

## Configuration

All via environment variables. Sane defaults for everything except the two required secrets.

| Variable | Default | What |
|----------|---------|------|
| `ENCRYPTION_KEY` | *required* | 32-byte hex key for AES-256-GCM |
| `ADMIN_TOKEN` | *required* | Bearer token for `/admin/*` endpoints |
| `SERVER_PORT` | 8080 | HTTP listen port |
| `DB_PATH` | ./data/pool.db | SQLite database path |
| `WORKER_COUNT` | 10 | Concurrent script executors |
| `QUEUE_MAX_SIZE` | 1000 | Max pending executions |
| `SCRIPTS_PATH` | ./scripts | Where to find script folders |
| `LOG_LEVEL` | info | debug, info, warn, error |
| `LOG_FORMAT` | json | json or pretty |

## What happens when things fail

1. Script returns `{"success": false}` or exits non-zero → **retry** (if enabled in contract, with exponential backoff + jitter)
2. All retries exhausted → execution moves to **dead letter queue**
3. Dead letters are inspectable via API and can be retried manually
4. If `callback_url` was set, webhooks fire on both success and final failure
5. No available key with rate budget → execution is re-queued with backoff

## Tech

Go 1.22 · SQLite (WAL) · AES-256-GCM · zerolog · stdlib net/http · no frameworks

## Project structure

```
cmd/main.go              → wires everything, starts server
internal/
  api/                   → HTTP handlers, router, middleware, auth
  config/                → env-based config with validation
  contract/              → contract.yaml parser
  crypto/                → AES-256-GCM encrypt/decrypt
  db/                    → SQLite adapter, migrations, CRUD
  keypool/               → key pool manager, round-robin, rate tracking
  queue/                 → bounded work queue
  runner/                → subprocess execution
  scheduler/             → cron-based job scheduling
  webhook/               → fire-and-forget callback delivery
  worker/                → worker pool with staggered warmup
migrations/              → SQL schema files
scripts/                 → your script folders go here
```
