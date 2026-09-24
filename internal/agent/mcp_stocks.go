package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"workshop-agent/internal/integrations/mcpclient"
	"workshop-agent/internal/marketplace"
)

// MCPStocks invokes the Day16 client through the application's authorization
// boundary. It never calls the WB adapter or invokes an LLM.
func (a *WorkshopAgent) MCPStocks(ctx context.Context, user, workshop int64, trace bool) (string, mcpclient.StocksResult, error) {
	var result mcpclient.StocksResult
	if a.Marketplace == nil || a.MCP == nil {
		return "", result, mcpclient.Error("mcp_not_configured")
	}
	scope := marketplace.Scope{UserID: user, WorkshopID: workshop}
	c, err := a.Marketplace.Status(scope)
	if err != nil {
		return "", result, err
	}
	scope.ConnectionID = c.ID
	err = a.Marketplace.MCPRead(ctx, scope, func(ctx context.Context, c marketplace.Connection) error {
		var e error
		result, e = a.MCP.Stocks(ctx, c.SellerID)
		return e
	})
	if err != nil {
		return "", mcpclient.StocksResult{}, err
	}
	var b strings.Builder
	b.WriteString("Wildberries · Остатки на складах WB\n")
	for i, row := range result.Value.Data {
		if i == 12 {
			fmt.Fprintf(&b, "Показано 12 из %d строк.\n", len(result.Value.Data))
			break
		}
		warehouse := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, row.WarehouseName)
		name := []rune(warehouse)
		if len(name) > 60 {
			warehouse = string(name[:60])
		}
		fmt.Fprintf(&b, "nmID %d · вариант %d · склад %d %s — %d шт.\n", row.NmID, row.ChrtID, row.WarehouseID, warehouse, row.Quantity)
	}
	if len(result.Value.Data) == 0 {
		b.WriteString("API вернул пустую выборку. Это не подтверждение нулевых остатков всех товаров.\n")
	}
	fmt.Fprintf(&b, "Получено через MCP. Время: %s.\nОстатки цеха и складов продавца сюда не входят.", result.Value.FetchedAt)
	if trace {
		fmt.Fprintf(&b, "\nMCP TRACE\n/wb_stocks → wb_get_wb_stocks → wildberries.Client.WBStocks → success\nTransport: stdio; duration: %d ms.", result.DurationMS)
	}
	return b.String(), result, nil
}

// MCPErrorMessage never includes arbitrary remote error strings.
func MCPErrorMessage(err error) string {
	var code mcpclient.Error
	if !errors.As(err, &code) {
		return ""
	}
	switch code {
	case "wb_configuration_error":
		return "WB_API_TOKEN не настроен на MCP-сервере. Задайте его локально и перезапустите приложение."
	case "wb_authentication_error", "wb_access_denied":
		return "WB отклонил доступ. Проверьте срок и права токена локально."
	case "wb_rate_limit":
		return "WB ограничил частоту запросов. MCP соблюдает сохранённый срок ожидания; повторите позже."
	case "wb_timeout":
		return "Время ожидания WB/MCP истекло. Данные не получены."
	case "wb_cancelled":
		return "Запрос WB отменён."
	case "wb_identity_required", "wb_identity_mismatch":
		return "Кабинет MCP не совпадает с привязкой мастерской или ещё не проверен."
	case "mcp_busy":
		return "Запрос MCP уже выполняется. Дождитесь результата."
	case "invalid_tool_arguments":
		return "Неверные параметры MCP-инструмента."
	case "mcp_connection_error", "mcp_not_configured":
		return "Не удалось подключиться к локальному MCP-серверу. Проверьте его сборку и настройку запуска."
	case "wb_result_limit", "wb_output_limit":
		return "Результат WB превышает допустимый размер. Полная выборка не получена."
	case "wb_api_error":
		return "WB API не вернул корректные данные."
	default:
		return "Ошибка MCP. Данные не получены."
	}
}
