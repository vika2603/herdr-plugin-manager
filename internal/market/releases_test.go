package market

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleases(t *testing.T) {
	var auth, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`[
		  {"tag_name": "v2.0.0", "name": "Next", "body": "", "draft": true},
		  {"tag_name": "v1.0.0", "name": "First", "body": "Hello \u001b[2J world", "published_at": "2026-09-01T00:00:00Z", "html_url": "https://github.com/o/r/releases/tag/v1.0.0"}
		]`))
	}))
	defer server.Close()
	c := NewClient("", "test")
	c.HTTP.Transport = rewriteHost{target: server.URL, base: http.DefaultTransport}
	c.Token = "secret"

	list, err := c.Releases(context.Background(), mustSource(t, "o/r/sub"))
	if err != nil {
		t.Fatal(err)
	}
	if path != "/repos/o/r/releases" || auth != "Bearer secret" {
		t.Errorf("requested %q with Authorization %q", path, auth)
	}
	if len(list) != 1 || list[0].Tag != "v1.0.0" || list[0].PublishedAt.IsZero() {
		t.Fatalf("releases = %+v, want v1.0.0 only, drafts left out", list)
	}
	if strings.Contains(list[0].Notes, "\x1b") {
		t.Errorf("notes carry a raw escape: %q", list[0].Notes)
	}
}

func TestReleasesRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()
	c := NewClient("", "test")
	c.HTTP.Transport = rewriteHost{target: server.URL, base: http.DefaultTransport}
	if _, err := c.Releases(context.Background(), mustSource(t, "o/r")); !errors.Is(err, ErrRateLimited) {
		t.Errorf("err = %v, want ErrRateLimited", err)
	}
}
