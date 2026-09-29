package manager

import (
	"context"
	"strings"
	"sync"
)

// Review is an update as it is reviewed before it runs: the manifest at the
// commit the check found, and what changes.
type Review struct {
	Checked Checked
	Preview *Preview
	Explain Explanation
	// Err is why the preview could not be read.
	Err error
}

// Ready reports whether the update can run: its preview was read and shows
// no problem.
func (r Review) Ready() bool { return r.Err == nil && len(r.Preview.Problems) == 0 }

// Blocker says why the update cannot run, "" when it can.
func (r Review) Blocker() string {
	switch {
	case r.Err != nil:
		return r.Err.Error()
	case len(r.Preview.Problems) > 0:
		return strings.Join(r.Preview.Problems, "; ")
	}
	return ""
}

// Change is the change that applies the update as reviewed: the plugin
// follows the checked ref, and herdr is asked for the commit whose manifest
// was shown.
func (r Review) Change() Change {
	p := r.Checked.Plugin
	return Change{
		Kind: KindUpdate, ID: p.PluginID, Current: &p,
		Target: Target{Source: r.Preview.Source, Ref: r.Preview.Ref, Commit: r.Preview.Commit},
	}
}

// Review reads the manifest an update a check found would install, and
// explains the update.
func (m *Manager) Review(ctx context.Context, ch Checked, herdrVersion string) Review {
	r := Review{Checked: ch}
	res := ch.Result
	r.Preview, r.Err = m.PreviewAt(ctx, res.Source, res.TargetRef, res.TargetCommit, herdrVersion, nil)
	if r.Err != nil {
		return r
	}
	r.Preview.RequireID(ch.Plugin.PluginID)
	r.Explain = m.Explain(ctx, ch.Plugin, r.Preview)
	return r
}

// ReviewAll reviews each update concurrently, keeping the input order.
func (m *Manager) ReviewAll(ctx context.Context, list []Checked, herdrVersion string) []Review {
	out := make([]Review, len(list))
	sem := make(chan struct{}, checkConcurrency)
	var wg sync.WaitGroup
	for i, ch := range list {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i] = m.Review(ctx, ch, herdrVersion)
		})
	}
	wg.Wait()
	return out
}
