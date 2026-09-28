# Day20 — MCP orchestration (WA-D169)

@PROJECT:WORKSHOP_AGENT @L3:DAY20_MCP_ORCHESTRATION @NO_COMPRESS @PRESERVE

L0: dynamic read-only marketplace queries, one executable/process/client and
in-memory session. WB/Ozon are two registered internal domains, not two physical
servers. No pipeline replacement, external transport, autonomous loop or writes
to marketplaces. No live API/Telegram/LLM calls during development; .env unread.

L1: extend existing llm.StructuredCommand/SemanticClient with MarketplaceQueryIntent.
internal/marketplacequery owns typed intent, trusted capability registry/router,
query read-model, filter/comparison, scoped trace and explicit Ozon product mapping.
mcpmanager implements the call port using existing MCP client and registered tools.
WB uses existing authorized MCPRead and READ tools (may refresh upstream data);
Ozon uses existing cache-only tools, all local pages, no implicit refresh.
Names/mappings are local SQLite reads. Source timestamps and incompleteness remain
visible. Dynamic orchestration is bounded sequential calls, not PipelineRunnerV2.

L2: WA-D169 extends WA-D167/168; never infer product mapping from name/IDs across
providers. Existing WB mappings reused, Ozon mapping explicit owner action.
Strict comparison excludes unmapped or incomplete source totals, states why.
Missing/failed/stale !=0. No cross-provider/source sums; warehouse rows may be
aggregated only per explicitly mapped internal product within one provider/source.
Default source AUTO: single-provider unqualified stocks shows both sections;
multi-provider filtering/comparison uses SELLER. Explicit source always wins.
"low stock" without number uses existing wb_low_stock_threshold or asks for one.
LLM emits enums/filters only, never tool names/server/IDs. Local registry intersected
with actual ListTools read-only availability supplies trusted plan. Access checked
before each call and before return; InvariantEngine remains authoritative.
Trace excludes raw results/secrets; stored per user/workshop, admin reads scoped.

L3: inspect -> intent/router -> query/storage/MCP -> Telegram -> mock SDK evidence,
full go test ./..., report, canonical EXE rebuild after process check.
Acceptance and remaining limitations will be updated after verification.

Known unrelated live limitation: user-provided400 body now proves Ozon seller v2
requires sku or offer_id; existing request defaults omit both. Day20 cache reads
must report this source failure, not invent stock0. No silent endpoint switch.

## Implementation / acceptance 2026-09-28

- Intent/router: internal/marketplacequery/intent.go. STOCKS, PRODUCTS, COMPARE;
  sources AUTO, SELLER, MARKETPLACE, BOTH; comparison NONE, SIDE_BY_SIDE,
  WB_AVAILABLE_OZON_ZERO, BOTH_ZERO; filters LT/LE/EQ and configured threshold.
- Existing llm/semantic.go and StructuredCommand extended with marketplace_query.
  Strict decoding rejects unknown nested fields, IDs, arbitrary tools and writes.
  Natural-language variability is handled by the existing LLM interpreter; offline
  model response tests exercise its boundary, not a live-model accuracy benchmark.
- Dynamic query execution/normalization/render: marketplacequery/query.go.
  Existing MCP adapter: integrations/mcpmanager/query.go; SDK dispatch remains
  integrations/mcpclient. No vendor HTTP clients in MarketplaceOrchestrator.
- Trusted routes intersect actual SDK ListTools READ_ONLY flags:

| Provider | Capability/source | Actual tool |
|---|---|---|
| WB | STOCKS/SELLER | wb_get_seller_stocks |
| WB | STOCKS/MARKETPLACE | wb_get_wb_stocks |
| WB | PRODUCTS | wb_get_products |
| OZON | STOCKS/SELLER | ozon_get_seller_stocks |
| OZON | STOCKS/MARKETPLACE | ozon_get_ozon_stocks |
| OZON | PRODUCTS | ozon_list_products |

- WB query uses existing live MCP READ operation under MCPRead scope/revocation
  and identity guard; Ozon uses snapshot-only MCP calls with offsets15, bounded
  at10000 rows and detects changed run_id. Asymmetric freshness is explicit, not
  represented as a synchronized market snapshot. WB underlying tool may obtain
  catalog before stocks; no extra catalog MCP call from the orchestrator.
- Local cached titles/variants and explicit maps loaded in bulk. Identifiers
  remain provider-specific. Unknown/missing/failed/stale quantities excluded from
  zero/low filters; unfiltered output may show them as unconfirmed. Invalid rows
  (identity, timestamp, quantity or duplicate) mark the section incomplete.
- Strict comparison uses only complete SELLER sources and explicit internal
  mappings, sums warehouse rows within that one provider/product only. Unmapped
  positions counted and excluded. Similar names never establish a match.
