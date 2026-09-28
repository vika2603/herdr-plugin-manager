package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// APIURL is GitHub's REST API.
const APIURL = "https://api.github.com"

// ErrRateLimited is returned when GitHub's API allowance is used up.
// Unauthenticated, it is 60 requests an hour for each address.
var ErrRateLimited = errors.New("GitHub's API rate limit is used up; set GH_TOKEN or GITHUB_TOKEN to raise it")

// Release is a published GitHub release: the notes its author wrote for a
// tag.
type Release struct {
	Tag         string
	Name        string
	Notes       string
	PublishedAt time.Time
	URL         string
}

// Releases lists the published releases of src's repository, the newest
// first, with their notes made printable. It reads up to the 100 newest.
func (c *Client) Releases(ctx context.Context, src source.GitHub) ([]Release, error) {
	url := APIURL + "/repos/" + src.Owner + "/" + src.Repo + "/releases?per_page=100"
	data, err := c.fetch(ctx, url, func(h http.Header) {
		h.Set("Accept", "application/vnd.github+json")
		if c.Token != "" {
			h.Set("Authorization", "Bearer "+c.Token)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("read the releases of %s: %w", src.Repository(), err)
	}
	var raw []struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		Draft       bool      `json:"draft"`
		PublishedAt time.Time `json:"published_at"`
		HTMLURL     string    `json:"html_url"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("read the releases of %s: %w", src.Repository(), err)
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		if r.Draft {
			continue
		}
		out = append(out, Release{
			Tag: r.TagName, Name: safe.Line(r.Name), Notes: safe.Text(r.Body),
			PublishedAt: r.PublishedAt, URL: r.HTMLURL,
		})
	}
	return out, nil
}
