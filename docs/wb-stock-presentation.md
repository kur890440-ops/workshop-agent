# WA-D164 — stock presentation (v0.19.3)
@PRESERVE

Presentation/read-model change only. WA-D162 source separation and WA-D163 fetch flow remain.
No endpoint, limiter, MCP, scheduler, pipeline, storage schema or snapshot semantics changed.

Before: StockSourcesText selected warehouse_id/nm_id/chrt_id/quantity/fetched_at and printed raw IDs, ISO timestamps and error codes. invalid_response with valid rows was usually the current PARTIAL missing-record reason, not necessarily a historical error.

Now: StockPage returns typed StockDisplayItem/StockDisplayPage using one SQLite LEFT JOIN query plus count per source. Cards.title and cards.vendor_code supply display name/article; variants.size supplies size; stocks.warehouse_name supplies warehouse. Keys include connection/workshop, variant includes nm_id. No HTTP or per-row API calls.
Fallback: title -> vendor_code -> "Товар WB #nmID"; absent size omitted; warehouse -> source-specific warehouse #ID. No invented names and no automatic internal-product matching.

StockSourcesText formats source status separately in Russian. Latest source run provides counts/status; historical error is never displayed as a current success error. PARTIAL explains missing/invalid separately. Legacy unknown counters are not zero. Newer manual attempts are shown separately from the saved snapshot. Retained older rows carry a local confirmation date; common snapshot time is shown once above list.
Time: workshop_settings.timezone, default Europe/Moscow when settings absent; invalid timezone falls back UTC. Embedded tzdata supports Windows. Format DD.MM.YYYY HH:mm.

Single-source pages have15 rows sorted by product display name, variant, warehouse and stable ID tie-breakers. Buttons Previous/Next and offset commands are available. Combined stocks completion shows5 rows per source with full-list links to avoid overlong messages. Existing Bot.screen uses sendMessage, not editMessageText; preserved its navigation convention. External display fields are bounded/control-character cleaned, UTF16 aware. Technical IDs/UTC/errors stay in owner/manage-only /wb stock_debug [seller_stocks|wb_stocks] [offset].

Tests: metadata/name/article/size/warehouse; vendor and numeric fallback;67 rows traversed pages15/15/15/15/7 without duplicates; workshop timezone; invalid previous run followed by success without stale error; partial data/restart/source403; unknown legacy counters; display causes zero API calls; UTF16 field bound. Existing tenant/permission checks retained.
Full tests and build results: marketplace/WORKSTATE.md.
