"""Read-only repository audit; snapshot/verify a separate SQLite migration fixture."""
from pathlib import Path
import hashlib
import json
import re
import sqlite3
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'reports/task-only-refactor'

def dump(conn, table):
    return conn.execute('SELECT * FROM "' + table.replace('"', '""') + '" ORDER BY rowid').fetchall()

def fingerprint(rows):
    return hashlib.sha256(json.dumps(rows, ensure_ascii=False, sort_keys=True, default=str).encode()).hexdigest()

def snapshot(conn):
    tables = [r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")]
    return {
        'counts': {t: len(dump(conn, t)) for t in tables},
        'hashes': {t: fingerprint(dump(conn, t)) for t in tables},
        'tasks': conn.execute('SELECT task_id,user_id,workshop_id,state_json,status,created_by_user_id,assigned_to_user_id,created_at,updated_at FROM working_memory ORDER BY task_id').fetchall(),
    }

OUT.mkdir(parents=True, exist_ok=True)
mode = sys.argv[1]
if mode == 'snapshot':
    target = OUT / 'migration-fixture.db'
    if target.exists():
        raise SystemExit('Snapshot already exists; refusing to replace evidence')
    source = sqlite3.connect((ROOT/'data/workshop.db').as_uri()+'?mode=ro', uri=True)
    with sqlite3.connect(target) as copy:
        source.backup(copy)
        before = snapshot(copy)
    source.close()
    (OUT/'migration-before.json').write_text(json.dumps(before, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'snapshot': str(target), 'legacy_records': before['counts'].get('production_plans'), 'tasks':len(before['tasks'])}))
elif mode == 'verify':
    before=json.loads((OUT/'migration-before.json').read_text(encoding='utf-8'))
    with sqlite3.connect((OUT/'migration-fixture.db').as_uri()+'?mode=ro', uri=True) as conn:
        after=snapshot(conn)
        integrity=conn.execute('PRAGMA integrity_check').fetchall()
        foreign_keys=conn.execute('PRAGMA foreign_key_check').fetchall()
        version=conn.execute('SELECT COUNT(*) FROM schema_migrations WHERE number=108').fetchone()[0]
    permitted={'schema_migrations','production_plans','working_memory','long_term_memory','task_completion_intents'}
    checks={
        'version_108':version==1, 'integrity':integrity==[('ok',)], 'foreign_keys':not foreign_keys,
        'retired_table_removed':'production_plans' not in after['counts'],
        'task_ids_data_status_assignment_dates_preserved':json.loads(json.dumps(after['tasks']))==before['tasks'],
        'other_tables_unchanged':all(after['hashes'].get(t)==digest for t,digest in before['hashes'].items() if t not in permitted),
        'backup_created':bool(list(OUT.glob('migration-fixture.db.backup-*.db'))),
    }
    result={'checks':checks,'before_counts':before['counts'],'after_counts':after['counts'],'production_process_migrated':False}
    (OUT/'migration-result.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps(checks))
    if not all(checks.values()): raise SystemExit(1)
elif mode == 'search':
    result=subprocess.run(['rg','--json','-i','plan|план','--glob','!reports/task-only-refactor/**','--glob','!scripts/task_refactor_audit.py','.'],cwd=ROOT,capture_output=True,encoding='utf-8')
    if result.returncode not in (0,1):raise SystemExit(result.stderr)
    matches=[]
    for line in result.stdout.splitlines():
        item=json.loads(line)
        if item['type']!='match':continue
        d=item['data'];path=d['path']['text'].replace('\\','/').removeprefix('./');content=d['lines']['text']
        if path.startswith('reports/'):reason='Historical immutable experiment report'
        elif path.startswith('internal/storage/'):reason='Historical schema, migration 108, or migration regression fixture'
        elif path=='docs/task-only-refactor.md':reason='Legacy migration explanation and audit references'
        elif 'planning' in content.lower():reason='Technical preparation phase of Task; no separate entity'
        elif 'план' in content.lower() and path.endswith('_test.go'):reason='Regression assertion forbidding retired UI terminology'
        elif 'explan' in content.lower():reason='Substring in explanation; unrelated to domain'
        else:reason='REVIEW_REQUIRED'
        matches.append({'path':path,'line':d['line_number'],'occurrences':len(d['submatches']),'reason':reason})
    evidence={'pattern':'plan|план (case insensitive)','matches':matches,'excluded_audit_artifacts':'This audit script/output and the immutable before-search archive describe retired names; binary SQLite backups are not source code.'}
    (OUT/'remaining.json').write_text(json.dumps(evidence,ensure_ascii=False,indent=2),encoding='utf-8')
    review=[m for m in matches if m['reason']=='REVIEW_REQUIRED']
    print(json.dumps({'matching_lines':len(matches),'review_required':review},ensure_ascii=False))
    if review:raise SystemExit(1)
else:
    raise SystemExit('Use snapshot, verify or search')
