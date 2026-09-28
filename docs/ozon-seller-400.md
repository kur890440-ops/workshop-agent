# Ozon Seller Stocks HTTP 400 — WA-D168

@PROJECT:WORKSHOP_AGENT @NO_COMPRESS @PRESERVE

## Scope / decision

2026-09-28. Targeted audit requested in attachment 89bea558-db64-4d41-8879-89fdf487fd3f.
Keep one executable/process, existing in-memory MCP, read-only operations and current
endpoint. No scheduler, N+1, endpoint switch or production DB mutation.
WA-D168 adds bounded, sanitized HTTP 400 diagnostics to the existing owner-only
trace. It is an explicit exception to WA-D167's metadata-only trace: no headers,
credentials, success payloads or raw unbounded errors may be stored. Diagnostic
payloads must not enter tool results, agent memory or LLM requests.

## Findings before changes

Audited checkout: security-audit/ozon-mcp/source, HEAD
`b95da59689cdacb544c659c5c0c1fcbecc50a995`.
`ozon_mcp/client.py:582`, OzonSellerClient.product_stocks_by_warehouse:
body starts with limit; nonempty skus become sku array of decimal strings;
nonempty cursor is added. Default request is exactly `{"limit":100}`.
`ozon_mcp/server.py:561` registers optional integer-array skus and limit100;
dispatch at1253 does not forward cursor. Client itself has no pagination loop,
returns generic JSON. No endpoint-specific tests/fixtures found in upstream.
No maximum batch size or API required fields established by this code.

Go: request.go SellerStocksRequest / SellerStocksPage, fetch.go Client.seller.
First request is also `{"limit":100}`. No identifier is sent; catalog SKU parameter
is ignored for SellerSource. This is not product_id/offer_id substituted for sku.
Do not invent a mismatch or make SKU mandatory without evidence.
Upstream comment says v1 retires 2026-04-07, but no v1 compatibility implementation
or verified v1/v2 schema diff was found.

Official documentation fetch on2026-09-28 failed with redirect loop. Upstream
reference equivalence is not proof of the current Ozon API schema.

## Live test boundary

No inherited OZON credentials
available in agent process (names-only check). Real .env remains unread.
Live test cannot run through this environment without obtaining credentials;
do not start full bot or its jobs just to probe. Actual cause of historical400
cannot be reconstructed because its body was discarded.

## Exact request evidence

The two methods were extracted with Python ast from the pinned file and executed
with a capture-only `_post` stub. No imports of upstream runtime, credentials or
network requests. Returned endpoint/body pairs:

```json
{"endpoint":"/v2/product/info/stocks-by-warehouse/fbs","body":{"limit":100}}
{"endpoint":"/v2/product/info/stocks-by-warehouse/fbs","body":{"limit":100,"sku":["91001","91002"]}}
{"endpoint":"/v2/product/info/stocks-by-warehouse/fbs","body":{"limit":25,"sku":["91001"],"cursor":"next-page"}}
{"endpoint":"/v4/product/info/stocks","body":{"limit":100,"last_id":"","filter":{"product_id":[71001,71002]}}}
```

Values above are synthetic; only structure is captured from upstream. Empty skus
and cursor also produce `{"limit":100}`. Tool schema has no required fields;
limit always exists in constructed body, API-requiredness is NOT established.

## Field comparison

| Field | Upstream Python | Go before/after this patch | Match |
|---|---|---|---|
| Endpoint | /v2/product/info/stocks-by-warehouse/fbs | same | yes |
| Method | POST | POST | yes |
| Content-Type | httpx json=, application/json | explicit application/json | yes |
| Client-Id / Api-Key | credential headers | credential headers | yes; values excluded |
| Root | JSON object | JSON object | yes |
| sku vs skus | tool skus -> HTTP sku | SKUs json:"sku,omitempty" | HTTP yes |
| SKU wire type | list of strings via str(int) | []string | yes |
| Scalar SKU | never | never | yes |
| limit | defaults100, no method bounds | required1..100 | default yes; Go stricter |
| cursor | omitted empty; method accepts | omitted empty; capped2048 bytes | HTTP yes; validation stricter |
| filter / warehouse parameter | absent | absent | yes |
| Empty SKUs | omitted | omitted | yes |
| Zero / duplicate SKUs | forwarded if nonempty list | rejected before HTTP | Go stricter |
| Batch maximum | not specified/validated | local cap100 | not an established API limit |
| Pagination | method returns one dict; tool never forwards cursor | has_next/cursor loop, max100pages, repetition guard | different |
| Response parsing | generic r.json(), no typed validation | products/has_next, typed stock validation | different |
| 400 | raise_for_status, no retry | formerly FAILED/discarded body; now BAD_REQUEST + sanitized diagnostic | deliberate change |

