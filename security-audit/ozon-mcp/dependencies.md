# Dependencies, reproducibility и license

Проверено 2026-09-27, Python 3.13.12, isolated venv. pip-audit exit 1: 10 advisory entries / 5 unique IDs, все у pip 25.3 — bootstrap/build tooling, не runtime dependency Ozon. Scanner продублировал advisory entries; не считать их десятью независимыми CVE. Полный raw JSON и freeze сохранены. Известных advisory в разрешённых runtime зависимостях scanner не вернул; это не отсутствие всех уязвимостей.

| Package | Version | Role | Scanner advisory IDs |
|---|---|---|---|
| aiosqlite | 0.22.1 | direct runtime | none reported |
| annotated-doc | 0.0.5 | transitive runtime | none reported |
| annotated-types | 0.8.0 | transitive runtime | none reported |
| anyio | 4.15.1 | transitive runtime | none reported |
| attrs | 26.1.0 | transitive runtime | none reported |
| boolean-py | 5.0 | audit/build/test tooling or audited root package | none reported |
| cachecontrol | 0.14.4 | audit/build/test tooling or audited root package | none reported |
| certifi | 2026.7.22 | transitive runtime | none reported |
| cffi | 2.1.1 | transitive runtime | none reported |
| charset-normalizer | 3.5.1 | audit/build/test tooling or audited root package | none reported |
| click | 8.5.0 | transitive runtime | none reported |
| colorama | 0.4.6 | transitive runtime | none reported |
| cryptography | 50.0.1 | direct runtime | none reported |
| cyclonedx-python-lib | 11.12.0 | audit/build/test tooling or audited root package | none reported |
| defusedxml | 0.7.1 | audit/build/test tooling or audited root package | none reported |
| fastapi | 0.141.1 | direct runtime | none reported |
| filelock | 4.0.4 | audit/build/test tooling or audited root package | none reported |
| h11 | 0.16.0 | transitive runtime | none reported |
| httpcore | 1.0.9 | transitive runtime | none reported |
| httptools | 0.8.0 | transitive runtime | none reported |
| httpx | 0.28.1 | direct runtime | none reported |
| httpx-sse | 0.4.3 | transitive runtime | none reported |
| idna | 3.20 | transitive runtime | none reported |
| iniconfig | 2.3.0 | audit/build/test tooling or audited root package | none reported |
| jinja2 | 3.1.6 | direct runtime | none reported |
| jsonschema | 4.26.0 | transitive runtime | none reported |
| jsonschema-specifications | 2025.9.1 | transitive runtime | none reported |
| license-expression | 30.4.4 | audit/build/test tooling or audited root package | none reported |
| markdown-it-py | 4.2.0 | transitive runtime | none reported |
| markupsafe | 3.0.3 | transitive runtime | none reported |
| mcp | 1.30.0 | direct runtime | none reported |
| mdurl | 0.1.2 | transitive runtime | none reported |
| msgpack | 1.2.2 | audit/build/test tooling or audited root package | none reported |
| ozon-mcp-server | 2.5.2 | audit/build/test tooling or audited root package | none reported |
| packageurl-python | 0.17.6 | audit/build/test tooling or audited root package | none reported |
| packaging | 26.3 | audit/build/test tooling or audited root package | none reported |
| pip | 25.3 | audit/build/test tooling or audited root package | PYSEC-2026-1796, PYSEC-2026-196, PYSEC-2026-2875, PYSEC-2026-2876, PYSEC-2026-3721 |
| pip-api | 0.0.35 | audit/build/test tooling or audited root package | none reported |
| pip-audit | 2.10.1 | audit/build/test tooling or audited root package | none reported |
| pip-requirements-parser | 32.0.1 | audit/build/test tooling or audited root package | none reported |
| platformdirs | 4.12.0 | audit/build/test tooling or audited root package | none reported |
| pluggy | 1.6.0 | audit/build/test tooling or audited root package | none reported |
| py-serializable | 2.1.0 | audit/build/test tooling or audited root package | none reported |
| pycparser | 3.0 | transitive runtime | none reported |
| pydantic | 2.13.5 | direct runtime | none reported |
| pydantic-core | 2.46.5 | transitive runtime | none reported |
| pydantic-settings | 2.15.0 | transitive runtime | none reported |
| pygments | 2.21.0 | transitive runtime | none reported |
| pyjwt | 2.15.0 | transitive runtime | none reported |
| pyparsing | 3.3.3 | audit/build/test tooling or audited root package | none reported |
| pytest | 9.1.1 | audit/build/test tooling or audited root package | none reported |
| pytest-asyncio | 1.4.0 | audit/build/test tooling or audited root package | none reported |
| python-dotenv | 1.2.3 | transitive runtime | none reported |
| python-multipart | 0.0.32 | transitive runtime | none reported |
| pywin32 | 312 | transitive runtime | none reported |
| pyyaml | 6.0.3 | transitive runtime | none reported |
| referencing | 0.37.0 | transitive runtime | none reported |
| requests | 2.34.2 | audit/build/test tooling or audited root package | none reported |
| rich | 15.0.0 | transitive runtime | none reported |
| rpds-py | 2026.6.3 | transitive runtime | none reported |
| shellingham | 1.5.4 | transitive runtime | none reported |
| sortedcontainers | 2.4.0 | audit/build/test tooling or audited root package | none reported |
| sse-starlette | 3.4.11 | transitive runtime | none reported |
| starlette | 1.7.0 | transitive runtime | none reported |
| tomli | 2.4.1 | audit/build/test tooling or audited root package | none reported |
| tomli-w | 1.2.0 | audit/build/test tooling or audited root package | none reported |
| typer | 0.27.2 | transitive runtime | none reported |
| typing-extensions | 4.16.0 | transitive runtime | none reported |
| typing-inspection | 0.4.4 | transitive runtime | none reported |
| urllib3 | 2.8.0 | audit/build/test tooling or audited root package | none reported |
| uvicorn | 0.54.0 | direct runtime | none reported |
| watchfiles | 1.3.0 | transitive runtime | none reported |
| websockets | 17.1 | transitive runtime | none reported |

