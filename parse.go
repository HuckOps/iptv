package main

import (
	"fmt"
	"os"
	"strings"

	m3u "github.com/jamesnetherton/m3u"
)

// parseM3UData parses an M3U document. It tries the third-party
// github.com/jamesnetherton/m3u library first (preserves tvg-id, group-title,
// tvg-logo as tags), and falls back to a lenient hand-rolled parser if the
// library rejects the file (some real-world playlists are non-standard and
// would otherwise cause a total parse failure on thousands of channels).
func parseM3UData(data []byte) (headerAttrs string, tracks []Track) {
	headerAttrs = extractHeader(data)

	// Primary path: write to a temp file and let the m3u library parse it.
	tmp, err := os.CreateTemp("", "iptv-*.m3u")
	if err == nil {
		_, _ = tmp.Write(data)
		tmp.Close()
		defer os.Remove(tmp.Name())
		if pl, perr := m3u.Parse(tmp.Name()); perr == nil {
			for _, mt := range pl.Tracks {
				tracks = append(tracks, fromM3UTrack(mt))
			}
		}
	}
	if len(tracks) > 0 {
		return headerAttrs, tracks
	}

	// Fallback path: tolerant parser that never aborts on a single bad line.
	return headerAttrs, parseM3ULenient(data)
}

// extractHeader returns the attribute string after "#EXTM3U" on the first line
// (e.g. ` url-tvg="..."`), used to rebuild the output playlist header.
func extractHeader(data []byte) string {
	first := strings.SplitN(string(data), "\n", 2)[0]
	first = strings.TrimRight(first, "\r")
	if strings.HasPrefix(first, "#EXTM3U") {
		return strings.TrimSpace(strings.TrimPrefix(first, "#EXTM3U"))
	}
	return ""
}

// fromM3UTrack converts a jamesnetherton/m3u.Track into our Track.
func fromM3UTrack(mt m3u.Track) Track {
	attrs := tagString(mt.Tags)
	raw := fmt.Sprintf("%d", mt.Length)
	if attrs != "" {
		raw += " " + attrs
	}
	return Track{RawAttrs: raw, Name: mt.Name, URI: mt.URI}
}

// tagString re-serialises the parsed EXTINF tags as `key="value"` pairs.
func tagString(tags []m3u.Tag) string {
	parts := make([]string, 0, len(tags))
	for _, t := range tags {
		parts = append(parts, fmt.Sprintf("%s=%q", t.Name, t.Value))
	}
	return strings.Join(parts, " ")
}

// parseM3ULenient is the fallback parser: it tolerates minor deviations and
// preserves the original attribute string verbatim.
func parseM3ULenient(data []byte) (tracks []Track) {
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		rest := strings.TrimPrefix(line, "#EXTINF:")
		idx := strings.Index(rest, ",")
		if idx < 0 {
			continue
		}
		attrs := rest[:idx]
		name := rest[idx+1:]
		uri := ""
		for j := i + 1; j < len(lines); j++ {
			nxt := strings.TrimSpace(lines[j])
			if nxt == "" || strings.HasPrefix(nxt, "#") {
				continue
			}
			uri = nxt
			i = j
			break
		}
		if uri != "" {
			tracks = append(tracks, Track{RawAttrs: attrs, Name: name, URI: uri})
		}
	}
	return
}

// groupTitle extracts the group-title attribute from RawAttrs.
func groupTitle(raw string) string {
	return attr(raw, "group-title")
}

func attr(raw, key string) string {
	marker := key + "="
	idx := strings.Index(raw, marker)
	if idx < 0 {
		return ""
	}
	rest := raw[idx+len(marker):]
	if len(rest) > 0 && rest[0] == '"' {
		end := strings.Index(rest[1:], "\"")
		if end >= 0 {
			return rest[1 : end+1]
		}
		return ""
	}
	end := strings.IndexAny(rest, " \t,")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
