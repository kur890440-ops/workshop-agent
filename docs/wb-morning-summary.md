# WA-D165 — товарная утренняя сводка (0.19.4)
@NO_COMPRESS @PRESERVE

Scope: presentation/aggregation only; same scheduler, pipeline graph, MCP and read-only WB client. Existing snapshot/current policy unchanged.

Before: Aggregate contained counters; Summary in wb_executor.go printed technical source statuses, raw-record counts and misleading zero/low counts for unavailable sources. Day18 retrieval had a separate formatter. Seller rows use nmID/chrtID/warehouseID; cards provide title/vendor_code, variants.size provides optional display size.

Now: prepareMarket calls buildSellerItems (seller_summary.go) inside existing wb_build_market_summary underlying service. SELLER rows from the current run are grouped by product+variant; named variants stay separate, unnamed/placeholder size0 collapse to product. Quantities summed only within SELLER. WB and internal inventory never enter this function. IDs stay in typed aggregate for identity, not ordinary text. Names/article/size are read from local SQLite per distinct product/variant, zero HTTP.

Aggregate.seller_items contains bounded normalized name/article/variant, quantity, confirmation time, partial flag and low-stock indicator using existing threshold. No raw API responses. Positive quantity addition checks overflow. Maximum300 pre-collapse variant groups and existing64KiB aggregate limit; oversized input fails explicitly, never silently truncates a list. Save recomputes and compares aggregate structurally; transactional policy unchanged. Existing result_json reader cap aligned to writer1MiB, not unbounded.

PARTIAL list sums CURRENT received rows only: incomplete totals are explicitly labelled. Previously retained rows stay in current storage but are not presented as newly confirmed. Missing does not create a row or zero. Confirmed0 shown as0 in the confirmed subset. No promise of complete all-warehouse quantity under PARTIAL. Sort: confirmed zero, low, then names/variants, stable ID tie-breakers.

One Summary formatter serves morning notification and Day18 stored summary. No technical enums/counters/raw errors. WB403 = unavailable, prices failure leaves previous data; timezone is Job.Timezone. Captured time comes from this run. Next schedule is current job schedule. Normalized product lines are never split; SplitMorningSummary budgets3800 UTF16 units and adds continuation headings. Telegram sender sends all parts sequentially, retaining permission checks. Retrieval uses same chunker, no extra WB requests.

Older aggregates: after validating saved run and pipeline, missing seller_items can be rebuilt from wb_daily_snapshots for exactly that run/source/workshop/connection plus local metadata. No production write, no mixing with latest current state. New runs persist list directly.

Verification:
- 7+13 on two seller warehouses ->20; named white/black variants separate.
- WB unavailable not represented by zero; seller data remains visible.
- PARTIAL received-row total excludes missing/last-known data.
-67 test display items survive UTF16 chunking without duplicates/missing lines.
- Stored summary retrieves same normalized totals, existing real MCP retrieval tests retained.
- Existing internal inventory isolation and 34/33/1 partial persistence retained.
- Read-only preview of real saved run4 (27.09.2026 08:00):67 seller rows ->23 product positions, one Telegram message. Sourcepartial warning retained. Artifacts reports/wb-morning-summary/.

Executable deployment and final test evidence: marketplace/WORKSTATE.md.
