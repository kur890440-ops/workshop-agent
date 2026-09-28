"""Offline artifact renderer. Reads only mock evidence and test outputs in this directory."""
import html
import json
from pathlib import Path

root = Path(__file__).resolve().parent
evidence = json.loads((root / "evidence.json").read_text(encoding="utf-8"))
events = [json.loads(line) for line in (root / "tests.jsonl").read_text(encoding="utf-8-sig").splitlines() if line.strip()]
failed = [e for e in events if e.get("Action") == "fail"]
packages = [e for e in events if e.get("Action") == "pass" and not e.get("Test")]
tests = [e for e in events if e.get("Action") == "pass" and e.get("Test")]

def pre(v):
    return "<pre>" + html.escape(v if isinstance(v, str) else json.dumps(v, ensure_ascii=False, indent=2)) + "</pre>"

def demo(key):
    d = evidence[key]
    return pre(d["trace"]) + "<h3>Telegram output (mock)</h3>" + pre(d["telegram"])

tools = evidence["discovery"]["tools"]
sections = [
    ("Архитектура", "<p>Один процесс, один workshop-agent.exe, прежний MCP Client и in-memory session. WB и Ozon — два внутренних домена на общем SDK server. Day19 MCPPipelineRunner и Day18 Scheduler сохранены.</p>" + pre("Telegram User\n  ↓ Existing LLM Command Interpreter\nMarketplaceQueryIntent\n  ↓ MarketplaceToolRouter → trusted ExecutionPlan\nMarketplaceOrchestrator → existing MCP Client.CallTool\n  ↙ WB MCP domain       ↘ Ozon MCP domain\n READ tools              READ tools\n  ↘ normalization / explicit product mapping / comparison\nMarketplaceQueryResult → Telegram")),
    ("Зарегистрированные MCP server domains", "<p>Фактический ListTools SDK при initialize. Не два внешних процесса и не два клиента.</p>" + pre(evidence["discovery"])),
    ("WB tools", pre([t for t in tools if t["name"].startswith("wb_")])),
    ("Ozon tools", pre([t for t in tools if t["name"].startswith("ozon_")])),
    ("MarketplaceQueryIntent", pre(evidence["both"]["trace"]["intent"]) + "<p>STOCKS / PRODUCTS / COMPARE; AUTO / SELLER / MARKETPLACE / BOTH; typed filters LT / LE / EQ, explicit zero policy. LLM emits no tool or server names. Bare low stock uses the existing workshop threshold.</p>"),
    ("MarketplaceToolRouter", pre(evidence["both"]["trace"]["plan"]) + "<p>Static trusted capability bindings intersect actual discovery and READ_ONLY metadata. No arbitrary LLM tool dispatch. AUTO single marketplace exposes both separate stock sources; dual-marketplace comparison/filtering selects SELLER.</p>"),
    ("MarketplaceOrchestrator", "<p>Bounded sequential reads via existing MCP client. WB uses MCPRead access/identity gate and existing live READ tools. Ozon uses existing cache-only tools with local pagination and snapshot-change checks. No direct API clients in orchestrator. At most40 displayed rows per section, truncation explicit. Workshop/permissions/invariant/connection revision rechecked.</p>"),
    ("WB-only query", demo("wb_only")),
    ("Ozon-only query", demo("ozon_only")),
    ("Multi-marketplace query", demo("both")),
    ("Long-flow scenario", demo("long")),
    ("Product matching", "<p>WB mappings reused; Ozon mapping migration119, explicit owner command /ozon_map SKU INTERNAL_PRODUCT_ID. Unmapped rows excluded, counted. Full-source confirmed observations only in strict comparisons; no fuzzy name matching or cross-provider ID equality.</p>" + demo("comparison")),
    ("Partial success", demo("partial")),
    ("Orchestration Trace", "<p>Persisted user/workshop-scoped trace, /mcp_trace owner view. Includes intent, selected domains/tools, order, MCP call counts, input/normalized/mapped/filtered row counts, final status and duration; excludes credentials and raw API payloads. Revoked membership, disabled connection and stale workshop tested.</p>" + demo("disabled")),
    ("Итог", f"<p>Offline test evidence: {len(packages)} packages passed; {len(tests)} successful test events; {len(failed)} failures. See tests.jsonl. Real SDK MCP exercised; vendor and LLM data are mocked. No live API/Telegram/LLM calls, production migrations or .env reads.</p>" + "<p>Limitations: Ozon seller live import still needs its separately diagnosed mandatory SKU/offer filter fix. Day20 reports cached BAD_REQUEST/unknown honestly. Natural-language tests validate mock interpreter contracts, not live-model accuracy. Explicit mappings are required; different source timestamps are displayed, no simultaneous-snapshot promise. Existing EXE build evidence is in build.txt.</p>"),
]
page = """<!doctype html><html lang="ru"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>День 20 · Orchestration MCP</title><style>body{font:16px/1.55 system-ui,sans-serif;max-width:1150px;margin:40px auto;padding:0 24px;background:#f4f6fa;color:#172338}h1{font-size:36px}h2{margin-top:0}section{background:white;border:1px solid #dbe2ec;border-radius:12px;margin:22px 0;padding:24px}pre{font:13px/1.5 Consolas,monospace;background:#eef2f7;padding:18px;border-radius:8px;white-space:pre-wrap;overflow-wrap:anywhere}nav a{display:inline-block;margin:4px 10px;color:#275ca0}.badge{background:#deebff;padding:8px 14px;border-radius:6px}</style><h1>День 20 · Orchestration MCP</h1><p>Маршрутизация запросов между Wildberries и Ozon MCP</p><p class="badge">OFFLINE / REAL MCP SDK · MOCK VENDOR DATA · 2026-09-28</p>"""
page += "<nav>" + "".join(f'<a href="#s{i}">{i}. {html.escape(title)}</a>' for i, (title, _) in enumerate(sections, 1)) + "</nav>"
page += "".join(f'<section id="s{i}"><h2>{i}. {html.escape(title)}</h2>{body}</section>' for i, (title, body) in enumerate(sections, 1)) + "</html>"
(root / "report.html").write_text(page, encoding="utf-8")
print(json.dumps({"packages_passed": len(packages), "test_pass_events": len(tests), "failures": len(failed)}, ensure_ascii=False))
