package market

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/vika2603/herdr-client/plugin/manifest"

	"github.com/vika2603/herdr-plugin-manager/internal/safe"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
)

// DefaultMaxAge matches how often herdr.dev regenerates the index.
const DefaultMaxAge = 30 * time.Minute

// maxBody bounds a downloaded document. The index is about 1 MB today.
const maxBody = 64 << 20

const cacheFile = "index.json"

// Client downloads the index and manifests. The zero value is not usable;
// build one with NewClient.
type Client struct {
	HTTP     *http.Client
	IndexURL string
	// CacheDir holds the last downloaded index. Empty disables caching.
	CacheDir string
	MaxAge   time.Duration
	// UserAgent identifies the manager to herdr.dev and GitHub.
	UserAgent string
	// Token authenticates requests to GitHub's API, which raises its rate
	// limit. Empty sends none.
	Token string
	Now   func() time.Time
}

// NewClient returns a client for the public index, caching under cacheDir.
func NewClient(cacheDir, userAgent string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		IndexURL:  IndexURL,
		CacheDir:  cacheDir,
		MaxAge:    DefaultMaxAge,
		UserAgent: userAgent,
		Now:       time.Now,
	}
}

// Status describes where a loaded index came from.
type Status struct {
	// FetchedAt is when the index was downloaded, possibly by an earlier run.
	FetchedAt time.Time
	// FromCache is set when no download happened in this call.
	FromCache bool
	// FetchErr is the download failure when a stale cached copy was used
	// instead.
	FetchErr error
}

// Load returns the index, from the cache while it is younger than MaxAge
// unless refresh is set. A failed download falls back to any cached copy and
// reports the failure in Status.FetchErr.
func (c *Client) Load(ctx context.Context, refresh bool) (*Index, Status, error) {
	cached, cachedAt, cacheErr := c.readCache()
	if cacheErr == nil && !refresh && c.Now().Sub(cachedAt) < c.MaxAge {
		return cached, Status{FetchedAt: cachedAt, FromCache: true}, nil
	}

	data, err := c.get(ctx, c.IndexURL)
	if err == nil {
		var ix *Index
		if ix, err = decodeIndex(data); err == nil {
			// A cache that cannot be written only costs a download next time.
			_ = c.writeCache(data)
			return ix, Status{FetchedAt: c.Now()}, nil
		}
	}
	if cacheErr == nil {
		return cached, Status{FetchedAt: cachedAt, FromCache: true, FetchErr: err}, nil
	}
	return nil, Status{}, fmt.Errorf("download plugin index: %w", err)
}

// Cached returns the cached index however old it is, without downloading.
func (c *Client) Cached() (*Index, bool) {
	ix, _, err := c.readCache()
	return ix, err == nil
}

// Manifest downloads and validates the manifest of src at ref, which may be a
// branch, tag, commit or empty for the default branch. Validation applies the
// rules herdr applies when it loads the manifest; warnings are returned
// separately.
func (c *Client) Manifest(ctx context.Context, src source.GitHub, ref string) (*manifest.Manifest, []string, error) {
	data, err := c.get(ctx, src.ManifestURL(ref))
	if err != nil {
		return nil, nil, fmt.Errorf("download manifest of %s: %w", src, err)
	}
	m, err := manifest.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("manifest of %s: %w", src, err)
	}
	warnings, err := m.Validate()
	if err != nil {
		return m, nil, fmt.Errorf("manifest of %s: %w", src, err)
	}
	return m, warnings, nil
}

// ErrNotFound is returned for a file the server does not have.
var ErrNotFound = errors.New("not found")

// File downloads path, relative to the repository root, from src at ref.
func (c *Client) File(ctx context.Context, src source.GitHub, ref, path string) ([]byte, error) {
	return c.get(ctx, src.RawURL(ref, path))
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	return c.fetch(ctx, url, nil)
}

// fetch downloads url; header, when set, adds request headers.
func (c *Client) fetch(ctx context.Context, url string, header func(http.Header)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if header != nil {
		header(req.Header)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("GET %s: %w", url, ErrNotFound)
	case resp.StatusCode == http.StatusTooManyRequests || resp.Header.Get("X-RateLimit-Remaining") == "0":
		return nil, fmt.Errorf("GET %s: %w", url, ErrRateLimited)
	default:
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("GET %s: response larger than %d bytes", url, maxBody)
	}
	return data, nil
}

func decodeIndex(data []byte) (*Index, error) {
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil, fmt.Errorf("decode plugin index: %w", err)
	}
	if ix.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("plugin index schema version %d is not supported (expected %d)", ix.SchemaVersion, SchemaVersion)
	}
	ix.sanitize()
	return &ix, nil
}

// sanitize makes the text fields printable. The index repeats what plugin
// authors wrote in their repositories and manifests; the owner and repository
// names are GitHub's own and are left alone because install uses them.
func (ix *Index) sanitize() {
	for i := range ix.Repositories {
		r := &ix.Repositories[i]
		r.Description, r.Language = safe.Line(r.Description), safe.Line(r.Language)
		for j := range r.Topics {
			r.Topics[j] = safe.Line(r.Topics[j])
		}
		for j := range r.Manifests {
			m := &r.Manifests[j]
			m.ID, m.Name, m.Version = safe.Line(m.ID), safe.Line(m.Name), safe.Line(m.Version)
			m.Description, m.MinHerdrVersion = safe.Line(m.Description), safe.Line(m.MinHerdrVersion)
			for k := range m.Platforms {
				m.Platforms[k] = safe.Line(m.Platforms[k])
			}
		}
	}
}

func (c *Client) readCache() (*Index, time.Time, error) {
	if c.CacheDir == "" {
		return nil, time.Time{}, errors.New("no cache directory")
	}
	path := filepath.Join(c.CacheDir, cacheFile)
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // The cache file inside the manager's own cache directory.
	if err != nil {
		return nil, time.Time{}, err
	}
	ix, err := decodeIndex(data)
	if err != nil {
		return nil, time.Time{}, err
	}
	return ix, info.ModTime(), nil
}

// writeCache replaces the cached index through a rename so a concurrent
// reader never sees a partial file.
func (c *Client) writeCache(data []byte) error {
	if c.CacheDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.CacheDir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.CacheDir, cacheFile+".*")
	if err != nil {
		return err
	}
	// After the rename the temporary name no longer exists, so this only
	// cleans up a failed write.
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(c.CacheDir, cacheFile))
}
