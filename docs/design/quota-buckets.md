# Quota buckets: accounts and scopes in keypooler

Keypooler treats a key as the unit of quota, but providers do not. Google meters the Gemini free tier per project and per model (`GenerateRequestsPerDayPerProjectPerModel-FreeTier`, 500 a day for `gemini-3.5-flash-lite`), several keys can belong to one project, and one key spans several models with separate limits. On 15 and 16 October this made keypooler park whole keys when one model ran out, forced a second tier holding copies of the same keys (`gemini_flash`) to keep Flash serving, and left dead keys in rotation until they were disabled by hand in the database. This design adds two provider-neutral concepts, the **account** (what owns a quota) and the **scope** (what the quota is spent on, named by the caller), and moves every provider rule into ai-gateway.

## What happened

| # | Observation | Evidence |
|---|---|---|
| 1 | A key's daily quota is per model, not per key | Gemini 429 body: `quotaId: GenerateRequestsPerDayPerProjectPerModel-FreeTier`, `quotaValue: 500`, `quotaDimensions.model: gemini-3.5-flash-lite`, `retryDelay: 62565s` at 06:37:04 UTC (reset at 00:00 UTC) |
| 2 | One exhausted model parked the key for every model | `exhausted_until` is a column on `keys`; ai-gateway calls `POST /key/{id}/exhausted` with no model |
| 3 | Separate tiers were the only way to separate models | tier `gemini_flash` holds copies of the six `gemini` keys; ai-gateway #41 added `Model.Pool` to draw from it |
| 4 | Dead keys stayed in rotation | Google 401 `ACCOUNT_STATE_INVALID` ("bound service account is deleted or disabled") on `gemini-03/04/05`, 403 "project has been denied access" on four more; each was redrawn and refused until `is_active` was set in Postgres by hand |
| 5 | Provider-specific cool-downs were lost | Google puts the wait in the body (`RetryInfo.retryDelay`, 17 h); ai-gateway reads only the `Retry-After` header and re-parked the keys for about 30 minutes, then retried |
| 6 | Round-robin spreads draws but not what is left | draws per key were even (20, 21, 26, 21, 21, 24 between 20:45 and 20:52), yet two projects hit their daily limit while another still answered |

## Goals and non-goals

| Goals | Non-goals |
|---|---|
| Quota state per account and scope, so one model's limit leaves the account's other models serving | Any provider knowledge in keypooler: status codes, error bodies, model names, reset times are ai-gateway's |
| Keys that share a quota (one project, several keys) share its state | Billing, prices or spend; `spend_events` and budgets stay as they are |
| Disable and park as API calls with a reason, never a database edit | A UI |
| Budgets known before a provider refuses: a count per window that resets at a set time | Predicting provider limits; the caller declares them |
| Draw the bucket with the most budget left, so accounts drain evenly | Changing the consumer and scope (`consumer_scopes`) model |
| Backward compatible: a draw without a scope behaves as today | |

## Concepts

| Concept | Meaning | Who sets it |
|---|---|---|
| **Account** | Owns a quota. Every key belongs to one account; keys of one account share its buckets. For Gemini an account is a Google project | admin, when adding keys |
| **Scope** | An opaque string naming what a draw spends: ai-gateway passes the model name. Keypooler never interprets it; an empty scope is the account's default bucket | the caller, per draw |
| **Bucket** | One (account, feature, scope). Holds `parked_until`, `park_reason`, `used`, `window_start` | created on first draw |
| **Budget** | A count per window with a reset anchor, for a tier feature and optionally one scope: "500 a day per bucket, resetting at 00:00 UTC" | admin, per tier feature |
| **Key state** | `active`, or `disabled` with a reason; parking moves to buckets | the caller (`disable`) or admin (`enable`) |

