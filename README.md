<div align="center">

# 🔑 keypooler

**One source of truth for API keys in the orkait stack.** Round-robin rotation, per-feature rate limits, monthly usage budgets, scoped consumers, bound secrets, and opt-in encryption at rest.

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![CGO free](https://img.shields.io/badge/CGO-free-00ADD8?logo=go&logoColor=white)](#-tech)
[![Postgres](https://img.shields.io/badge/Postgres-pgx-4169E1?logo=postgresql&logoColor=white)](https://github.com/jackc/pgx)
[![Deployed on Railway](https://img.shields.io/badge/deployed%20on-Railway-0B0D0E?logo=railway&logoColor=white)](https://railway.app)

</div>

Other services (e.g. the siphon runner) call keypooler to obtain a usable key for a feature. They never store, rotate, or decrypt credentials themselves.

```
caller  ──►  GET /key?feature=firecrawl_scrape  ──►  keypooler picks a key  ──►  { value, secrets, metadata }
                                                          │
                                                          ├─ scope-filters to the caller's allowed tiers
                                                          ├─ round-robins across the pool
                                                          ├─ enforces per-feature rate + usage budgets
                                                          └─ audits the serve
```

## 🧭 What it does

| Capability | Detail |
|---|---|
| 🔄 **Rotation** | Round-robin across all keys in a tier; exhausted keys are skipped, the next is tried. |
| ⏱️ **Rate limits** | Per-feature, windowed (e.g. 10 calls / 60s). Defined on the tier, applied per key. |
| 📅 **Usage budgets** | Per-key `usage_limit` with an optional `usage_window_seconds` (e.g. 2592000 = monthly auto-reset). `nil` window = lifetime cap. |
| 👤 **Scoped consumers** | Each client gets a bearer token scoped to specific tiers. The admin token is a superuser. |
| 🔗 **Bound secrets** | Extra named secrets travel with a key (e.g. a Firecrawl `webhook_secret`), returned at serve time. |
| 🔐 **Opt-in encryption** | Plaintext at rest by default; set `ENCRYPTION_KEY` to encrypt new writes. Self-tagged, so both coexist. |
| 📜 **Audit** | Every serve appends a `usage_events` row (key, consumer, feature, time). |
| ⏳ **Expiry** | Optional `expires_at` per key; expired keys leave the pool automatically. |

## 🏗️ Architecture

Keys live in **tiers**. A tier names the features it covers and the rate limit for each. **Consumers** are scoped clients that may only draw from the tiers they are granted. The admin token bypasses scoping.

```mermaid
flowchart LR
    C[caller] -->|Bearer token| K{/key?feature=X}
    K -->|admin token| ALL[all tiers]
    K -->|consumer token| SC[scoped tiers only]
    ALL & SC --> F[filter pool:<br/>active, unexpired, under usage limit,<br/>supports feature, has rate budget]
    F --> RR[round-robin pick]
    RR --> G[TryRate + TryConsumeUsage]
    G -->|ok| R[return value + secrets + metadata]
    G -->|none left| E[403 out-of-scope / 429 exhausted]
    R -.async.-> A[(usage_events audit)]
```

**Auth model**

| Caller | `/key` access | `/admin/*` |
|---|---|---|
| Admin token | every tier (superuser) | full |
| Consumer token | only granted tiers (`401` unknown, `403` out-of-scope) | denied |

<details>
<summary>🗄️ <b>Data model</b> (7 tables)</summary>

| Table | Holds |
|---|---|
| `tiers` | feature group + `description` |
| `tier_features` | per-feature `rate_limit` + `window_seconds` |
| `keys` | the key value (`plaintext` or `enc:gcm:…`), tier, `expires_at`, `usage_limit`, `usage_window_seconds/start`, `metadata_json` |
| `key_secrets` | named secrets bound to a key (e.g. `webhook_secret`) |
| `consumers` | scoped clients, bearer token stored sha256-hashed (shown once) |
| `consumer_scopes` | which tiers a consumer may draw from |
| `usage_events` | append-only audit, one row per serve |

Migrations are a single consolidated `migrations/001_init.sql`, idempotent (`CREATE ... IF NOT EXISTS`). Deletes also clean up children explicitly, in one transaction, so a reused id can never re-grant an old scope.
</details>

<details>
<summary>⚡ <b>The serve path</b>: no database round trip</summary>

| On every `/key` | Where it lives |
|---|---|
| Keys, tiers, rate windows, usage gates | in memory, loaded at boot and on admin writes (`internal/keypool`) |
| Consumer token to its scopes | in memory for a minute; any admin write clears it, so a revocation holds on the next call (`internal/api/authcache.go`) |
| Usage counts and audit events | queued, then written every second and on shutdown: counts coalesced per key, events in one `COPY` (`internal/writeback`). A crash loses at most a second of them; a database outage keeps up to 10,000 events for the next flush |

Measured locally against Postgres 17: 2,000 consumer draws over one keep-alive connection at p50 0.028 ms, p99 0.081 ms; the 2,001 serves landed as `usage_count = 2001` and 2,001 audit rows.
</details>

## 🔐 Encryption (opt-in, plaintext default)

Storage is self-describing. Each value carries a scheme tag, so plaintext and encrypted rows coexist in one database and the mode can be switched on later with no migration.

```
ENCRYPTION_KEY unset (default)   ──►  key_value = "fc-2c7b07f…"      (plaintext)
ENCRYPTION_KEY set (32-byte hex) ──►  key_value = "enc:gcm:9f3a…"   (AES-256-GCM, hex)

read: decrypt iff the value starts with "enc:gcm:"  ·  a tagged value with no key is a hard error
```

The decision lives in one place (`crypto.Sealer`). Callers always receive the plaintext value from the API response and never need the key.

## 🚀 Quick start

```bash
export ADMIN_TOKEN=$(openssl rand -hex 32)
# optional: export ENCRYPTION_KEY=$(openssl rand -hex 32)   # omit for plaintext at rest

docker compose up -d postgres                  # local Postgres on 127.0.0.1:5432
export DATABASE_URL=postgres://keypooler:keypooler@127.0.0.1:5432/keypooler
go build -o keypooler ./cmd/keypooler          # pure Go, no CGO
./keypooler
```

<details>
<summary>📦 Register a tier, add a key, create a scoped consumer</summary>

```bash
A="Authorization: Bearer $ADMIN_TOKEN"

# 1. create a tier with two rate-limited features
curl -X POST localhost:8080/admin/tiers -H "$A" -H 'Content-Type: application/json' -d '{
  "name": "firecrawl",
  "description": "Firecrawl pooled free-tier accounts",
  "features": { "firecrawl_scrape": {"rate_limit":10,"window_seconds":60},
                "firecrawl_search": {"rate_limit":5,"window_seconds":60} }
}'

# 2. add a key with a monthly 1000-credit budget and a bound webhook secret
curl -X POST localhost:8080/admin/keys -H "$A" -H 'Content-Type: application/json' -d '{
  "name": "firecrawl-01", "key": "fc-xxxx", "tier": "firecrawl",
  "usage_limit": 1000, "usage_window_seconds": 2592000,
  "secrets": { "webhook_secret": "whsec_xxxx" }
}'

# 3. create a consumer and scope it to the tier (token is shown ONCE)
CID=$(curl -s -X POST localhost:8080/admin/consumers -H "$A" -H 'Content-Type: application/json' \
  -d '{"name":"siphon-runner","description":"firecrawl via rotation"}' | jq -r .id)
curl -X POST localhost:8080/admin/consumers/$CID/scopes -H "$A" -H 'Content-Type: application/json' \
  -d '{"tier":"firecrawl"}'

# 4. draw a rotated key (consumer token)
curl localhost:8080/key?feature=firecrawl_scrape -H "Authorization: Bearer <consumer-token>"
```
</details>

## 📡 API

<details>
<summary>Endpoints</summary>

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/health` | public | liveness |
| `GET` | `/key?feature=X` | admin **or** consumer | draw a rotated key for a feature |
| `POST` | `/key/{id}/exhausted` | admin **or** consumer | report a key the provider refused; body `{"until": RFC3339}`, the key is skipped until then and serves again on its own |
| `GET` `POST` `PATCH` | `/admin/tiers` | admin | list / create / update tier features |
| `GET` `POST` | `/admin/keys` | admin | list / add keys |
| `DELETE` | `/admin/keys/{id}` | admin | remove a key (+ its secrets) |
| `GET` `POST` | `/admin/consumers` | admin | list / create consumers |
| `DELETE` | `/admin/consumers/{id}` | admin | remove a consumer (+ its scopes) |
| `POST` | `/admin/consumers/{id}/scopes` | admin | grant a tier scope |
| `GET` | `/admin/usage?limit=N` | admin | audit log |
| `GET` | `/admin/health` | admin | pool size |
</details>

## ⚙️ Configuration

<details>
<summary>Environment variables</summary>

| Var | Default | Notes |
|---|---|---|
| `ADMIN_TOKEN` | *(required)* | superuser bearer token |
| `ENCRYPTION_KEY` | *(empty = plaintext)* | 32-byte hex; when set, new writes are encrypted |
| `DATABASE_URL` | *(required)* | `postgres://user:pass@host:5432/db` |
| `DB_MAX_OPEN_CONNS` | `4` | pool size; the serve path uses none, so admin and flushes are all it serves |
| `SERVER_PORT` | `8080` | listen port |
| `SERVER_{READ,WRITE,IDLE,SHUTDOWN}_TIMEOUT_SECONDS` | `30/30/120/30` | HTTP server timeouts |
| `LOG_LEVEL` `LOG_FORMAT` | `info` `json` | logging |
| `LOG_REQUESTS` | `false` | a log line per request; every serve is already a `usage_events` row |
</details>

## 🗂️ Project structure

```
cmd/keypooler/main.go    wires DB, sealer, key pool, HTTP server
internal/
  api/                   handlers, router, middleware, consumer auth
  config/                env config + validation
  crypto/                AES-256-GCM + Sealer (opt-in, self-tagged)
  db/                    Postgres adapter (pgx pool), migrations
  keypool/               round-robin pool, rate + usage budgets
  writeback/             usage counts and audit events, flushed behind the serve
migrations/001_init.sql  consolidated schema
```

## 🚢 Deployment

Runs on **Railway** as service `keypooler` in the `platform` project (Singapore), built from `main`, beside ai-gateway and the shared Postgres it uses (database `keypooler`, reached over the private network). Infrastructure is `orkait/infra` `terraform/platform`.

A schema change is a new `migrations/NNN_name.sql`; an applied version is never re-run.

Tests need a throwaway Postgres (the db tests drop their own tables):

```bash
docker compose up -d postgres
KEYPOOLER_TEST_DATABASE_URL=postgres://keypooler:keypooler@127.0.0.1:5432/keypooler go test -race ./...
```

## 🧰 Tech

Go 1.25 (CGO-free, fully static binary) · Postgres via [pgx](https://github.com/jackc/pgx) (native protocol, cached prepared statements) · AES-256-GCM · zerolog · stdlib `net/http` · distroless runtime image.
