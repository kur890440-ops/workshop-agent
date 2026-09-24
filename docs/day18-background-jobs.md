> Исторический документ предыдущего этапа. С 2026-09-24 STDIO, отдельные
> server/smoke EXE и прежняя схема jobs заменены [рефакторингом WA-D154–156](day18-refactor.md).
> Старые команды сборки ниже не являются текущей инструкцией запуска.

# Day18 — WB_DAILY_SYNC

@PROJECT:WORKSHOP_AGENT @L3:DAY18_BACKGROUND_JOBS @PRESERVE

Статус: реализация и offline-приёмка завершены 2026-09-24. Основное задание: ежедневные цены и остатки через
существующий MCP, SQLite snapshots/current/diff, короткая Telegram-сводка.

## WA-D151 — расписание и область доступа

Явное включение с marketplace.manage (по текущей политике это владелец). DAILY 08:00,
timezone мастерской (начальное явное значение Europe/Moscow), UTC next_run_at.
Фоновый доступ проверяет membership создателя и connection/revision, а не его
текущий открытый Telegram context. UI продолжает проверять активную мастерскую.
Один кабинет по прежней модели. Токен остаётся за MCP boundary нового flow.

## WA-D152 — история, целостность, повторные запуски

Migration 113: timezone/threshold, background_jobs/runs, snapshots/current/diffs.
Уникальность workshop/job_type/local_date, один running run на job, lease и
проверка владельца lease перед записью; сетевые ожидания вне SQL transactions.
Partial сохраняет успешный источник, не затирает другой. Исчезнувшие строки
не превращаются в нули. Сравниваются совпадающие variant/warehouse keys;
числа изменений агрегируются по nmID. Первый снимок — baseline, не изменение.
Цены в целых сотых единицы валюты, отдельно от внутренних цен/себестоимости.
Инвентарь/производство не изменяются. Scheduler не выбирает tools из parameters.

## WA-D153 — недостающий read-only метод цен

В Day17 метода цен не было. Для выполнения Day18 добавить только Prices,
GET discounts-prices-api.wildberries.ru/api/v2/list/goods/filter, limit/offset,
затем зарегистрировать wb_get_prices через существующий SDK/server/client.
Контракт найден в официальном индексе:
https://dev.wildberries.ru/en/openapi/work-with-products
Сверка 2026-09-24: полный документ возвращает HTTP 498; индекс содержит listGoods,
sizes/sizeID/price/discountedPrice/currencyIsoCode4217 и пагинацию до пустого массива.
Ограничение актуальности полной схемы сохраняется как прежний D1.

## Оперативное состояние

Исходное состояние: migration112, пять MCP tools; scheduler/job model/timezone
отсутствовали. Текущее: migration113, шесть read-only tools, persistent scheduler.
После сжатия перечитать этот документ и marketplace L0–L3/WORKSTATE.
Day18 завершён; Day19 не начинать без нового задания. Live WB/Telegram и рабочую DB
не использовать для повторения offline-проверок.

## Реализация

| Часть | Файл / контракт |
|---|---|
| Persistent job, Start/Close/Tick, общий scheduled/RunNow executor | internal/background/service.go |
| Snapshot/current/diff transactions, normalize/aggregate | internal/background/snapshots.go |
| Миграция с backup перед обновлением старой базы | internal/storage/background.go, migration 113; storage/sqlite.go |
| Новый read-only API метод | internal/marketplace/wildberries/prices.go: Client.Prices |
| MCP регистрация | internal/integrations/wbmcp/server.go: wb_get_prices → Client.Prices; wb_get_wb_stocks → Client.WBStocks |
| MCP client | internal/integrations/mcpclient/prices.go и call.go, прежний SDK/STDIO/session/identity |
| Telegram меню, отправка по внутреннему user ID | internal/telegram/background.go |
| Startup/shutdown, offline report до config.Load | cmd/workshop-agent/main.go; internal/experiment/day18.go |

Таблицы: workshop_settings дополнена timezone и wb_low_stock_threshold;
background_jobs, background_job_runs, wb_daily_snapshots, wb_daily_current,
wb_daily_diffs. marketplace_cooldowns расширена группой prices с сохранением
существующих deadlines. Новые таблицы не заменяют production/inventory.
В существующей производственной схеме нет отдельного столбца себестоимости:
новые цены не добавляются в products/materials и не используются как себестоимость.

Schedule хранится явно: DAILY, local_time=08:00, IANA timezone, next_run_at UTC Unix.
Встроена time/tzdata для Windows. time.Date/LoadLocation рассчитывают календарную
дату; Add(24h) не используется. Изменённое время сохраняется в самой job, timezone
также в workshop_settings. Порог по умолчанию 5; low означает 0 < stock < threshold,
zero подсчитывается отдельно. Аггрегирование по nmID, stock суммируется по
полученным вариантам/складам с защитой переполнения. Отсутствующие строки
не удаляются из current и не обнуляются; captured_at показывает возраст данных.

Цена хранится в сотых единицы Currency, DiscountedCents nullable. Diff включает
old_json/new_json/delta_json по ключу варианта или варианта+склада; валютная смена
считается изменением, но денежная delta между разными валютами не вычисляется.
ProductsCount — объединение nmID успешных выборок, не размер всего каталога.

