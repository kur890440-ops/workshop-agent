package wildberries

import (
	"encoding/json"
	"time"
)

// RateWire is deliberately bounded and contains no remote strings or credentials.
type RateWire struct {
	Code           string `json:"code"`
	RetryNotBefore string `json:"retry_not_before"`
	RateKey        string `json:"rate_key"`
	Endpoint       string `json:"endpoint"`
	Source         string `json:"source"`
	BlockedLocally bool   `json:"blocked_locally"`
}

func RateEndpoint(group string) string {
	switch group {
	case "common":
		return "/api/v1/seller-info"
	case "prices":
		return "/api/v2/list/goods/filter"
	case "analytics":
		return "/api/analytics/v1/stocks-report/wb-warehouses"
	case "content":
		return "/content/v2/get/cards/list"
	case "marketplace":
		return "/api/v3/*"
	}
	return ""
}
func (e *RateLimitError) Wire() RateWire {
	return RateWire{"WB_RATE_LIMITED", e.RetryAt.UTC().Format(time.RFC3339Nano), e.Operation, RateEndpoint(e.Operation), e.Source, e.BlockedLocally}
}
func DecodeRateError(value any) *RateLimitError {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 2048 {
		return nil
	}
	var v RateWire
	if json.Unmarshal(raw, &v) != nil || v.Code != "WB_RATE_LIMITED" || !ValidRateGroup(v.RateKey) || !ValidRateSource(v.Source) || v.Endpoint != RateEndpoint(v.RateKey) {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, v.RetryNotBefore)
	if err != nil || at.IsZero() {
		return nil
	}
	return &RateLimitError{Operation: v.RateKey, RetryAt: at, Source: v.Source, BlockedLocally: v.BlockedLocally}
}