No other seller-specific example/fixture/test found by search in the pinned
checkout. The v1 retirement comment is evidence of intention, not a v1->v2 field
mapping or proof that v2 accepts an unfiltered request today.

## Loaded identities (read-only inspection)

Workshop6 / connection2:25 catalog products,27 SKU relations,27 unique positive
integer SKU, zero invalid/duplicate SKU; none equals its product_id or offer_id.
First three identity tuples anonymized consistently by row:

| product_id | offer_id | sku | Types | Sent in seller request |
|---|---|---|---|---|
| P1 | O1 | S1 | integer/string/positive integer | none |
| P2 | O2 | S2 | integer/string/positive integer | none |
| P3 | O3 | S3 | integer/string/positive integer | none |

The local catalog comes from product list + info list; SKU comes from detail sku
and sources[].sku. Catalog decoder retains positive values and deduplicates within
product; scoped SQLite SKU identity prevents duplicate relations. Service.skus
reads the local relation. Fetch passes the values to FBO only, NOT seller. The
seller operation sends no catalog product_id, offer_id or sku. Thus claiming
"wrong identifier caused400" would be unsupported. Go's explicit SKU request
validator rejects the entire invalid/duplicate input instead of silently changing
the intended requested set; this policy was preserved.

## v2 versus v4 — recommendation only

Upstream client.py:624 OzonSellerClient.product_info_stocks uses v4, optional
offer_id/product_id arrays under filter, limit100 and last_id; tool has no
warehouse request parameter. Its docstring describes product FBO/FBS stocks.
This is a candidate for a bulk Seller Stock Summary, with explicit selection of
the seller stock type. v2 is the existing Seller Warehouse Breakdown keyed by
SKU + warehouse, using free_stock. These are different data granularities.
Upstream does not type-check responses, so current fields, quantity semantics and
v4 cursor contract must still be verified before designing that summary.

For a basic totals-per-product screen v4 is the better candidate to investigate;
for the current per-seller-warehouse screen v2 remains the intended endpoint.
A switch would risk losing warehouse detail or mixing FBO/FBS quantities. It
requires explicit architectural approval and a separately confirmed response
contract. No v4 endpoint, single-SKU loop or N+1 was added.

## Implemented correction

request.go now maps400 to BAD_REQUEST and reads a bounded error body.
diagnostics.go captures sanitized request/response, code/message/details only
for400 in existing scoped trace; Error holds it internally with json:"-".
No error payload added to application Result. Existing owner/manage-only
/ozon debug reads history; normal UI identifies HTTP400 and points there.

Limits: read at most16KiB+1; retain at most4KiB per JSON body; recursively scrub
known credential values including escaped JSON/numbers, omit credential keys,
redact credential-labelled free text, limit nesting16. Invalid/oversized/truncated
bodies become an explicit omission marker, never raw text or a sliced secret.
No HTTP headers are captured. Existing success path and endpoint unchanged.

Synthetic test error after sanitization (NOT an observed Ozon reply):

```json
{"code":3,"message":"invalid limit","details":[{"field":"limit","reason":"must be positive","echo":"[REDACTED]","escaped":"[REDACTED]","text":"[REDACTED]","nested":{}}]}
```

Default Go body BEFORE and AFTER: `{"limit":100}`. The HTTP400 root cause is
still unknown. The verified bug fixed here is loss of diagnostics, not a proven
request schema error. Existing historical FAILED entries are not rewritten.

## Validation

- Targeted `go test ./internal/marketplace/ozon`: exit0.
- New tests: upstream default/batch/cursor JSON equivalence, SKU array of strings,
  no product/offer substitution, no per-product fanout, invalid/zero/duplicate
  rejection,100-SKU one request, safe400 JSON/code/message/details, escaped/numeric
  credential removal, bounded errors, error/MCP result isolation and UI400.
- Existing retry test extended:400/401/403 one attempt,429 cooldown unchanged.
- Full `go test ./...`: exit0 (2026-09-28, session43398); Telegram69.684s,
  Ozon1.302s, MCP manager9.635s. All packages passed. `git diff --check`: exit0.
- No live request: credentials absent from inherited process environment. .env
  not read, production database only inspected read-only, bot not started.
- Existing EXE not rebuilt by this audit; changes apply after a later rebuild.

Sources: pinned local checkout above; public upstream
https://github.com/DeviceIngineering/ozon-mcp-server/blob/b95da59689cdacb544c659c5c0c1fcbecc50a995/ozon_mcp/client.py
and server.py. Official https://docs.ozon.ru/api/seller/ was inaccessible
(redirect loop); no current official-schema claim is made.