## Управление

`/wb_auto` или Wildberries → Автосинхронизация.
`/wb_auto enable` создаёт расписание с явным действием владельца; автоматического
создания jobs при миграции нет. Нужна включённая WB-привязка и проверенный seller ID.
Доступны Run Now, Pause, Resume, Cancel; изменение:
`/wb_auto time 08:00 Europe/Moscow 5`.
`/wb_auto run`, `pause`, `resume`, `cancel` — текстовые эквиваленты кнопок.
Paused допускает явный Run Now, но не scheduled запуск. Cancel окончательный
для данной job в этом этапе; история остаётся. Resume применяется к paused.
UI проверяет active workshop; scheduler — persisted workshop/connection и права
создателя независимо от текущего окна Telegram.

Run Now и Tick вызывают один execute. Уникальный ключ (workshop,job_type,local_date)
ограничивает число логических запусков, включая ручной запуск до 08:00.
Просроченное расписание не запускается до сегодняшнего локального времени;
после него выполняется один catch-up, next_run_at переносится на следующий день.
Дополнительные scheduler retries отсутствуют: используется ограниченная политика
WB adapter, 429/deadline сохраняются, auth/config не ретраятся планировщиком.
Run failure сохраняется отдельно и не удаляет active job.

Lease 2 минуты, heartbeat 20 секунд, общий executor timeout 4 минуты.
Истёкший running становится failed/interrupted (либо partial_success при наличии
сохранённых строк), поздняя запись от потерявшего lease worker отклоняется.
Уже занятая локальная дата не запускается повторно даже после failed/interrupted:
это консервативная at-most-once политика первого этапа. Catch-up не воспроизводит
пропущенные дни. Источники сохраняются отдельными transactions без сетевого ожидания.

Перед отправкой проверяются user/membership/доступ; Telegram ID берётся из users.
Отказ доставки записывается в notification_state и не откатывает снимки.
Повторная доставка после сбоя/перезапуска пока не реализована; pending/failed
остаются видимыми в run history. Настоящие сообщения при разработке не отправляются.

## Проверки и установка

Тесты: internal/background/service_test.go (расписание/DST, snapshots/diff,
partial, scope, revoke, paused/resume/cancel, catch-up, duplicate/overlap/lease,
изоляция материалов и фиктивной внутренней себестоимости);
wildberries/prices_test.go (пагинация, точные деньги, malformed/repeat guard);
mcpclient/call_test.go (реальные Prices по STDIO, safe failures);
telegram/background_test.go (кнопки, настройки и отказ уведомления отозванному user).
Прежние tests registry/схем обновлены на 6 read-only tools, write=0.

Финальные доказательства:
- `go test ./...` — exit 0; background 6.328s, storage 12.342s,
  Telegram 66.940s; остальные пакеты прошли.
- `go build -o bin/workshop-agent-check.exe ./cmd/workshop-agent` — exit 0.
- `go build -o bin/check/wb-mcp-server-day16.exe ./cmd/wb-mcp-server` — exit 0.
- `workshop-agent-check.exe day18-background-report` — exit 0;
  [HTML](../reports/day18-background-jobs/20260924T132053.913479400Z/report.html),
  [JSON](../reports/day18-background-jobs/20260924T132053.913479400Z/result.json).
  Actual STDIO, mock WB/Telegram, временная fixture DB в каталоге отчёта;
  2 runs, 8 snapshot rows, 2 diffs, products=3, price_changes=1,
  stock_changes=1, zero=1, low=2, errors=0. Restart + two ticks → один catch-up.
- Day16 smoke с новым staged server — exit 0, 6 tools/write=0/session+process closed;
  [HTML](../reports/day16-mcp/20260924T131709.901199500Z/report.html).
- `git diff --check` — exit 0.

SHA256 app: `AFBBA3B60E534231EE2D5F6AEFED7A5D79EAE9569ED226283C86FE0EEFDB4843`.
SHA256 staged server: `0120627B04C221E616E69CBD7582FEE2F6E43339965532193C067A82F22802B4`.

Сборки подготовлены: bin/workshop-agent-check.exe и
bin/check/wb-mcp-server-day16.exe. Работающие процессы app/server обнаружены,
поэтому основные EXE не перезаписаны и не остановлены. После остановки приложения
заменить оба файла под прежними именами:

```powershell
Copy-Item .\bin\workshop-agent-check.exe .\bin\workshop-agent.exe
Copy-Item .\bin\check\wb-mcp-server-day16.exe .\bin\wb-mcp-server-day16.exe
.\bin\workshop-agent.exe
```

При следующем обычном запуске миграция применяется штатным storage после backup.
Рабочая БД в ходе разработки не открывалась. Не запускать две копии bot одновременно.
Offline demo: `.\bin\workshop-agent-check.exe day18-background-report`.

Пределы: один кабинет; только READ_ONLY; без LLM выбора tools; без WB writes.
Полный актуальный контракт цен недоступен (HTTP 498); индекс официальной документации
и HTTP-заглушки не заменяют live проверку D1/V1. Day19 не начат.
