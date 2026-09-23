# Контракты WB — детализация WA-D139

## Повторная сверка 2026-09-23: identity и cooldown

Цепочки исходного MCP и Telegram/Go повторно сверены по коду:
[основания](WB_STOCKS_FIX_PROMPT.md). Наш MCP вызывает WBStocks напрямую,
Telegram/Service раньше безусловно вызывал Seller перед каждой job. Теперь
это заменено кешем WA-D145; ниже сохранена историческая сверка первого этапа.

Официальный индекс https://dev.wildberries.ru/openapi//analytics подтвердил
request limit<=250000, offset, nmIds/chrtIds и пример data.items с nmId,
chrtId, warehouseId, warehouseName, quantity (также regionName/inWay*).
Используемая подвыборка DTO и page limit=1000 совместимы с этим примером;
оснований изменять parsing нет. Это не полная текущая сверка и не live-тест.
Прямое открытие страниц вновь не удалось; индекс имеет давность около пяти месяцев.

В https://dev.wildberries.ru/en/openapi/analytics для wb-warehouses указаны
Personal/Service, 3/min, interval 20 s, burst 1, обновление данных раз в 30 min.
Надёжно подтвердить текущие квоты каждого типа токена для seller-info не удалось;
не делать вывод о типе реального токена по 429. D1 остаётся открытым.

Официальные определения заголовков:
https://dev.wildberries.ru/knowledge-base/articles/019d49a1-28ca-7735-bf2f-98210695abc7/limity-zaprosov-wb-api
и https://dev.wildberries.ru/knowledge-base/articles/019d49a1-2cb0-781d-8921-deaf4a014a58/rasshifrovka-kodov-oshibok-wb-api.
Retry — секунды до повтора; Reset — секунды до полного восстановления burst,
не Unix timestamp. Retry=2 и Reset=29 не требуют ждать 29 при известном Retry.

Текущая политика повторов уточнена WA-D146: 429 возвращает структурированный
cooldown после первой попытки; исторические «три попытки» ниже остаются для 5xx.
В приложении срок сохраняется в SQLite; самостоятельный MCP Client без подключённого
CooldownStore имеет лишь память процесса, не читает БД бота и не координирует
квоту с ним. Долговечный cooldown этого исправления относится к Telegram/Service.

## Историческая сверка первого этапа

Дата сверки: 2026-09-22. Прочитан upstream client.py на точном audited commit.
Выбранные request/response поля и квоты сопоставлены с доступными разделами
официальной документации в поисковом индексе. Прямое открытие страниц и YAML,
включая загрузку вне sandbox, возвращает HTTP 498. Индекс помечен как полученный
около пяти месяцев назад. Поэтому **актуальность полной спецификации на дату
разработки не подтверждена**. D1 остаётся открытым; тесты проверяют приведённый
контракт, а не доступность реального WB.

Каждый путь ниже включён в закрытую allowlist клиента. Все семь операций читают
данные по смыслу API; POST не означает изменение. Остальные WB методы недоступны.

| Операция | Host и method/path | Используемый контракт | Права и пагинация |
|---|---|---|---|
| Кабинет | common-api.wildberries.ru GET /api/v1/seller-info | без body; sid/name | любая категория; одна запись |
| Карточки | content-api.wildberries.ru POST /content/v2/get/cards/list | settings.cursor, filter.withPhoto=-1; cards(nmID,title,vendorCode,updatedAt,sizes(chrtID,techSize,skus)), cursor | Контент; limit=100, следующий updatedAt/nmID; конец total<100 |
| Склады продавца | marketplace-api.wildberries.ru GET /api/v3/warehouses | массив id/name | Маркетплейс; все склады, без курсора |
| Остатки продавца | marketplace-api.wildberries.ru POST /api/v3/stocks/{warehouseId} | chrtIds; stocks(chrtId,amount) | Маркетплейс; локальные партии ≤1000 вариантов |
| Остатки WB | seller-analytics-api.wildberries.ru POST /api/analytics/v1/stocks-report/wb-warehouses | limit/offset; data.items(nmId,chrtId,warehouseId,warehouseName,quantity) | Аналитика, Personal/Service; локальный limit=1000 (в документации ≤250000), конец неполная страница |
| Новые FBS | marketplace-api.wildberries.ru GET /api/v3/orders/new | orders(id,nmId,chrtId,warehouseId,article,createdAt) | Маркетплейс; новые заказы, без курсора |
| Статусы FBS | marketplace-api.wildberries.ru POST /api/v3/orders/status | orders: ID[]; orders(id,supplierStatus,wbStatus) | Маркетплейс; 1–1000 ID |

Источники:

- [Кабинет и авторизация](https://dev.wildberries.ru/en/docs/openapi/api-information).
- [Карточки, склады и остатки продавца](https://dev.wildberries.ru/en/openapi/work-with-products).
- [FBS](https://dev.wildberries.ru/en/docs/openapi/orders-fbs?locale=ru).
- [WB stocks](https://dev.wildberries.ru/en/openapi/analytics).
- [Официальное описание нового метода остатков](https://dev.wildberries.ru/en/news/302).

Из доступных разделов: seller-info — 1/min, content — 100/min с интервалом 600 ms,
marketplace группы — 300/min с интервалом 200 ms, analytics stocks — 3/min/20 s.
Наш клиент консервативно объединяет marketplace-вызовы с интервалом 400 ms,
не использует burst и делит лимит между всеми операциями одного клиента/кабинета.
Это не координация с другими приложениями, использующими тот же WB кабинет.
429/5xx учитывают Retry-After (секунды или HTTP-date), не больше трёх попыток;
409 не повторяется и откладывает следующий вызов на десять интервалов.
Сетевые ошибки/401/403/некорректный ответ не повторяются автоматически.

Локальные пределы: HTTP timeout 30 s; работа 20 min; body 8 MiB; до 500 страниц
карточек, 50000 карточек/вариантов/строк остатков/заказов, 100000 баркодов.
WB stocks обновляются источником примерно раз в 30 min. fetched_at — время
получения, не гарантия момента изменения у WB. Диапазоны дат не принимаются:
историческая выгрузка заказов не входит в этап.

Пустой корректный массив и отсутствующее обязательное поле различаются.
Повтор курсора/ключа, превышение лимита или отсутствие запрошенного варианта/
статуса — ошибка полноты: прежний снимок сохраняется. В частности, неявный ноль
для отсутствующего варианта не придумывается. Фактическую семантику нулевых
остатков и отсутствие вариантов в WB ответах нужно уточнить при завершении D1/V1.

Проверка `/wb check` подтверждает identity/token через seller-info; она не
подтверждает все категории токена. Ошибка конкретной категории показывается
отдельно при соответствующей загрузке. Исторические, не импортированные ранее
заказы не извлекаются: статусы обновляются у новых и уже известных заказов.

Перед реальным использованием: завершить полную сверку официальных схем/прав/
квот и проверить нулевые остатки, пустые страницы, большие каталоги, срок действия
токена и смену sid. Не включать raw ответы с коммерческими данными/секретом в отчёт.
