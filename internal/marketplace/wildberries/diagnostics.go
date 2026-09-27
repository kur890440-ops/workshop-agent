package wildberries

import "time"

// LimitToOneRequest is for an operator's standalone diagnostic command only.
// Configure before use. It bounds all pagination and retries to one HTTP attempt.
func (c *Client) LimitToOneRequest() { n := 1; c.requestBudget = &n }

type RateState struct {
	Group    string    `json:"rate_key"`
	Endpoint string    `json:"endpoint"`
	Blocked  bool      `json:"blocked"`
	RetryAt  time.Time `json:"retry_not_before"`
	Source   string    `json:"source"`
}
type Diagnostics struct {
	TokenType         string      `json:"token_type"`
	IdentityCached    bool        `json:"identity_cached"`
	IdentityFetchedAt time.Time   `json:"identity_fetched_at"`
	Rates             []RateState `json:"rates"`
}

// Diagnostics never sends HTTP or returns a token, fingerprint or seller payload.
func (c *Client) Diagnostics() (Diagnostics, error) {
	d := Diagnostics{TokenType: c.TokenType()}
	cached, e := c.CachedSeller()
	if e != nil {
		return d, e
	}
	d.IdentityCached = cached.Seller.ID != ""
	d.IdentityFetchedAt = cached.FetchedAt
	for _, g := range []string{"common", "prices", "analytics", "content", "marketplace"} {
		v, e := c.cooldown(g)
		if e != nil {
			return d, e
		}
		d.Rates = append(d.Rates, RateState{g, RateEndpoint(g), v.RetryAt.After(c.clock()), v.RetryAt, v.Source})
	}
	return d, nil
}
