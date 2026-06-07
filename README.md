# keypooler

One source of truth for API keys in the orkait stack: round-robin rotation, per-feature rate limits, monthly usage budgets, scoped consumers, bound secrets, and opt-in encryption at rest.

Live: https://keypooler.orkait.com (Railway, project `orkait/keypooler`, Turso libSQL DB in `orkait-eu`/Ireland).

Other services call keypooler to get a usable key for a feature. They never store, rotate, or decrypt credentials themselves.

```
GET /key?feature=firecrawl_scrape  ->  { value, secrets, metadata }
   scope-filters to the caller's tiers, round-robins the pool,
   enforces rate + usage budgets, audits the serve.
```

## What it does

- Rotation: round-robin across a tier's keys; exhausted keys are skipped.
- Rate limits: per-feature, windowed (e.g. 10 calls / 60s), defined on the tier.
- Usage budgets: per-key `usage_limit` with optional `usage_window_seconds` (e.g. 2592000 = monthly auto-reset); no window = lifetime cap.
- Scoped consumers: each client gets a bearer token scoped to specific tiers. The admin token is a superuser.
- Bound secrets: named secrets travel with a key (e.g. a `webhook_secret`), returned at serve time.
- Opt-in encryption: plaintext at rest by default; set `ENCRYPTION_KEY` to encrypt new writes (self-tagged, so plaintext and encrypted rows coexist).
- Audit: every serve appends a `usage_events` row.

## Auth

`/key` accepts the admin token (any tier) or a consumer token (only its scoped tiers; `401` unknown, `403` out-of-scope). All `/admin/*` routes are admin-only.

## Quick start

```bash
export ADMIN_TOKEN=$(openssl rand -hex 32)
# optional: export ENCRYPTION_KEY=$(openssl rand -hex 32)   # omit for plaintext

go build -o keypooler ./cmd/keypooler          # pure Go, no CGO
./keypooler                                    # local SQLite at ./data/pool.db
# or: DATABASE_URL="libsql://<host>?authToken=<jwt>" ./keypooler   # Turso
```

```bash
A="Authorization: Bearer $ADMIN_TOKEN"

# create a tier
curl -X POST localhost:8080/admin/tiers -H "$A" -d '{"name":"firecrawl","description":"pooled accounts",
  "features":{"firecrawl_scrape":{"rate_limit":10,"window_seconds":60}}}'

# add a key (monthly 1000-credit budget + bound secret)
curl -X POST localhost:8080/admin/keys -H "$A" -d '{"name":"firecrawl-01","key":"fc-xxxx","tier":"firecrawl",
  "usage_limit":1000,"usage_window_seconds":2592000,"secrets":{"webhook_secret":"whsec_xxxx"}}'

# create + scope a consumer (token shown once), then draw a key
CID=$(curl -s -X POST localhost:8080/admin/consumers -H "$A" -d '{"name":"siphon-runner"}' | jq -r .id)
curl -X POST localhost:8080/admin/consumers/$CID/scopes -H "$A" -d '{"tier":"firecrawl"}'
curl localhost:8080/key?feature=firecrawl_scrape -H "Authorization: Bearer <consumer-token>"
```

## API

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/health` | public | liveness |
| GET | `/key?feature=X` | admin or consumer | draw a rotated key |
| GET POST PATCH | `/admin/tiers` | admin | list / create / update tiers |
| GET POST | `/admin/keys` | admin | list / add keys |
| DELETE | `/admin/keys/{id}` | admin | remove a key |
| GET POST | `/admin/consumers` | admin | list / create consumers |
| DELETE | `/admin/consumers/{id}` | admin | remove a consumer |
| POST | `/admin/consumers/{id}/scopes` | admin | grant a tier scope |
| GET | `/admin/usage?limit=N` | admin | audit log |
| GET | `/admin/health` | admin | pool size |

## Configuration

| Var | Default | Notes |
|---|---|---|
| `ADMIN_TOKEN` | required | superuser bearer token |
| `ENCRYPTION_KEY` | empty = plaintext | 32-byte hex; when set, new writes are encrypted |
| `DATABASE_URL` | empty | `libsql://…` Turso URL; set = Turso, unset = local SQLite |
| `DB_PATH` | `./data/pool.db` | local SQLite path |
| `DB_MAX_OPEN_CONNS` | `1` | local SQLite must be 1; a remote pool may be higher |
| `SERVER_PORT` | `8080` | listen port |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `json` | logging |

## Project structure

```
cmd/keypooler/main.go    entrypoint: wires DB, sealer, pool, HTTP server
internal/
  api/                   handlers, router, middleware, consumer auth
  config/                env config + validation
  crypto/                AES-256-GCM + Sealer (opt-in, self-tagged)
  db/                    libSQL (Turso) + local SQLite adapter, migrations
  keypool/               round-robin pool, rate + usage budgets
  util/                  db context helpers
migrations/001_init.sql  consolidated schema
```

## Deploy

```bash
railway up --service keypooler --detach     # from repo root
```

Editing `migrations/001_init.sql` does not re-migrate an existing DB (the version stays `1`). To apply a schema change, wipe the database first so the migration re-runs.

## Tech

Go 1.25 (CGO-free, static binary) · Turso/libSQL + modernc.org/sqlite (both pure Go) · AES-256-GCM · zerolog · stdlib net/http · distroless image.
