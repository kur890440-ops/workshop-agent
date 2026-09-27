# WB и Ozon: не переносить предположения

WB-колонка основана на текущем коде Workshop Agent (`internal/marketplace/wildberries`, `internal/integrations/wbmcp`), Ozon — на audited source. Это сравнение интеграций, не полная справка обо всех API платформ.

| Concept | WB / Workshop Agent | Ozon / audited upstream |
|---|---|---|
| Authentication | WB_API_TOKEN, Authorization | Seller Client-Id + Api-Key; отдельный Performance client_credentials -> Bearer |
| Product ID | nmID + variant/chrtID + barcode | product_id + offer_id + SKU; точное отношение scheme SKU проверить |
| Seller SKU | vendorCode/артикул, barcode для вариантов | offer_id; SKU Ozon — другой ID |
| Stocks | seller stocks и WB stocks разведены по источникам | FBS/rFBS seller warehouse и FBO/Ozon analytics разведены; product aggregate и warehouse rows не суммировать |
| Warehouses | WB и seller warehouse namespaces | Ozon warehouse/cluster и seller warehouse; не склад цеха |
| Prices | Marketplace prices отдельно от local cost | price object/old/min/marketing concepts; generic response не гарантирует все поля |
| Orders | FBS new orders и отдельные statuses | Posting — отправление, FBS/FBO версии раздельны; order может иметь несколько postings |
| Rate limits | В текущем Go коде есть group cooldown и SQLite persistence | Общий Seller retry, до 4 attempts, Retry-After cap10s; central durable limiter отсутствует |
| Errors | Safe typed codes, sync completeness и source status | Raw exceptions; часть ошибок возвращается обычным JSON; HTTP 429 не локальная блокировка |
| Hosts | Несколько fixed WB hosts по API family | Seller api-seller.ozon.ru; Performance api-performance.ozon.ru |
| MCP | Internal Go, in-memory, существующий executable | Standalone Python STDIO либо SSE+web; не подключать его напрямую в production |
| Tenant boundary | Users/workshops/scoped connection authorization | Shop routing + optional общий MCP token, users/workshops ACL нет |

Концепты доступа, источника данных, completeness и durable limiter стоит переиспользовать. Идентификаторы, quantities, денежные поля, статусы, endpoint groups и частоты — определить заново для Ozon. WB seller-info cooldown не является политикой Ozon и не должен блокировать его методы.
