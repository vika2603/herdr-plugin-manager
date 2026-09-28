package market

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
)

// Terms splits a query into the lower-case terms Search matches.
func Terms(query string) []string {
	return strings.Fields(strings.ToLower(query))
}

// Field weights for relevance. A term in the plugin's own name or id says
// much more about what it is than one in a description or a topic.
const (
	scoreExact     = 100 // the name, the id or the id's last segment is the term
	scoreWordStart = 60  // a word of the name or id starts with the term
	scoreNameID    = 40  // the name or id contains the term
	scoreDesc      = 20  // the manifest description contains it
	scoreTopic     = 15  // a topic is the term
	scoreRepoDesc  = 10  // the repository description contains it
	scoreTopicPart = 8   // a topic contains it
	scoreSource    = 5   // the owner/repo/subdir source contains it
	scoreLanguage  = 3   // the repository language contains it
)

// Score is how well e matches every term, or false when some term matches
// nowhere. Each term scores the best field it appears in.
func Score(e Entry, terms []string) (int, bool) {
	id := strings.ToLower(e.Manifest.ID)
	name := strings.ToLower(e.Manifest.Name)
	lastID := id[strings.LastIndexAny(id, ".:")+1:]
	desc := strings.ToLower(e.Manifest.Description)
	repoDesc := strings.ToLower(e.Repo.Description)
	src := strings.ToLower(e.Source.String())
	lang := strings.ToLower(e.Repo.Language)

	total := 0
	for _, term := range terms {
		best := 0
		switch {
		case name == term || id == term || lastID == term:
			best = scoreExact
		case wordStart(name, term) || wordStart(id, term):
			best = scoreWordStart
		case strings.Contains(name, term) || strings.Contains(id, term):
			best = scoreNameID
		case strings.Contains(desc, term):
			best = scoreDesc
		}
		if best < scoreTopic {
			for _, topic := range e.Repo.Topics {
				switch topic = strings.ToLower(topic); {
				case topic == term:
					best = max(best, scoreTopic)
				case strings.Contains(topic, term):
					best = max(best, scoreTopicPart)
				}
			}
		}
		switch {
		case best > 0:
		case strings.Contains(repoDesc, term):
			best = scoreRepoDesc
		case strings.Contains(src, term):
			best = scoreSource
		case strings.Contains(lang, term):
			best = scoreLanguage
		default:
			return 0, false
		}
		total += best
	}
	return total, true
}

// wordStart reports whether a word of s, split at anything but a letter or
// digit, starts with term.
func wordStart(s, term string) bool {
	for i := strings.Index(s, term); i >= 0; {
		if i == 0 || !isWordByte(s[i-1]) {
			return true
		}
		next := strings.Index(s[i+1:], term)
		if next < 0 {
			return false
		}
		i += 1 + next
	}
	return false
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b >= 0x80
}

// Search keeps the entries matching every term of query. With ByRelevance
// they are ranked by Score, ties broken by stars; with any other order they
// are sorted by it. Without terms the entries come back as given.
func Search(entries []Entry, query string, by Order) []Entry {
	terms := Terms(query)
	if len(terms) == 0 {
		return entries
	}
	type scored struct {
		entry Entry
		score int
	}
	var hits []scored
	for _, e := range entries {
		if s, ok := Score(e, terms); ok {
			hits = append(hits, scored{e, s})
		}
	}
	if by == ByRelevance {
		slices.SortStableFunc(hits, func(a, b scored) int {
			if c := cmp.Compare(b.score, a.score); c != 0 {
				return c
			}
			return compareBy(a.entry, b.entry, ByPopular)
		})
	}
	out := make([]Entry, len(hits))
	for i, h := range hits {
		out[i] = h.entry
	}
	if by != ByRelevance {
		Sort(out, by)
	}
	return out
}

// ShownDescription is the description to display for e under terms: the
// manifest's own, unless only the repository description mentions a term.
func ShownDescription(e Entry, terms []string) string {
	if e.Manifest.Description == "" || (!containsAny(e.Manifest.Description, terms) && containsAny(e.Repo.Description, terms)) {
		return e.Repo.Description
	}
	return e.Manifest.Description
}

// MatchedTopics lists the topics that contain a term, leaving out the
// herdr-plugin topic every listed repository carries.
func MatchedTopics(e Entry, terms []string) []string {
	var out []string
	for _, topic := range e.Repo.Topics {
		if topic != "herdr-plugin" && containsAny(topic, terms) {
			out = append(out, topic)
		}
	}
	return out
}

func containsAny(s string, terms []string) bool {
	s = strings.ToLower(s)
	for _, term := range terms {
		if strings.Contains(s, term) {
			return true
		}
	}
	return false
}

// Snippet fits text into width runes, keeping the first occurrence of a
// term in view: when the match would fall past the end, the text is cut from
// the front, leaving a third of the width before the match. Cuts are marked
// with "…".
func Snippet(text string, terms []string, width int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if width <= 1 || len(runes) <= width {
		return string(runes)
	}
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	at, end := -1, 0
	for _, term := range terms {
		if i := runeIndex(lower, []rune(term)); i >= 0 && (at < 0 || i < at) {
			at, end = i, i+len([]rune(term))
		}
	}
	start := 0
	if at >= 0 && end > width-1 {
		start = max(min(at-width/3, len(runes)-width+1), 1)
	}
	out := runes[start:]
	prefix := ""
	if start > 0 {
		prefix = "…"
		width--
	}
	if len(out) > width {
		return prefix + string(out[:width-1]) + "…"
	}
	return prefix + string(out)
}

func runeIndex(s, sub []rune) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}
