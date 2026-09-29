package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// Comparison is what one commit of a repository has that another has not.
type Comparison struct {
	// Status is GitHub's word for head against base: ahead, behind, diverged
	// or identical.
	Status string
	// Total counts the commits head has that base has not; Commits lists up
	// to the newest 100 of them, the newest first.
	Total   int
	Commits []Commit
	URL     string
}

// Commit is one commit of a comparison.
type Commit struct {
	SHA string
	// Title is the first line of its message.
	Title  string
	Author string
	Date   time.Time
}

// Compare reads what head has that base has not in src's repository.
func (c *Client) Compare(ctx context.Context, src source.GitHub, base, head string) (Comparison, error) {
	url := APIURL + "/repos/" + src.Owner + "/" + src.Repo + "/compare/" + base + "..." + head + "?per_page=100"
	data, err := c.fetch(ctx, url, func(h http.Header) {
		h.Set("Accept", "application/vnd.github+json")
		if c.Token != "" {
			h.Set("Authorization", "Bearer "+c.Token)
		}
	})
	if err != nil {
		return Comparison{}, fmt.Errorf("compare the commits of %s: %w", src.Repository(), err)
	}
	var raw struct {
		Status  string `json:"status"`
		AheadBy int    `json:"ahead_by"`
		HTMLURL string `json:"html_url"`
		Commits []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string    `json:"name"`
					Date time.Time `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Comparison{}, fmt.Errorf("compare the commits of %s: %w", src.Repository(), err)
	}
	out := Comparison{Status: raw.Status, Total: raw.AheadBy, URL: raw.HTMLURL}
	// GitHub lists the oldest first.
	for _, rc := range slices.Backward(raw.Commits) {
		title, _, _ := strings.Cut(strings.TrimSpace(rc.Commit.Message), "\n")
		out.Commits = append(out.Commits, Commit{
			SHA: rc.SHA, Title: safe.Line(title), Author: safe.Line(rc.Commit.Author.Name), Date: rc.Commit.Author.Date,
		})
	}
	return out, nil
}
