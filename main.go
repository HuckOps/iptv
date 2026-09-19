package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	var (
		sources   = flag.String("sources", "", "comma-separated m3u URLs (overrides -region)")
		region    = flag.String("region", "", "source region: cn | global (empty = global default)")
		input     = flag.String("input", "", "local m3u file to probe instead of -sources")
		out       = flag.String("out", "live.m3u", "output m3u path for alive channels")
		deadOut   = flag.String("deadout", "", "optional output m3u path for dead channels")
		report    = flag.String("report", "report.json", "output report path (JSON)")
		workers   = flag.Int("workers", 50, "concurrent probes")
		timeout   = flag.Int("timeout", 8, "per-request timeout in seconds")
		retries   = flag.Int("retries", 1, "retries on network error / 5xx")
		insecure  = flag.Bool("insecure", false, "skip TLS certificate verification")
		userAgent = flag.String("ua", defaultUA, "User-Agent header for requests")
		verbose   = flag.Bool("verbose", false, "append channel URI to each probe status line")
		dedup     = flag.Bool("dedup", true, "merge duplicate channels by normalized name (handles CCTV1/CCTV-1 and 简/繁)")
	)
	flag.Parse()

	ctx, stop := signalCtx()
	defer stop()

	// Resolve which source list to probe.
	var srcList []string
	if *sources != "" {
		srcList = splitNonEmpty(*sources, ",")
	} else {
		switch *region {
		case "cn":
			srcList = CnSources
		case "global", "":
			srcList = GlobalSources
		default:
			fatal("unknown region %q (use cn|global)", *region)
		}
	}

	// 1. Collect channels.
	var tracks []Track
	var fetchErrors []string
	var outHeader string
	if *input != "" {
		data, err := os.ReadFile(*input)
		if err != nil {
			fatal("read input: %v", err)
		}
		outHeader, tracks = parseM3UData(data)
	} else {
		client := httpClient(*timeout, *insecure)
		for _, src := range srcList {
			data, err := fetchSource(ctx, client, src)
			if err != nil {
				fetchErrors = append(fetchErrors, fmt.Sprintf("%s: %v", src, err))
				fmt.Fprintf(os.Stderr, "WARN cannot fetch %s: %v\n", src, err)
				continue
			}
			header, ts := parseM3UData(data)
			if outHeader == "" && strings.TrimSpace(header) != "" {
				outHeader = header
			}
			fmt.Printf("fetched %s -> %d channels\n", src, len(ts))
			tracks = append(tracks, ts...)
		}
	}
	if len(tracks) == 0 {
		fatal("no channels found")
	}

	// 2. Deduplicate by URI.
	seen := make(map[string]bool, len(tracks))
	uniq := tracks[:0]
	for _, t := range tracks {
		u := strings.TrimSpace(t.URI)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		t.URI = u
		uniq = append(uniq, t)
	}
	tracks = uniq
	fmt.Printf("probing %d unique channels (workers=%d timeout=%ds)\n", len(tracks), *workers, *timeout)

	// 3. Probe concurrently (status of every channel is logged inline).
	client := httpClient(*timeout, *insecure)
	results := runProbes(ctx, client, tracks, *workers, *timeout, *retries, *userAgent, *verbose)

	// 4. Write outputs.
	alive, dead := splitResults(results)
	outResults := alive
	unifiedCount := len(alive)
	if *dedup {
		outResults = mergeChannels(alive)
		unifiedCount = len(outResults)
	}
	writeM3U(*out, outHeader, outResults)
	fmt.Printf("wrote %s (%d alive", *out, len(alive))
	if *dedup {
		fmt.Printf(", %d after de-duplication", unifiedCount)
	}
	fmt.Printf(")\n")
	if *deadOut != "" && len(dead) > 0 {
		writeM3U(*deadOut, outHeader, dead)
		fmt.Printf("wrote %s (%d dead)\n", *deadOut, len(dead))
	}
	writeReport(*report, srcList, tracks, results, outResults, dead, fetchErrors, unifiedCount)

	// 5. Console summary.
	printSummary(results, unifiedCount)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "FATAL: "+format+"\n", args...)
	os.Exit(1)
}

// signalCtx returns a context cancelled on SIGINT/SIGTERM so an interrupted run
// still writes the partial results it has collected so far.
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
