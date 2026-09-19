package main

import (
	"regexp"
	"sort"
	"strings"

	"github.com/longbridgeapp/opencc"
)

// ---- channel normalization & de-duplication ----

// t2sConv converts Traditional Chinese to Simplified Chinese so that sources
// using different scripts for the same station (e.g. 中央電視台 vs 中央电视台)
// are recognised as one. Initialised once; if the dictionary fails to load we
// simply skip conversion rather than crashing.
var t2sConv *opencc.OpenCC

func init() {
	if c, err := opencc.New("t2s"); err == nil {
		t2sConv = c
	}
}

func toSimplified(s string) string {
	if t2sConv == nil {
		return s
	}
	if out, err := t2sConv.Convert(s); err == nil {
		return out
	}
	return s
}

// sepRe matches separators that differ between equivalent channel names
// (CCTV1 vs CCTV-1 vs CCTV 1).
var sepRe = regexp.MustCompile(`[\s\-_./()（）·•|]+`)

// normalizeKey returns a canonical comparison key for a channel name:
// Traditional->Simplified, lower-cased, separators removed.
func normalizeKey(name string) string {
	s := toSimplified(name)
	s = strings.ToLower(s)
	s = sepRe.ReplaceAllString(s, "")
	return s
}

// setAttr sets or replaces a quoted `key="value"` attribute inside an EXTINF
// attribute string, appending it when absent.
func setAttr(raw, key, value string) string {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(key) + `="[^"]*"`)
	repl := key + `="` + value + `"`
	if re.MatchString(raw) {
		return re.ReplaceAllString(raw, repl)
	}
	if raw == "" {
		return repl
	}
	return raw + " " + repl
}

// hasSep reports whether s contains a name-separator (preferred for a "clean"
// canonical name over a run-together form like CCTV1).
func hasSep(s string) bool {
	return strings.ContainsAny(s, "-_ ")
}

// pickName prefers a separated form (CCTV-1) over a concatenated one (CCTV1),
// then the longer, then keeps the existing choice.
func pickName(existing, candidate string) string {
	ce, cc := hasSep(existing), hasSep(candidate)
	switch {
	case cc && !ce:
		return candidate
	case ce && !cc:
		return existing
	case len(candidate) > len(existing):
		return candidate
	default:
		return existing
	}
}

// majorityGroup returns the most common (Simplified) group, falling back to the
// first entry on a tie.
func majorityGroup(groups map[string]int) string {
	best := ""
	bestN := -1
	for g, n := range groups {
		if n > bestN {
			best, bestN = g, n
		}
	}
	if best == "" {
		return "Unknown"
	}
	return best
}

// mergeChannels collapses alive results that represent the same station into a
// single entry. Identity is the normalized name (handles CCTV1/CCTV-1 and
// 简/繁); the output uses one canonical name + group and the lowest-latency URL.
func mergeChannels(alive []Result) []Result {
	type bucket struct {
		key      string
		best     Result
		prefName string
		groups   map[string]int
		order    int
	}
	m := make(map[string]*bucket)
	order := 0
	for _, r := range alive {
		key := normalizeKey(r.Track.Name)
		if key == "" {
			key = r.Track.URI // last-resort identity for nameless entries
		}
		b, ok := m[key]
		if !ok {
			b = &bucket{key: key, groups: map[string]int{}, order: order, prefName: r.Track.Name}
			order++
			m[key] = b
			b.best = r
		} else {
			if b.best.Track.URI == "" || r.LatencyMs < b.best.LatencyMs {
				b.best = r
			}
			b.prefName = pickName(b.prefName, r.Track.Name)
		}
		g := toSimplified(r.Group)
		if g == "" {
			g = "Unknown"
		}
		b.groups[g]++
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]].order < m[keys[j]].order })

	out := make([]Result, 0, len(m))
	for _, k := range keys {
		b := m[k]
		best := b.best
		canonicalName := toSimplified(b.prefName)
		canonicalGroup := majorityGroup(b.groups)
		raw := setAttr(best.Track.RawAttrs, "group-title", canonicalGroup)
		raw = setAttr(raw, "tvg-name", canonicalName)
		out = append(out, Result{
			Track:     Track{RawAttrs: raw, Name: canonicalName, URI: best.Track.URI},
			Alive:     true,
			Reason:    best.Reason,
			Status:    best.Status,
			LatencyMs: best.LatencyMs,
			Group:     canonicalGroup,
		})
	}
	// Sort the unified playlist by group then name for a clean output.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Track.Name < out[j].Track.Name
	})
	return out
}