A draw for (feature, scope) is eligible on a key when the key is active, its tier serves the feature, the consumer may use the tier, and the bucket (key's account, feature, scope) is neither parked nor over budget. Among eligible keys, keypooler picks the one whose bucket has the most budget left, then the least recently drawn, so every account drains at the same pace.

## Data model

| Change | Detail |
|---|---|
| `accounts (id, tier_id, name, created_at)` | new; unique (tier_id, name) |
| `keys.account_id` | new, references `accounts`; migration creates one account per key from `metadata_json.account`, so keys with the same account name share one |
| `keys.disabled_reason`, `keys.disabled_at` | new; `is_active = false` keeps its meaning |
| `buckets (account_id, feature, scope, parked_until, park_reason, used, window_start)` | new; primary key (account_id, feature, scope) |
| `tier_budgets (tier_id, feature, scope, limit_count, window_seconds, reset_anchor)` | new; empty `scope` is the default for every scope; no row means no budget |
| `keys.exhausted_until` | kept for one release as the bucket of the empty scope, then dropped |

## API

| Call | Behaviour |
|---|---|
| `GET /key?feature=f&scope=s` | draws as above; `scope` optional (empty scope = today's behaviour). The response adds `account` |
| `POST /key/{id}/park {scope, until, reason}` | parks the bucket (key's account, feature of the draw, scope) for every key of the account; empty scope parks the default bucket |
| `POST /key/{id}/disable {reason}` | sets the key inactive with the reason; the next draw skips it |
| `POST /admin/keys/{id}/enable` | reactivates a disabled key |
| `POST /key/{id}/exhausted {until}` | kept as `park` with an empty scope until callers move |
| `GET /admin/buckets?tier=t` | every bucket: account, feature, scope, used, budget, window reset, parked until and why |
| `PUT /admin/tiers/{name}/budgets` | sets budgets per feature and scope |

## ai-gateway

ai-gateway keeps all provider knowledge and speaks only the verbs above.

| Change | Detail |
|---|---|
| Draw with the model as scope | `Draw(ctx, feature, scope = model.Name)` |
| Classify each refusal per provider | dead account (401 `ACCOUNT_STATE_INVALID`, 403 project denied, invalid key) → `disable(reason)`; billing (402) → `park(scope = "", far future, reason)`; daily or minute quota (429) → `park(scope = model, until = RetryInfo.retryDelay, else Retry-After, else default)` |
| Remove the tier workaround | drop `Model.Pool` and route Flash and Flash-Lite through one `gemini` tier; buckets keep their quotas apart |

## Options considered

| Option | Separates models | Shared-project keys | Provider rules live in | Verdict |
|---|---|---|---|---|
| A. A tier per model family (today) | per family only, by copying keys | no | ai-gateway, but state is per key copy | stop-gap; copies drift and double the admin work |
| B. Account and scope buckets in keypooler | yes, any scope the caller names | yes | ai-gateway | **chosen** |
| C. ai-gateway tracks quota in memory | yes | yes | ai-gateway | rejected: state is lost on deploy and not shared between instances, and keypooler already owns key state |

## Migration and rollout

| Step | Action | Check |
|---|---|---|
| 1 | Migration adds `accounts`, `buckets`, `tier_budgets`, the key columns; backfills `account_id` | every key has an account; Gemini keys `gemini-02..12` get accounts `gemini-02..12` |
| 2 | Deploy keypooler; draws without a scope unchanged | ai-gateway #41 keeps working untouched |
| 3 | Set the Gemini budget: `gemini` feature, 500 per 86 400 s, reset 00:00 UTC | `GET /admin/buckets?tier=gemini` shows budgets |
| 4 | Deploy ai-gateway drawing with scope and classifying refusals | a 429 parks only (account, model) until its `retryDelay`; a 401 disables the key |
| 5 | Route Flash through `gemini`, delete tier `gemini_flash` and its six copies, remove `Model.Pool` | `watch` still serves both families; one key per account |

## Open questions

| Question | How to settle it |
|---|---|
| Is every Gemini model its own 500-a-day bucket, or do models of one family share one? The quota id says per model | one request per model on a key whose `gemini-3.5-flash-lite` quota is spent; a 200 on `gemini-3.1-flash-lite` confirms per model |
| Free-tier per-minute limits per model | read `quotaId` on the first per-minute 429 and declare it as a second budget |
| Do the AQ keys belong to distinct projects? | the 429 and 401 bodies do not carry a project id; ask the key owner, or compare quotas: two keys of one project exhaust together |

## Verification

Commands run on 15 and 16 October while investigating; no code exists yet.

| Command | Result |
|---|---|
| `curl generativelanguage.googleapis.com/v1beta/models/gemini-flash-lite-latest:generateContent` with each active key | 429 `GenerateRequestsPerDayPerProjectPerModel-FreeTier`, limit 500, `retryDelay 62565s` on `gemini-02` and `gemini-07`; 401 `ACCOUNT_STATE_INVALID` on `gemini-03/04/05`; 200 on `gemini-06` |
| `GET /admin/usage?limit=1000`, grouped by key and feature | even round-robin draws across the six keys; every draw of a dead key followed by an `exhausted` event |
| `railway logs --service ai-gateway`, refusals grouped by account | 37 of the last 40 Gemini refusals were 401, 3 were 429 |
| `psql keypooler: update keys set is_active = false ...` | the only way to disable a key today (`UPDATE 5`, `UPDATE 2`, `UPDATE 6`) |
