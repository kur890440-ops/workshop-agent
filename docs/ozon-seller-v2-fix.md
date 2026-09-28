# WA-D170 — mandatory cached SKU filter for Seller/FBS v2

@PROJECT:WORKSHOP_AGENT @NO_COMPRESS @PRESERVE

2026-09-28, task3e3bb18b-1215-443c-9f80-1da3b3c4fc89.
L0: retain v2 warehouse breakdown, source OZON_SELLER_STOCK, existing MCP/client/
process/EXE; no v4 fallback. L1: existing SQLite catalog SKU relation supplies
identifiers; no implicit catalog/network lookup. Existing stock keys remain
connection/source/SKU/warehouse. L2 WA-D170 supersedes the unfiltered seller
request admitted by WA-D167/168. User supplied actual400 explicitly requires sku
or offer_id. Use sku[] strings, not product_id; positive/deduplicated batches100
(existing conservative application cap, NOT a verified maximum Ozon quota).
Official docs fetch still fails with redirect loop; no guessed new API fields.
Each batch starts fresh cursor, every continuation repeats same SKU filter.
Empty cache -> unavailable with0 HTTP. Missing catalog identifiers -> partial,
never unknown positions converted to0. Keep current-state preservation policy.
Extend existing error diagnostics to non-2xx, bounded sanitized bodies; error
details stay owner-only, not LLM/tool results. Existing retry policy unchanged.
L3: upstream reread and live error evidence -> implement builder/metrics/errors/
presentation -> mock serializer/batch/pages/persistence/MCP/regressions -> EXE.
No .env read or production DB mutation. No inherited Ozon env credentials found;
conditional live test cannot run in this session without reading .env.


## Contract evidence and comparison

Pinned source: security-audit/ozon-mcp/source, commit
b95da59689cdacb544c659c5c0c1fcbecc50a995.
Python: ozon_mcp/client.py:582 `product_stocks_by_warehouse`.
MCP: ozon_mcp/server.py:561 `ozon_product_stocks_by_warehouse`;
dispatch at1253. Tool input: optional `skus` integer array and optional `limit`
integer default100; no required array. Python method additionally accepts cursor;
MCP dispatch does not expose/pass it. Method returns raw JSON: no typed response
parser or dedicated success fixtures/tests confirm response fields at this commit.
Existing Go response model retained, fixtures are regression fixtures, not live
API conformance proof. Official documentation could not be loaded (redirect loop).

| Property | Upstream Python | Go before fix | Match |
|---|---|---|---|
| Method/path | POST /v2/product/info/stocks-by-warehouse/fbs | same | yes |
| Root | JSON object | object | yes |
| Input vs JSON field | skus -> sku | SKUs -> sku, unused by fetch | builder mismatch |
| Type/batch | integer input -> array of decimal strings | typed string array, omitted | missing actual filter |
| offer_id/offer_ids | neither supported by this method | neither sent | yes |
| limit | integer100 default | integer100 | yes |
| cursor | optional nonempty string | optional string | yes |
| Empty/null/omitempty | omit sku when list empty | omit empty sku | unsafe for actual API |
| Zero values | no positive-ID validation in reference | no filter in fetch | now reject <=0 |
| Maximum batch | not specified | local cap100 | not verified API maximum |

Actual reported HTTP400 request before: `{"limit":100}`.
Actual reported Ozon response:
`{"code":3,"message":"Request validation error: one of 'sku' or 'offer_id' must be specified"}`.
Cause: missing mandatory filter; not product_id accidentally substituted for SKU.
Current Go implementation: internal/marketplace/ozon/request.go SellerStocksRequest,
SellerStocksPage; fetch.go seller/sellerBatch; service.go Execute/skus.
After (synthetic example): `{"sku":["91001","91002"],"limit":100}`.
Next page: same sku array plus `"cursor":"next"`; new batch resets cursor.
No `skus`, `offer_ids`, nested filter or v4 fallback added.

## Catalog and verification evidence

Read-only SQLite inspection, connection2/workshop6:25 catalog products;
25 have SKU,25 have nonempty offer_id,0 empty offers;27 SKU records,
27 unique positive identifiers,0 invalid/zero identifiers,0 duplicate SKU records.
No production data modified by this task.

TestSellerCachedCatalogRefreshPreservesLastGood seeds a temporary SQLite catalog
with25 products/27 identifiers and executes existing Service. Mock HTTP200:
CACHE HIT, products_considered25, identifiers_valid27, identifiers_invalid0,
batches1, batch size27, HTTP requests1, received2, valid2, saved2, warehouses2.
A second explicit refresh receives mock400 once; current quantities remain8+4.
TestSellerFilterBatchPaginationAndNoNPlusOne:101 unique identifiers,2 batches,
3 HTTP requests including continuation, same filter on each page, no catalog API.
Empty input ->0HTTP. Invalid/missing quantity, genuine zero, malformed pagination,
partial persistence and actual SDK MCP tests remain covered by the test suite.

Sanitized synthetic trace (not a real Ozon response):
```json
{"operation":"seller_stocks","endpoint":"/v2/product/info/stocks-by-warehouse/fbs","http_status":400,"batch":1,"batches":1,"identifier_count":2,"request":{"sku":["91001","91002"],"limit":100},"response":{"code":3,"message":"fixture bad request","request_id":"fixture-id"},"attempt":1}
```
Owner-only diagnostics retain bounded sanitized request/error JSON, operation,
endpoint/status, code/message/details/request_id/trace_id. Existing credential
redaction also covers escaped/numeric secret values; headers are not dumped.
All non-2xx covered;400 has no retry, existing429/5xx behavior preserved.
Logical SYNC trace contains refresh_metrics, including committed row count;
HTTP traces contain batch/identifier counts. Raw errors never become LLM input.

Telegram example from the mock service test:
```
Ozon - Seller stocks (localized Russian heading in application)
Product A
Warehouse A - 8
Warehouse B - 4
Total - 12
```
Names use cached catalog/response metadata, fallback to IDs when absent. Storage
keeps both warehouse rows. Incomplete pages show a confirmed-page subtotal only;
missing/stale rows and failed refresh are not converted to zero. Snapshot times
and last successful refresh remain visible.

No real READ test: inherited Ozon credentials absent; .env intentionally unread.
Therefore production HTTP200/schema compatibility is not claimed. Restart the
canonical EXE and explicitly run `/ozon refresh seller`; then read
`/ozon seller_stocks`. Existing Day20 routing reads this same source/tool/cache.
No new endpoint/tool/client/server/process/scheduler or v4 fallback.

## Final validation

go test ./... exit0:20 packages,406 test pass events,0 failures.
Canonical bin/workshop-agent.exe rebuilt, --version WorkshopAgent v0.21.1.
git diff --check exit0. Evidence: [test output](../reports/ozon-seller-v2-fix/tests.jsonl), [mock refresh](../reports/ozon-seller-v2-fix/mock-refresh.txt), [build](../reports/ozon-seller-v2-fix/build.txt).
No running application was stopped or launched; production API not exercised.
