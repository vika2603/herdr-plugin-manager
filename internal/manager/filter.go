package manager

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-plugin-manager/internal/compat"
	"github.com/vika2603/herdr-plugin-manager/internal/source"
	"github.com/vika2603/herdr-plugin-manager/internal/updates"
)

// The states a filter can ask for, of installed plugins and of marketplace
// listings. "update", "current" and "failed" are what the last update check
// found.
var (
	InstalledStates = []string{"enabled", "disabled", "update", "current", "failed", "pinned", "warning", "compatible", "incompatible", "local"}
	ListingStates   = []string{"installed", "compatible", "incompatible"}
)

// Filter keeps the plugins in every state it names: is:<state> keeps those
// in it, and -is:<state> those not.
type Filter struct {
	terms []filterTerm
}

type filterTerm struct {
	state  string
	negate bool
}

// ParseFilter takes the is: terms out of query, which may name the given
// states, and returns the rest of the query as text. err names a state that
// is not one of them; the filter then leaves that term out.
func ParseFilter(query string, states []string) (f Filter, text string, err error) {
	var rest []string
	for field := range strings.FieldsSeq(query) {
		term, negate := strings.CutPrefix(strings.ToLower(field), "-")
		state, ok := strings.CutPrefix(term, "is:")
		if !ok {
			rest = append(rest, field)
			continue
		}
		if !slices.Contains(states, state) {
			if err == nil {
				err = fmt.Errorf("unknown filter %s; is: takes %s", field, strings.Join(states, ", "))
			}
			continue
		}
		f.terms = append(f.terms, filterTerm{state: state, negate: negate})
	}
	return f, strings.Join(rest, " "), err
}

// Empty reports whether the filter keeps everything.
func (f Filter) Empty() bool { return len(f.terms) == 0 }

// Uses reports whether the filter asks for any of states.
func (f Filter) Uses(states ...string) bool {
	return slices.ContainsFunc(f.terms, func(t filterTerm) bool { return slices.Contains(states, t.state) })
}

// Keeps reports whether a plugin for which in says which states it is in
// passes the filter.
func (f Filter) Keeps(in func(state string) bool) bool {
	for _, t := range f.terms {
		if in(t.state) == t.negate {
			return false
		}
	}
	return true
}

// CheckStates are the states that need an update check.
var CheckStates = []string{"update", "current", "failed"}

// InstalledState says which states installed plugin p is in. check is its
// last update check, nil when it has none; herdrVersion and platform are
// what it runs under.
func InstalledState(p herdr.InstalledPluginInfo, check *Checked, herdrVersion, platform string) func(string) bool {
	return func(state string) bool {
		switch state {
		case "enabled":
			return p.Enabled
		case "disabled":
			return !p.Enabled
		case "update":
			return check != nil && check.Err == nil && check.Result.Kind == updates.Available
		case "current":
			return check != nil && check.Err == nil && check.Result.Kind == updates.UpToDate
		case "failed":
			return check != nil && check.Err != nil
		case "pinned":
			return TrackingOf(p).Kind == TrackPinned
		case "warning":
			return len(p.Warnings.ValueOrZero()) > 0
		case "compatible", "incompatible":
			var platforms []string
			for _, pl := range p.Platforms.ValueOrZero() {
				platforms = append(platforms, string(pl))
			}
			fits := len(compat.Problems(platforms, p.MinHerdrVersion.ValueOrZero(), herdrVersion, platform)) == 0
			return fits == (state == "compatible")
		case "local":
			_, github := source.FromInstalled(p)
			return !github
		}
		return false
	}
}

// ListingState says which states a marketplace listing is in: installed
// under its id, and whether its platforms and herdr version fit.
func ListingState(installed bool, platforms []string, minHerdr, herdrVersion, platform string) func(string) bool {
	return func(state string) bool {
		switch state {
		case "installed":
			return installed
		case "compatible", "incompatible":
			fits := len(compat.Problems(platforms, minHerdr, herdrVersion, platform)) == 0
			return fits == (state == "compatible")
		}
		return false
	}
}

// MatchesText reports whether every word of text appears in p's id, name,
// description or source, ignoring case.
func MatchesText(p herdr.InstalledPluginInfo, text string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		p.PluginID, p.Name, p.Description.ValueOrZero(), SourceLabel(p),
	}, "\n"))
	for term := range strings.FieldsSeq(strings.ToLower(text)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}
