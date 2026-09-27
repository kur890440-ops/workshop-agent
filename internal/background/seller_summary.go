package background

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"workshop-agent/internal/auth"
	wb "workshop-agent/internal/marketplace/wildberries"
)

// Derived solely from this run's confirmed SELLER rows. Never mixes last-known rows.
type SellerStockSummaryItem struct {
	NmID          int64  `json:"nm_id"`
	ChrtID        int64  `json:"chrt_id"`
	ProductName   string `json:"product_name"`
	VendorCode    string `json:"vendor_code,omitempty"`
	Variant       string `json:"variant,omitempty"`
	TotalQuantity int64  `json:"total_quantity"`
	ConfirmedAt   string `json:"confirmed_at"`
	Partial       bool   `json:"partial"`
	Low           bool   `json:"low"`
}

func summaryLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	r := []rune(strings.TrimSpace(s))
	if len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return string(r)
}
func buildSellerItems(q auth.Querier, workshop, connection, threshold int64, b wb.StockBatch) ([]SellerStockSummaryItem, error) {
	if b.Info.Source != "" && b.Info.Source != wb.StockSeller {
		return nil, ErrInput
	}
	if !b.Usable() {
		return nil, nil
	}
	grouped := map[[2]int64]*SellerStockSummaryItem{}
	for _, r := range b.Rows {
		key := [2]int64{r.NmID, r.ChrtID}
		x := grouped[key]
		if x == nil {
			if len(grouped) >= 300 {
				return nil, ErrInput
			}
			x = &SellerStockSummaryItem{NmID: r.NmID, ChrtID: r.ChrtID, ConfirmedAt: b.Info.CapturedAt, Partial: b.Info.Status != "SUCCESS"}
			e := q.QueryRow(`SELECT c.title,c.vendor_code,COALESCE(v.size,'') FROM marketplace_cards c LEFT JOIN marketplace_variants v ON v.connection_id=c.connection_id AND v.workshop_id=c.workshop_id AND v.nm_id=c.nm_id AND v.chrt_id=? WHERE c.workshop_id=? AND c.connection_id=? AND c.nm_id=?`, r.ChrtID, workshop, connection, r.NmID).Scan(&x.ProductName, &x.VendorCode, &x.Variant)
			if e != nil && e != sql.ErrNoRows {
				return nil, e
			}
			x.ProductName = summaryLabel(x.ProductName)
			x.VendorCode = summaryLabel(x.VendorCode)
			x.Variant = summaryLabel(x.Variant)
			if x.Variant == "0" {
				x.Variant = ""
			}
			if x.ProductName == "" {
				x.ProductName = x.VendorCode
			}
			if x.ProductName == "" {
				x.ProductName = fmt.Sprintf("Товар WB #%d", r.NmID)
			}
			grouped[key] = x
		}
		if r.Quantity < 0 || x.TotalQuantity > math.MaxInt64-r.Quantity {
			return nil, ErrInput
		}
		x.TotalQuantity += r.Quantity
	}
	// Non-display variants collapse to the product; named variants stay distinct.
	collapsed := map[[2]int64]*SellerStockSummaryItem{}
	for key, x := range grouped {
		if x.Variant == "" {
			key[1] = 0
			x.ChrtID = 0
		}
		if old := collapsed[key]; old != nil {
			if old.TotalQuantity > math.MaxInt64-x.TotalQuantity {
				return nil, ErrInput
			}
			old.TotalQuantity += x.TotalQuantity
		} else {
			collapsed[key] = x
		}
	}
	grouped = collapsed
	if len(grouped) == 0 {
		return nil, nil
	}
	out := make([]SellerStockSummaryItem, 0, len(grouped))
	for _, x := range grouped {
		x.Low = x.TotalQuantity > 0 && x.TotalQuantity < threshold
		out = append(out, *x)
	}
	rank := func(x SellerStockSummaryItem) int {
		if x.TotalQuantity == 0 {
			return 0
		}
		if x.Low {
			return 1
		}
		return 2
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if a.ProductName != b.ProductName {
			return a.ProductName < b.ProductName
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.NmID != b.NmID {
			return a.NmID < b.NmID
		}
		return a.ChrtID < b.ChrtID
	})
	return out, nil
}
func sourceSummary(status string) string {
	switch status {
	case "SUCCESS":
		return "Данные обновлены."
	case "PARTIAL":
		return "Данные получены частично."
	case "PERMISSION_DENIED":
		return "Недоступны для текущего токена."
	case "RATE_LIMITED":
		return "Временно ограничено WB; прежние значения сохранены."
	default:
		return "Данные не обновлены; прежние значения сохранены."
	}
}
func summaryDate(raw string, loc *time.Location) string {
	v, e := time.Parse(time.RFC3339Nano, raw)
	if e != nil {
		return ""
	}
	return v.In(loc).Format("02.01.2006 15:04")
}
func Summary(j Job, a Aggregate, status string) string {
	loc, e := time.LoadLocation(j.Timezone)
	if e != nil {
		loc = time.UTC
	}
	state := "не выполнено"
	if status == "success" {
		state = "успешно"
	}
	if status == "partial_success" {
		state = "частично успешно"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Wildberries · Утренняя синхронизация\nРезультат: %s\n", state)
	if stamp := summaryDate(a.SellerSource.CapturedAt, loc); stamp != "" {
		fmt.Fprintf(&out, "Получено: %s\n", stamp)
	}
	out.WriteString("\nОСТАТКИ ПРОДАВЦА\n")
	if len(a.SellerItems) > 0 {
		if a.SellerSource.Status == "PARTIAL" {
			out.WriteString("Суммы только по подтверждённым в этой загрузке складам; итог по всем складам может быть неполным.\n")
		}
		for i, x := range a.SellerItems {
			name := summaryLabel(x.ProductName)
			if x.Variant != "" {
				name += " · " + summaryLabel(x.Variant)
			}
			if x.VendorCode != "" && x.VendorCode != x.ProductName {
				name += " [" + summaryLabel(x.VendorCode) + "]"
			}
			mark := ""
			if x.TotalQuantity == 0 {
				mark = " ⛔"
			} else if x.Low {
				mark = " ⚠️"
			}
			fmt.Fprintf(&out, "%d. %s — %d шт.%s\n", i+1, name, x.TotalQuantity, mark)
		}
		fmt.Fprintf(&out, "Товарных позиций: %d\n", len(a.SellerItems))
	} else if a.SellerSource.Status == "SUCCESS" {
		out.WriteString("В этой загрузке позиции не получены.\n")
	} else {
		out.WriteString("Подтверждённого списка этой загрузки нет. Сохранённые остатки: /wb seller_stocks\n")
	}
	if a.SellerSource.Status == "PARTIAL" {
		out.WriteString("⚠️ Данные получены частично. Отсутствующие позиции не обнулены; прежние значения сохранены отдельно и в эти суммы не включены.\n")
	} else if a.SellerSource.Status != "SUCCESS" {
		out.WriteString(sourceSummary(a.SellerSource.Status) + "\n")
	}
	out.WriteString("\nСКЛАДЫ WB\n" + sourceSummary(a.WBSource.Status) + "\n")
	out.WriteString("\nЦЕНЫ\n")
	if a.PricesOK {
		fmt.Fprintf(&out, "Данные обновлены. Изменилось товаров: %d.\n", a.PriceChanges)
	} else {
		out.WriteString("Актуальные данные не обновлены; последние сохранённые значения оставлены без изменений.\n")
	}
	if j.Status == "active" || j.Status == "" {
		fmt.Fprintf(&out, "\nСледующий запуск: %s (%s).\n", time.Unix(j.NextRun, 0).In(loc).Format("02.01.2006 15:04"), j.Timezone)
	} else {
		out.WriteString("\nРасписание не активно.\n")
	}
	out.WriteString("Склад мастерской учитывается отдельно.")
	return out.String()
}

// SplitMorningSummary preserves whole product lines and budgets Telegram UTF16 units.
func SplitMorningSummary(text string) []string {
	const limit = 3800
	var out []string
	part := ""
	for _, line := range strings.Split(text, "\n") {
		if len(utf16.Encode([]rune(part+line+"\n"))) > limit && part != "" {
			out = append(out, strings.TrimSuffix(part, "\n"))
			part = "Wildberries · Продолжение сводки\n"
		}
		// Product fields are bounded at normalization; never split a product line.
		part += line + "\n"
	}
	if part != "" {
		out = append(out, strings.TrimSuffix(part, "\n"))
	}
	return out
}
