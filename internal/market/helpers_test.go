package market

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

func ids(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Manifest.ID
	}
	return out
}

func mustSource(t *testing.T, s string) source.GitHub {
	t.Helper()
	g, err := source.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// rewriteHost sends every request to target, keeping the path.
type rewriteHost struct {
	target string
	base   http.RoundTripper
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(r.target)
	if err != nil {
		return nil, err
	}
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host = u.Scheme, u.Host
	return r.base.RoundTrip(out)
}