- Migration119 adds ozon_product_mappings with scoped validation triggers and
  marketplace_query_traces. Existing WB mappings reused. Migration tested only
  on temporary databases; normal next application startup applies119 with backup.
- /ozon_map <SKU> <internal_product_id>: explicit owner mapping, local audit.
  /mcp_trace: owner-only latest own trace for active workshop. Text query paths
  async, recheck access before late Telegram reply. No credentials in tool input.
- /mcp_trace stores request/intent/plan, domain/tool order and MCP call counts,
  received/normalized/mapped/filtered counts, status and duration. Not raw results.

## Verified evidence

`reports/day20-mcp-orchestration/20260928-day20/report.html` is standalone with15
sections. evidence.json contains actual SDK traces and rendered mock Telegram
output; tests.jsonl contains full regression output; build.txt records EXE version.

Full `go test -json ./...`: exit0,20 packages,397 successful test events,0 failures
(session19174). Includes Day18/19 regressions, WB/Ozon, semantic schema, Telegram,
actual SDK both/WB-only/Ozon-only/long/comparison/partial, cross-workshop and revoked
access, disabled source and no name matching. git diff --check exit0.

Mock results:
- WB+Ozon LT5: WB1 MCP call (3 received,2 filtered); Ozon1 call (3/3).
- WB-only: WB1, Ozon0. Ozon catalog-only: WB0,Ozon1 (3 products).
- Long LE3 split: WB zero C0/low A2; Ozon zero C0/low A1+B3.
- Explicit cross-provider internal mapping: WB2/Ozon0 matched; unmapped excluded.
- Ozon RATE_LIMITED: PARTIAL_SUCCESS, WB preserved, Ozon retry deadline retained.

Canonical bin/workshop-agent.exe rebuilt, build exit0, --version:
`WorkshopAgent v0.21.0`. No running process found; no extra EXE created. Bot not
started; no .env read or live API/LLM/Telegram calls; production DB not migrated.

Limitations: live LLM accuracy and vendor responses are not certified by mocks;
Ozon400 import fix remains separate; mapping requires manual setup; maximum40
rendered rows per source with explicit truncation, no silent whole-catalog claim;
different source timestamps are not a single atomic comparison snapshot.

## WA-D171 - free-form stock source interpretation (2026-09-28)

Extend the existing LLM semantic instructions, no second model/client/router.
Colloquial marketplace stock questions select AUTO; generic warehouses wording
does not imply physical marketplace storage. Explicit FBS/rFBS -> SELLER,
FBO/FBW/platform storage -> MARKETPLACE, both -> BOTH. Current explicit intent
and negation win over previous dialog; local workshop inventory stays separate.
Existing trusted router defaults and MCP capabilities unchanged. Tests cover
transport of user text/instructions, schema decoding and source routing using
mock model responses. These tests do not measure live model language accuracy.
Release: canonical bin/workshop-agent.exe v0.21.2.

## WA-D172 - named-product follow-ups and totals (2026-09-28)

Cause: intent schema had no product-name/total fields; early marketplace handler
return also skipped short-term dialogue recording. Add optional product_name
(max120 chars) and total (STOCKS only). LLM resolves named-product follow-ups
using scoped recent dialog. Record intent only in the existing memory session,
not authoritative quantities. Explicit source/provider always wins.
Local matching uses normalized whole words and conservative Russian noun endings;
no ID guessing or cross-marketplace mapping by name. Family queries include
matching variants. All results still come from existing authorized MCP calls.
Totals computed before display truncation, separately per provider/source.
Partial/unconfirmed data yields confirmed subtotal with unknown-completeness label;
no matched data is never reported as zero. Current UI keeps matched row names
visible so product family selection is reviewable. No matching catalog/API calls.
TestProductNameAndPartialTotals covers names, partial/missing; extended
TestDay20SemanticTelegramAndTrace covers context, filtering and separate totals
through Telegram with a mocked LLM. Live model accuracy remains unverified.
Release canonical bin/workshop-agent.exe0.21.3, .env unread.

## WA-D173 - bounded semantic truncation recovery

Observed screenshot:1024 completion tokens and generic parse failure; provider
finish reason is unavailable, so the exact live cause remains unconfirmed.
Confirmed code defects: empty length response bypassed repair; JSON mode ignored
finish_reason=length. Detect length before empty-content/schema checks in shared
client. Semantic interpreter retries once with4096 tokens after initial1024
truncation, preserves combined usage, refuses even valid-looking truncated JSON.
Second truncation returns typed error and specific Telegram message/safe log code.
No retries for network/provider failures; ordinary schema repair unchanged.
Tests cover empty/partial/valid-looking truncated JSON, successful recovery,
second truncation, usage sum and429 no-retry. No live model call or .env access.
Canonical release0.21.4, same executable.
