# Database Schema & Migrations

## Migration System

File-based migrations in `migrations/` directory. Format: `NNN_description.sql`.
Tracked in `schema_migrations` table (version PK, name, applied_at).
Each migration runs in a transaction. Idempotent (skips already-applied versions).

## Tables

### api_keys (Migration 001)

| Column | Type | Default | Description |
|--------|------|---------|-------------|
| id | TEXT | PK | UUID identifier |
| name | TEXT | NOT NULL | Human-readable key name |
| key_encrypted | TEXT | NOT NULL | AES-256-GCM encrypted API key (hex) |
| weight | INTEGER | 1 | Weight for weighted round-robin |
| is_healthy | INTEGER | 1 | Boolean (0/1) for circuit breaker |
| failure_count | INTEGER | 0 | Consecutive failures count |
| circuit_state | TEXT | 'closed' | closed / open / half_open |
| last_used_at | TIMESTAMP | NULL | Last request timestamp |
| rate_limit_per_minute | INTEGER | 60 | Max requests per minute |
| rate_limit_per_day | INTEGER | 10000 | Max requests per day |
| concurrent_limit | INTEGER | 5 | Max concurrent requests |
| current_concurrent | INTEGER | 0 | Current concurrent count |
| created_at | TIMESTAMP | CURRENT_TIMESTAMP | |
| updated_at | TIMESTAMP | CURRENT_TIMESTAMP | |

### requests (Migration 002)

| Column | Type | Default | Description |
|--------|------|---------|-------------|
| id | TEXT | PK | UUID identifier |
| idempotency_key | TEXT | NULL, UNIQUE | Deduplication key |
| source | TEXT | NOT NULL | cron / manual / code_snippet |
| priority | INTEGER | 2 | 1=High, 2=Normal, 3=Low |
| method | TEXT | 'POST' | HTTP method |
| destination_url | TEXT | NOT NULL | Target API URL |
| headers | TEXT | NULL | JSON string of headers |
| payload | TEXT | NULL | JSON string of body |
| status | TEXT | 'pending' | pending / processing / success / failed / queued |
| assigned_key_id | TEXT | NULL | FK to api_keys.id |
| attempts | INTEGER | 0 | Total attempt count |
| last_error | TEXT | NULL | Last error message |
| response_status | INTEGER | NULL | HTTP response code |
| response_body | TEXT | NULL | Response content |
| created_at | TIMESTAMP | CURRENT_TIMESTAMP | |
| updated_at | TIMESTAMP | CURRENT_TIMESTAMP | |
| completed_at | TIMESTAMP | NULL | Completion timestamp |

**Indexes:**
- `idx_requests_status` ON (status)
- `idx_requests_idempotency` ON (idempotency_key)
- `idx_requests_created` ON (created_at)

### key_events (Migration 003)

| Column | Type | Default | Description |
|--------|------|---------|-------------|
| id | TEXT | PK | UUID identifier |
| key_id | TEXT | NOT NULL | FK to api_keys.id |
| event_type | TEXT | NOT NULL | created / deleted / circuit_opened / circuit_closed / circuit_half_opened / rate_limited / key_failed / key_recovered |
| message | TEXT | NULL | Event description |
| created_at | TIMESTAMP | CURRENT_TIMESTAMP | |

**Indexes:**
- `idx_key_events_key_id` ON (key_id)

### system_config (Migration 004)

| Column | Type | Default | Description |
|--------|------|---------|-------------|
| key | TEXT | PK | Config key name |
| value | TEXT | NOT NULL | Config value |
| description | TEXT | NULL | Documentation |
| updated_at | TIMESTAMP | CURRENT_TIMESTAMP | |

### Default Config Values (Migration 005, INSERT OR IGNORE)

| Key | Value | Description |
|-----|-------|-------------|
| worker_count | 10 | Concurrent worker goroutines |
| queue_max_size | 1000 | Maximum pending requests |
| strategy | round_robin | Key selection strategy |
| circuit_breaker_threshold | 3 | Failures before circuit opens |
| circuit_breaker_open_duration_seconds | 60 | Duration before half-open probe |
| retry_max_attempts | 4 | Max total attempts (1 + 3 retries) |
| retry_base_delay_ms | 1000 | Base delay for exponential backoff |
| retry_max_delay_ms | 30000 | Maximum retry delay |
| load_shed_level1 | 0.50 | Healthy ratio threshold for low-priority rejection |
| load_shed_level2 | 0.25 | Healthy ratio threshold for normal-priority rejection |

### schema_migrations (auto-created)

| Column | Type | Default | Description |
|--------|------|---------|-------------|
| version | INTEGER | PK | Migration version number |
| name | TEXT | NOT NULL | Migration description |
| applied_at | TIMESTAMP | CURRENT_TIMESTAMP | When applied |

## SQLite Configuration

- **Journal Mode:** WAL (Write-Ahead Logging) for concurrent reads
- **Max Open Conns:** 1 (SQLite single-writer constraint)
- **Max Idle Conns:** 1
- **Busy Timeout:** Configurable (default 5000ms)
- **Connection Lifetime:** Unlimited (0)
