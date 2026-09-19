package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/grafov/m3u8"
)

// httpClient builds a client honoring the timeout and TLS settings.
func httpClient(timeout int, insecure bool) *http.Client {
	t := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: insecure},
		DisableKeepAlives:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: time.Duration(timeout) * time.Second,
	}
	return &http.Client{
		Timeout:   time.Duration(timeout) * time.Second,
		Transport: t,
	}
}

// fetchSource downloads an m3u from a URL (or reads a local file path).
func fetchSource(ctx context.Context, client *http.Client, src string) ([]byte, error) {
	if u, err := url.Parse(src); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", defaultUA)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(src)
}

// runProbes launches a worker pool and returns results in input order.
// Each channel's probe status is printed as a single log line (no progress
// bar), so the full picture is visible in the run log / CI output.
func runProbes(ctx context.Context, client *http.Client, tracks []Track, workers, timeout, retries int, ua string, verbose bool) []Result {
	results := make([]Result, len(tracks))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, t := range tracks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t Track) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					results[i] = Result{
						Track:  t,
						Alive:  false,
						Reason: fmt.Sprintf("panic: %v", r),
						Group:  groupTitle(t.RawAttrs),
					}
					logProbe(&mu, false, 0, 0, groupTitle(t.RawAttrs), t.Name, "panic: "+fmt.Sprintf("%v", r), t.URI, verbose)
				}
			}()

			start := time.Now()
			alive, status, reason := probeWithRetry(ctx, client, t.URI, timeout, retries, ua)
			latency := time.Since(start).Milliseconds()

			results[i] = Result{
				Track:     t,
				Alive:     alive,
				Reason:    reason,
				Status:    status,
				LatencyMs: latency,
				Group:     groupTitle(t.RawAttrs),
			}

			logProbe(&mu, alive, status, latency, groupTitle(t.RawAttrs), t.Name, reason, t.URI, verbose)
		}(i, t)
	}
	wg.Wait()
	return results
}

// logProbe writes one consistent status line for a probed channel. The mutex
// keeps lines from interleaving in the log. When verbose is true the channel
// URI is appended.
func logProbe(mu *sync.Mutex, alive bool, status int, latency int64, group, name, reason, uri string, verbose bool) {
	state := "OK  "
	if !alive {
		state = "DEAD"
	}
	extra := ""
	if !alive && reason != "" {
		extra = " (" + reason + ")"
	}
	mu.Lock()
	if verbose {
		fmt.Printf("[%-4s] %4d %5dms [%s] %s%s  %s\n", state, status, latency, group, name, extra, uri)
	} else {
		fmt.Printf("[%-4s] %4d %5dms [%s] %s%s\n", state, status, latency, group, name, extra)
	}
	mu.Unlock()
}

// probeWithRetry runs probeURL up to retries+1 times.
func probeWithRetry(ctx context.Context, client *http.Client, u string, timeout, retries int, ua string) (bool, int, string) {
	var alive bool
	var status int
	var reason string
	for attempt := 0; attempt <= retries; attempt++ {
		alive, status, reason = probeURL(ctx, client, u, ua, 0)
		if alive {
			return true, status, reason
		}
		if status == 0 || (status >= 500 && status < 600) {
			if attempt < retries {
				time.Sleep(300 * time.Millisecond)
				continue
			}
		}
		break
	}
	return alive, status, reason
}

// probeURL performs one HTTP probe. For HLS it uses the github.com/grafov/m3u8
// library to properly distinguish master vs media playlists and to follow the
// first variant of a master playlist; a heuristic fallback covers any playlist
// the strict parser refuses.
func probeURL(ctx context.Context, client *http.Client, u, ua string, depth int) (bool, int, string) {
	if depth > 3 {
		return false, 0, "max recursion depth"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, 0, "bad url: " + err.Error()
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Range", "bytes=0-")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return false, 0, netReason(err)
	}
	defer resp.Body.Close()

	status := resp.StatusCode
	if status != http.StatusOK && status != http.StatusPartialContent {
		return false, status, fmt.Sprintf("HTTP %d", status)
	}

	buf := make([]byte, 64*1024)
	n, _ := io.ReadAtLeast(resp.Body, buf, 1)
	body := buf[:n]
	ct := strings.ToLower(resp.Header.Get("Content-Type"))

	isHLS := strings.HasSuffix(strings.ToLower(u), ".m3u8") ||
		strings.Contains(ct, "mpegurl") ||
		bytesHasPrefix(body, []byte("#EXTM3U"))

	if isHLS {
		text := string(body)

		// Proper HLS parsing via grafov/m3u8 (lenient mode). The library is
		// known to panic on a few malformed real-world playlists, so we wrap it
		// in a recover and fall back to heuristics on any failure.
		if pl, listType, perr := decodeM3U8(text); perr == nil {
			switch listType {
			case m3u8.MASTER:
				mp := pl.(*m3u8.MasterPlaylist)
				if len(mp.Variants) == 0 {
					return false, status, "master playlist without variants"
				}
				v := resolveURL(u, mp.Variants[0].URI)
				return probeURL(ctx, client, v, ua, depth+1)
			case m3u8.MEDIA:
				mp := pl.(*m3u8.MediaPlaylist)
				if len(mp.Segments) > 0 || mp.Closed {
					return true, status, "hls media playlist"
				}
				return true, status, "hls media playlist (no segments)"
			}
		}

		// Heuristic fallback when m3u8 cannot decode the body.
		if strings.Contains(text, "#EXT-X-STREAM-INF") {
			if v, ok := firstVariant(text, u); ok {
				return probeURL(ctx, client, v, ua, depth+1)
			}
			return false, status, "master playlist without variant"
		}
		if strings.Contains(text, "#EXTINF") ||
			strings.Contains(text, "#EXT-X-ENDLIST") ||
			strings.Contains(text, ".ts") ||
			strings.Contains(text, ".m4s") ||
			strings.Contains(text, "#EXT-X-MEDIA") {
			return true, status, "hls playlist"
		}
		if strings.Contains(text, "#EXTM3U") {
			return true, status, "hls playlist"
		}
		return false, status, "not a valid playlist"
	}

	if n > 0 {
		return true, status, "media data"
	}
	return false, status, "empty body"
}

// decodeM3U8 wraps m3u8.DecodeFrom with a recover so a single malformed
// playlist can never crash the whole probe run. On panic it returns an error
// and the caller falls back to heuristic checks.
func decodeM3U8(text string) (pl m3u8.Playlist, lt m3u8.ListType, err error) {
	defer func() {
		if r := recover(); r != nil {
			pl, lt, err = nil, 0, fmt.Errorf("m3u8 decode panic: %v", r)
		}
	}()
	return m3u8.DecodeFrom(strings.NewReader(text), false)
}

// firstVariant returns the first variant URI from an HLS master playlist
// (used only as a fallback when m3u8 decoding fails).
func firstVariant(text, base string) (string, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			if i+1 < len(lines) {
				uri := strings.TrimSpace(lines[i+1])
				if uri != "" && !strings.HasPrefix(uri, "#") {
					return resolveURL(base, uri), true
				}
			}
		}
	}
	return "", false
}

// resolveURL resolves a possibly-relative URI against a base URL.
func resolveURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func bytesHasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

func netReason(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context deadline") || strings.Contains(msg, "Client.Timeout"):
		return "timeout"
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "dns"):
		return "dns error"
	case strings.Contains(msg, "refused"):
		return "connection refused"
	case strings.Contains(msg, "TLS"):
		return "tls error"
	default:
		return msg
	}
}
