# keypooler

A lightweight HTTP service that manages API keys — rotation, rate limiting, and encrypted storage. It is the single source of truth for credentials in the orkait stack.

```
caller  →  GET /key?feature=image-gen  →  keypooler picks a key  →  returns decrypted value
                                               ↓
                                        round-robin across keys
                                        respects per-tier rate limits
                                        encrypted at rest (AES-256-GCM)
```

Other services (e.g. [pulse](https://github.com/orkait/pulse)) call keypooler to obtain keys. They never store or decrypt credentials themselves.

## What it does

**Keys live in tiers.** A tier defines which features it covers and the rate limit per minute for each feature. A "pro" tier might allow 100 image-gen calls/min; a "free" tier allows 10.

**Round-robin selection.** When a key is requested for a feature, keypooler picks the next available key from that tier's pool, skipping any whose rate budget is exhausted for the current window.

**Encrypted at rest.** Keys are stored encrypted with AES-256-GCM. Decryption happens inside keypooler at the boundary — callers receive the plaintext value directly from the API response and never need the encryption key.

## Quick start

```bash
export ENCRYPTION_KEY=$(openssl rand -hex 32)
export ADMIN_TOKEN=$(openssl rand -hex 32)

mkdir -p data
CGO_ENABLED=1 go build -o keypooler ./cmd/
./keypooler
```

Register a tier and add a key:

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

Fetch a key (from another service):

```bash
curl "localhost:8080/key?feature=image-generation" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
# {"key_id":"...","value":"sk-abc123..."}
```

## API

### Key access (requires `Authorization: Bearer <ADMIN_TOKEN>`)

| Method | Path | What |
|--------|------|------|
| `GET` | `/key?feature=<name>` | get next available key for feature; `429` if none available |

### Admin (requires `Authorization: Bearer <ADMIN_TOKEN>`)

| Method | Path | What |
|--------|------|------|
| `POST` | `/admin/tiers` | create tier with features + rate limits |
| `GET` | `/admin/tiers` | list tiers |
| `POST` | `/admin/keys` | add a key (encrypted at rest) |
| `GET` | `/admin/keys` | list keys with live rate usage |
| `DELETE` | `/admin/keys/{id}` | remove a key |
| `GET` | `/admin/health` | pool size and key counts |

### Public

| Method | Path | What |
|--------|------|------|
| `GET` | `/health` | liveness check |

## Configuration

| Variable | Default | What |
|----------|---------|------|
| `ENCRYPTION_KEY` | *required* | 32-byte hex for AES-256-GCM key storage |
| `ADMIN_TOKEN` | *required* | bearer token for all endpoints |
| `SERVER_PORT` | 8080 | listen port |
| `DB_PATH` | ./data/pool.db | SQLite path |
| `LOG_LEVEL` | info | debug/info/warn/error |
| `LOG_FORMAT` | json | json or pretty |

## Project structure

```
cmd/main.go              → wires DB, key pool, HTTP server
internal/
  api/                   → handlers (GetKey, tiers, keys, health), router, middleware
  config/                → env config with validation
  crypto/                → AES-256-GCM encrypt/decrypt
  db/                    → SQLite adapter, migrations (tiers + keys only)
  keypool/               → round-robin pool, rate-limit tracking
migrations/
  001_create_tiers.sql
  002_create_keys.sql
```

## Tech

Go 1.22 · SQLite (WAL mode) · AES-256-GCM · zerolog · stdlib net/http
