package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// RateLimit is how much of GitHub's API allowance is left.
type RateLimit struct {
	Limit, Remaining int
	Reset            time.Time
	// Token is set when the requests carry a token.
	Token bool
}

// RateLimit reads GitHub's API allowance, which reading it does not use.
func (c *Client) RateLimit(ctx context.Context) (RateLimit, error) {
	data, err := c.fetch(ctx, APIURL+"/rate_limit", func(h http.Header) {
		h.Set("Accept", "application/vnd.github+json")
		if c.Token != "" {
			h.Set("Authorization", "Bearer "+c.Token)
		}
	})
	if err != nil {
		return RateLimit{}, err
	}
	var raw struct {
		Resources struct {
			Core struct {
				Limit     int   `json:"limit"`
				Remaining int   `json:"remaining"`
				Reset     int64 `json:"reset"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return RateLimit{}, fmt.Errorf("read GitHub's rate limit: %w", err)
	}
	core := raw.Resources.Core
	return RateLimit{Limit: core.Limit, Remaining: core.Remaining, Reset: time.Unix(core.Reset, 0), Token: c.Token != ""}, nil
}

// CacheStatus says what the cached index holds and when it was downloaded,
// without downloading it.
func (c *Client) CacheStatus() (entries int, fetchedAt time.Time, err error) {
	ix, at, err := c.readCache()
	if err != nil {
		return 0, time.Time{}, err
	}
	return len(ix.Entries()), at, nil
}
