# keypooler

A local tool for people who juggle multiple API keys.

You have N keys across providers, different rate limits, maybe a free tier and a pro tier. You have scripts that call those APIs. Instead of hardcoding keys and building rate-limit logic into every script — point keypooler at your keys and scripts, and let it handle the rest.

```
you  →  POST /api/execute  →  keypooler picks a key  →  runs your script  →  result
                                    ↓
                             round-robin across keys
                             respects per-tier rate limits
                             retries on failure
                             dead-letters what it can't fix
```

This is not infrastructure. It's a local tool that runs on your machine.

## Why

You signed up for 3 accounts on an image-gen API. One is pro (100 req/min), two are free (10 req/min each). You want to blast through a batch of requests without thinking about which key to use, when to back off, or what to do when one gets rate-limited.

keypooler does that. You register your keys, assign them to tiers, write a script that does the actual API call, and let keypooler distribute work across your keys.

## Architecture

```
┌─────────────────┐      ┌──────────────────────────────────────────┐
│  keypooler       │      │  Docker (script execution)               │
│  (Go binary)    │      │                                          │
│                 │      │  ┌──────────┐ ┌──────────┐ ┌──────────┐  │
│  API server     │─────▶│  │ python3  │ │ bun      │ │ go       │  │
│  queue          │      │  │ .py      │ │ .js/.ts  │ │ .go      │  │
│  scheduler      │      │  └──────────┘ └──────────┘ └──────────┘  │
│  key pool       │      │                                          │
│  SQLite         │      │  runtimes pre-installed,                 │
│                 │      │  you don't need them locally             │
└─────────────────┘      └──────────────────────────────────────────┘
```

**Server** runs natively on your machine — it's a single Go binary. Lightweight, no dependencies beyond SQLite.

**Scripts execute in Docker containers** by default, with runtimes pre-installed (Python, Bun, Go). Build the runtime image with `docker build -f Dockerfile.runtime -t keypooler-runtime .` and keypooler spins up the right container based on the `runtime` field in your `contract.yaml`.

If Docker isn't available, keypooler **automatically falls back to local execution** and logs which runtimes are installed and which are missing, with install instructions for each:

```
WARN  runtime not found  runtime=bun  install="curl -fsSL https://bun.sh/install | bash"
WARN  install missing runtimes above, or set RUNNER_MODE=docker
```

You can also force local mode with `RUNNER_MODE=local`.

## How it works

**Keys live in tiers.** A tier defines features and rate limits. A "pro" tier might allow 100 image-gen calls/min. A "free" tier allows 10. keypooler picks keys round-robin, skipping any that are rate-exhausted.

**Scripts live in folders.** Each folder has a `contract.yaml` and a trigger file. keypooler calls your script with the function name and input as args. The API key arrives via `KEYPOOLER_API_KEY` env var — never visible in `ps`.

Your script writes JSON to stdout:
```json
{"success": true, "data": {"url": "https://..."}}
```

That's the whole interface.

## Quick start

```bash
# generate secrets
export ENCRYPTION_KEY=$(openssl rand -hex 32)
export ADMIN_TOKEN=$(openssl rand -hex 32)

# create dirs
mkdir -p data scripts

# build and run
CGO_ENABLED=1 go build -o keypooler ./cmd/
./keypooler
```

Then register your keys:

```bash
# create a tier
curl -X POST localhost:8080/admin/tiers \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "pro", "features": {"image-generation": 100}}'

# add a key
curl -X POST localhost:8080/admin/keys \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "openai-1", "key": "sk-abc123...", "tier": "pro"}'
```

Run something:

```bash
curl -X POST localhost:8080/api/execute \
  -H "Content-Type: application/json" \
  -d '{"script": "hello-world", "function": "greet", "input": {"name": "world"}}'
```

Sample scripts are included in `scripts/` — `hello-world` (Python), `hello-typescript`, and `hello-go`.

## Script structure

```
scripts/
└── image-gen/
    ├── contract.yaml
    └── trigger.py       (or .js for node/bun/deno, .ts for typescript, .go for go)
```

```yaml
name: image-gen
runtime: python              # python | node | bun | deno | typescript | go
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

Your trigger script receives:
```
python3 trigger.py --function=generate --input='{"prompt":"cat"}'
```

And the key via env: `KEYPOOLER_API_KEY=sk-abc123...`

## API

### Public

| Method | Path | What |
|--------|------|------|
| `GET` | `/health` | health check |
| `POST` | `/api/execute` | submit execution |
| `GET` | `/api/executions/{id}` | check status/result |

### Admin (requires `Authorization: Bearer <ADMIN_TOKEN>`)

| Method | Path | What |
|--------|------|------|
| `POST` | `/admin/tiers` | create tier with features + rate limits |
| `GET` | `/admin/tiers` | list tiers |
| `POST` | `/admin/keys` | add a key (encrypted at rest) |
| `GET` | `/admin/keys` | list keys with live rate usage |
| `DELETE` | `/admin/keys/{id}` | remove a key |
| `GET` | `/admin/health` | pool size, queue depth, schedules |
| `POST` | `/admin/scripts/scan` | reload scripts from disk |
| `GET` | `/admin/dead-letter` | view failed executions |
| `POST` | `/admin/dead-letter/{id}/retry` | retry a failed execution |

## When things fail

1. Script fails → **retry** with exponential backoff + jitter (if enabled in contract)
2. All retries exhausted → moves to **dead letter queue**
3. Dead letters are inspectable and retryable via API
4. `callback_url` set? → webhook fires on success or final failure
5. No key with rate budget? → re-queued with backoff until one frees up

## Configuration

All via env vars. Copy `.env.example` to `.env` and fill in the two required secrets.

| Variable | Default | What |
|----------|---------|------|
| `ENCRYPTION_KEY` | *required* | 32-byte hex for AES-256-GCM |
| `ADMIN_TOKEN` | *required* | bearer token for admin endpoints |
| `SERVER_PORT` | 8080 | listen port |
| `DB_PATH` | ./data/pool.db | SQLite path |
| `WORKER_COUNT` | 10 | concurrent script runners |
| `QUEUE_MAX_SIZE` | 1000 | max pending executions |
| `SCRIPTS_PATH` | ./scripts | script folders location |
| `RUNNER_MODE` | docker | docker/local/auto |
| `RUNNER_IMAGE` | keypooler-runtime | Docker image for script execution |
| `LOG_LEVEL` | info | debug/info/warn/error |
| `LOG_FORMAT` | json | json or pretty |

## Project structure

```
cmd/main.go              → wires everything, starts server
internal/
  api/                   → HTTP handlers, router, middleware, auth
  config/                → env config with validation
  contract/              → contract.yaml parser
  crypto/                → AES-256-GCM encrypt/decrypt
  db/                    → SQLite adapter, migrations, CRUD
  keypool/               → key pool, round-robin, rate tracking
  queue/                 → bounded work queue
  runner/                → script execution (local or Docker)
  scheduler/             → cron scheduling
  webhook/               → callback delivery
  worker/                → worker pool
migrations/              → SQL schema
scripts/                 → your script folders
Dockerfile.runtime       → Docker image with python3, bun, go
```

## Tech

Go 1.22 · SQLite (WAL mode) · AES-256-GCM · zerolog · stdlib net/http · Docker for script runtimes