## Pip advisories

- PYSEC-2026-1796: aliases CVE-2026-1703, GHSA-6vgw-5pg2-w6jp; fix versions 26.0. См. raw/pip-audit.json: исходное описание scanner.
- PYSEC-2026-2875: aliases CVE-2026-3219, GHSA-58qw-9mgm-455v; fix versions 26.1. См. raw/pip-audit.json: исходное описание scanner.
- PYSEC-2026-2876: aliases CVE-2026-6357, GHSA-jp4c-xjxw-mgf9; fix versions 26.1. См. raw/pip-audit.json: исходное описание scanner.
- PYSEC-2026-196: aliases GHSA-wf93-45jw-7689, CVE-2026-8643; fix versions 26.1.2. См. raw/pip-audit.json: исходное описание scanner.
- PYSEC-2026-3721: aliases GHSA-qwm4-qh6w-59xr, CVE-2026-13346; fix versions 26.2. См. raw/pip-audit.json: исходное описание scanner.

Для следующего чистого audit/build environment использовать исправленный pip (все перечисленные fixes покрывает >=26.2), проверенные индексы и hashes. Этот аудит не изменял глобальный Python/pip. Проверка ограничена версиями freeze.txt; будущие floating resolution не покрыты.

## Build / install / package parity

pyproject: Hatchling build backend, wheel package ozon_mcp; setup.py/custom install hooks не обнаружены. Console entrypoints ozon-mcp и ozon-mcp-web запускают server/app. Standard backend и зависимости всё равно исполняют Python при build/import — нет обещания безопасности любого downloaded wheel. Dependency lock с hashes отсутствует. GitHub publish использует OIDC Trusted Publishing; Actions version tags не закреплены commit hash.

PyPI 2.5.2 wheel и sdist скачаны, не запускались как отдельная поставка. 14 Python/template package files wheel совпали с commit после CRLF/LF normalization (working checkout Windows). Raw byte mismatch из-за EOL не считается подменой. Sdist package files/pyproject/LICENSE/README сравнены отдельно; подробности и SHA256 артефактов — raw/pypi-comparison.json. Это source parity, не verified build provenance/signature.

## License

Фактический source/LICENSE: MIT, Copyright (c) 2026 DeviceIngineering. Разрешает использование, копирование, модификацию, распространение и внутренний fork при сохранении copyright/license notices в копиях или существенных частях. При переносе заимствованного кода сохранить уведомление и LICENSE; это не разрешение от Ozon на API/данные и не гарантия автора. См. [LICENSE](source/LICENSE).

## Secret scan

Выполнен redacted heuristic regex scan всей доступной Git history: 49 commits / 211 unique blobs. Найден один explicit test token placeholder (tests/test_mcp_sse.py:30), production credentials не подтверждены. Private key/JWT/cloud/literal-secret patterns; не entropy scanner, binary и >2MB исключены. Не выдавать отсутствие совпадений за математическую гарантию. raw/secret-scan.txt содержит location/status без найденного значения.