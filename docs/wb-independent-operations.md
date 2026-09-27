# WA-D163 — seller-info is not an operation gateway

@NO_COMPRESS @PRESERVE

Supersedes identity expiry/fallback preflight and WA-D162 read-only `/wb stocks` UX.
One executable/process, same in-memory MCP/client/limiter/scheduler. No new endpoints.

## Audit before change
- Telegram `wbButton(check)` -> Service.Start(check) -> RefreshIdentity -> identity -> Client.Seller -> common seller-info HTTP.
- Telegram sync -> Service.Start -> identityValid -> identity if absent/untrusted; catalog/stocks/orders become dependency failures on identity error.
- Service fake/legacy fallback used a 24h RAM cache; restart invalidated it. Production already used fingerprint-scoped SQLite identity.
- MCP prices/SELLER/WB -> invoke -> EnsureSeller -> Seller (cached in production, network when missing); adapters without EnsureSeller called Seller on every read.
- Run Now / scheduler WB_DAILY_SYNC -> pipeline -> same MCP preflight.
- Status and v0.19.1 `/wb stocks` were saved-data views, not HTTP callers.
- No automatic startup seller-info or separate heartbeat was found. Explicit wb_get_seller remains a profile tool.
- seller identity is required locally for tenant/token binding, not as an HTTP parameter for prices/stocks/orders.

## Contract after change
- EnsureSeller checks only cached fingerprint-bound identity and local guard. It never calls Seller/HTTP. Missing/mismatched identity fails safely; initialize binding explicitly.
- Service initializes identity only when absent or token fingerprint changed; this is the explicitly permitted onboarding/token-rotation case. Existing binding never expires by wall clock/restart/source403.
- A network identity mismatch cannot silently attach another seller. Workshop permissions, active workshop, connection enabled/revision, grants and save guards remain.
- `/wb check` is explicitly labelled "Обновить информацию о кабинете", not health. It forces profile refresh, respecting common cooldown. `/wb` reads saved operation states.
- Profiles reuse marketplace_connections(workshop_id,seller_id,seller_name,checked_at,identity_fingerprint). No token stored. checked_at is successful fetch/update time. Restart reads SQLite; failed refresh preserves last profile.
- Existing marketplace_cooldowns and rate observations/trace persist independently by group. No reset/bypass of common cooldown. HTTP403 does not retry or block other groups.
- Caller for explicit refresh: manual_seller_info_refresh. Existing Telegram/background/run_now/tool caller trace remains. No secrets or raw payloads.

## Call graphs
`/wb stocks -> Service.Start(stocks) -> existing MCP Client -> in-memory MCP -> wb_get_seller_stocks / wb_get_wb_stocks -> same WB Client -> independent endpoints -> source snapshots -> combined UI`

`08:00 / Run Now -> existing BackgroundScheduler/Service -> WB_DAILY_SYNC -> WB_MARKET_SYNC_PIPELINE -> prices / SELLER / WB MCP tools -> compute -> save`

No seller-info HTTP for either graph with an existing credential binding. SELLER still needs Content catalog variants, a real source dependency, and its own Marketplace quota.

## State and UI
marketplace_sync stores separate current manual attempts; marketplace_stock_runs stores source outcomes; background_job_runs.result_json stores each pipeline step and timestamps. Aggregate additionally preserves explicit price_status, seller_stock, wb_stock statuses.
`/wb stocks` starts both sources asynchronously and sends completion after permissions recheck. `/wb seller_stocks`, `/wb wb_stocks` and paginated views remain saved-data reads.
Historical pre-migration counters are UNKNOWN, not zero. A newer running/failed attempt is identified separately from the saved snapshot. Status includes cached profile timestamp and latest pipeline source steps.
Prices SUCCESS + SELLER SUCCESS + WB PERMISSION_DENIED => PARTIAL_SUCCESS; available data saved. Existing 34/33/1 partial acceptance remains. Missing/unavailable never zeroes last known rows. No new migration or production DB mutation during development.

## Verification
- TestSellerCooldownDoesNotGateIndependentReads: real WB Client with HTTP stub, active common cooldown, four independent HTTP paths; zero seller-info requests; explicit profile refresh blocked locally.
- TestDailySyncWithSellerInfoCooldownDoesNotCallSeller: real in-memory MCP, Run Now + next-day Tick, zero Seller calls.
- TestStocksJobIgnoresSellerInfoFailureWithPersistentProfile: failed profile refresh followed by both successful stock sources.
- TestWBStocksStartsBothSourcesDuringSellerCooldown: Telegram command uses both source reads and sends saved result.
- TestHistoricalStockCountersAreUnknown: legacy partial rows do not show fabricated received=0.
- Existing identity rotation, cache restart, cooldown persistence, authorization, source403/partial/missing and rollback tests retained; expiry expectations updated to persistent binding contract.

Final command results are recorded in marketplace/WORKSTATE.md. All tests use fixtures, no .env reads/live API/Telegram/production DB writes.
