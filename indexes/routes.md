# API Routes

## Public Routes (no authentication)

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/health` | `HealthCheck` | Returns `{"status": "ok"}`, HTTP 200 |
| POST | `/api/requests` | `SubmitRequest` | Create and enqueue API request |
| GET | `/api/requests/{id}` | `GetRequestStatus` | Get request status by ID |

## Admin Routes (require `Authorization: Bearer <ADMIN_TOKEN>`)

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/admin/keys` | `ListKeys` | List all API keys (redacted) |
| POST | `/admin/keys` | `AddKey` | Create new API key (encrypted at rest) |
| DELETE | `/admin/keys/{id}` | `DeleteKey` | Remove API key from pool |
| PUT | `/admin/keys/{id}/weight` | `UpdateKeyWeight` | Update key weight for WRR |
| POST | `/admin/keys/{id}/reset` | `ResetKeyCircuit` | Reset circuit breaker to closed |
| GET | `/admin/health` | `PoolHealth` | Pool size, queue size, per-key health |
| GET | `/admin/config` | `GetConfig` | Get system_config values |
| PUT | `/admin/config` | `UpdateConfig` | Update system_config values |

## Metrics

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/metrics` | `metrics.Handler()` | Prometheus text format (if METRICS_ENABLED) |

## Middleware Chain

```
All requests:  RequestLogger -> ServeMux
Admin routes:  RequestLogger -> AdminAuth(ADMIN_TOKEN) -> Handler
```

## Request/Response Schemas

### POST /api/requests

**Request Body:**
```json
{
  "idempotency_key": "optional-dedup-key",
  "source": "manual|cron|code_snippet",
  "priority": 1,
  "method": "POST",
  "url": "https://api.example.com/endpoint",
  "headers": {"X-Custom": "value"},
  "payload": "{\"key\": \"value\"}"
}
```
- `method` and `url` are required
- `priority`: 1 (high), 2 (normal, default), 3 (low)
- Returns HTTP 202 Accepted

**Response (202):**
```json
{
  "id": "uuid",
  "source": "manual",
  "priority": 2,
  "method": "POST",
  "url": "https://...",
  "status": "pending",
  "created_at": "2024-01-01T00:00:00Z"
}
```

### POST /admin/keys

**Request Body:**
```json
{
  "name": "key-name",
  "key": "sk-actual-api-key-value",
  "weight": 1,
  "rate_limit_per_minute": 60,
  "rate_limit_per_day": 10000,
  "concurrent_limit": 5
}
```

### GET /admin/health

**Response:**
```json
{
  "pool_size": 3,
  "queue_size": 12,
  "keys": [
    {
      "id": "uuid",
      "name": "key-1",
      "circuit_state": "closed",
      "failure_count": 0,
      "current_concurrent": 2,
      "concurrent_limit": 5,
      "weight": 1
    }
  ]
}
```

### PUT /admin/config

**Request Body:**
```json
{
  "worker_count": "15",
  "strategy": "weighted_round_robin"
}
```

## HTTP Status Handling (downstream responses)

| Status Range | Action | Key Impact |
|-------------|--------|------------|
| 200-299 | Success, persist result | Mark healthy |
| 429 | Retryable failure | Mark failed (circuit breaker) |
| 500+ | Retryable failure | Mark failed (circuit breaker) |
| Other 4xx | Permanent failure, no retry | Key stays healthy |
