package ozon

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

const maxErrorBody = 16 << 10
const maxDiagnosticJSON = 4 << 10

// FailureDiagnostic is persisted only in the scoped owner diagnostic history.
// It is deliberately excluded from Error JSON and application Result.
type FailureDiagnostic struct {
	Operation    string          `json:"operation"`
	Endpoint     string          `json:"endpoint"`
	HTTPStatus   int             `json:"http_status"`
	RequestID    json.RawMessage `json:"request_id,omitempty"`
	TraceID      json.RawMessage `json:"trace_id,omitempty"`
	RequestBody  json.RawMessage `json:"request_body"`
	ResponseBody json.RawMessage `json:"response_body"`
	OzonCode     json.RawMessage `json:"ozon_error_code,omitempty"`
	OzonMessage  json.RawMessage `json:"ozon_error_message,omitempty"`
	OzonDetails  json.RawMessage `json:"ozon_error_details,omitempty"`
}

var credentialText = regexp.MustCompile(`(?i)(?:authorization|bearer|basic|api[-_ ]?key|client[-_ ]?id|token|password|secret|credential)[\s\S]*`)

func sensitiveField(k string) bool {
	k = strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(k))
	for _, name := range []string{"authorization", "apikey", "clientid", "token", "password", "secret", "credential", "cookie"} {
		if strings.Contains(k, name) {
			return true
		}
	}
	return false
}

func (c *Client) sanitizeText(s string) string {
	for _, secret := range []string{c.clientID, c.apiKey} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	return credentialText.ReplaceAllString(s, "[REDACTED]")
}

func (c *Client) diagnosticJSON(raw []byte) json.RawMessage {
	omitted := json.RawMessage(`{"diagnostic":"body omitted: invalid, oversized or unreadable JSON"}`)
	if len(raw) > maxErrorBody || !json.Valid(raw) {
		return omitted
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return omitted
	}
	var scrub func(any, int) any
	scrub = func(v any, depth int) any {
		if depth > 16 {
			return "[OMITTED: depth]"
		}
		switch x := v.(type) {
		case string:
			return c.sanitizeText(x)
		case json.Number:
			if c.Sensitive(string(x)) {
				return "[REDACTED]"
			}
			return x
		case []any:
			for i := range x {
				x[i] = scrub(x[i], depth+1)
			}
			return x
		case map[string]any:
			m := map[string]any{}
			for k, value := range x {
				if sensitiveField(k) || c.Sensitive(k) {
					continue
				}
				m[c.sanitizeText(k)] = scrub(value, depth+1)
			}
			return m
		default:
			return x
		}
	}
	b, err := json.Marshal(scrub(v, 0))
	if err != nil || len(b) > maxDiagnosticJSON {
		return omitted
	}
	return b
}

func (c *Client) failureDiagnostic(request []byte, response io.Reader) *FailureDiagnostic {
	d := &FailureDiagnostic{RequestBody: c.diagnosticJSON(request)}
	b, err := io.ReadAll(io.LimitReader(response, maxErrorBody+1))
	if err != nil {
		b = nil
	}
	d.ResponseBody = c.diagnosticJSON(b)
	var fields map[string]json.RawMessage
	if json.Unmarshal(d.ResponseBody, &fields) == nil {
		d.OzonCode, d.OzonMessage, d.OzonDetails = fields["code"], fields["message"], fields["details"]
		d.RequestID, d.TraceID = fields["request_id"], fields["trace_id"]
	}
	return d
}
