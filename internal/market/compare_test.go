package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"status": "ahead", "ahead_by": 2, "html_url": "https://github.com/o/r/compare/a...b", "commits": [
		  {"sha": "c1", "commit": {"message": "Fix the title\n\nLonger text", "author": {"name": "Ann", "date": "2026-09-01T00:00:00Z"}}},
		  {"sha": "c2", "commit": {"message": "Add \u001b[2J colours", "author": {"name": "Bo", "date": "2026-09-02T00:00:00Z"}}}
		]}`))
	}))
	defer server.Close()
	c := NewClient("", "test")
	c.HTTP.Transport = rewriteHost{target: server.URL, base: http.DefaultTransport}
	cmp, err := c.Compare(context.Background(), mustSource(t, "o/r/sub"), "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/repos/o/r/compare/a...b" {
		t.Errorf("requested %q", path)
	}
	if cmp.Total != 2 || len(cmp.Commits) != 2 || cmp.Commits[0].SHA != "c2" || cmp.Commits[1].Title != "Fix the title" {
		t.Fatalf("comparison = %+v, want the newest first with message titles", cmp)
	}
	if strings.Contains(cmp.Commits[0].Title, "\x1b") {
		t.Errorf("a raw escape reached the title: %q", cmp.Commits[0].Title)
	}
}
