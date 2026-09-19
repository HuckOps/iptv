package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// splitResults partitions results into alive and dead, preserving input order.
func splitResults(results []Result) (alive, dead []Result) {
	for _, r := range results {
		if r.Alive {
			alive = append(alive, r)
		} else {
			dead = append(dead, r)
		}
	}
	return
}

// writeM3U writes an M3U file with only the given results.
func writeM3U(filename, header string, results []Result) {
	var b strings.Builder
	if header != "" {
		b.WriteString("#EXTM3U " + header + "\n")
	} else {
		b.WriteString("#EXTM3U\n")
	}
	for _, r := range results {
		b.WriteString("#EXTINF:" + r.Track.RawAttrs + "," + r.Track.Name + "\n")
		b.WriteString(r.Track.URI + "\n")
	}
	if err := os.WriteFile(filename, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "WARN cannot write %s: %v\n", filename, err)
	}
}

// writeReport emits a JSON summary.
func writeReport(filename string, sources []string, tracks []Track, results, outResults, dead []Result, fetchErrors []string, unified int) {
	alive, dead := splitResults(results)
	byGroup := map[string]map[string]int{}
	for _, r := range results {
		g := r.Group
		if g == "" {
			g = "Unknown"
		}
		if byGroup[g] == nil {
			byGroup[g] = map[string]int{"total": 0, "alive": 0}
		}
		byGroup[g]["total"]++
		if r.Alive {
			byGroup[g]["alive"]++
		}
	}

	groups := make([]string, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return byGroup[groups[i]]["total"] > byGroup[groups[j]]["total"]
	})

	type chJSON struct {
		Name   string `json:"name"`
		URI    string `json:"uri"`
		Group  string `json:"group"`
		Status int    `json:"status"`
		Ms     int64  `json:"latency_ms"`
		Alive  bool   `json:"alive"`
	}
	type groupStat struct {
		Group string `json:"group"`
		Total int    `json:"total"`
		Alive int    `json:"alive"`
	}
	type report struct {
		GeneratedAt       string      `json:"generated_at"`
		Sources           []string    `json:"sources"`
		FetchErrors       []string    `json:"fetch_errors"`
		Total             int         `json:"total"`
		Alive             int         `json:"alive"`
		Dead              int         `json:"dead"`
		Unified           int         `json:"unified_channels"`
		DuplicatesMerged  int         `json:"duplicates_merged"`
		ByGroup           []groupStat `json:"by_group"`
		Channels          []chJSON    `json:"channels"`
	}
	rp := report{
		GeneratedAt:      time.Now().Format(time.RFC3339),
		Sources:          sources,
		FetchErrors:      fetchErrors,
		Total:            len(tracks),
		Alive:            len(alive),
		Dead:             len(dead),
		Unified:          unified,
		DuplicatesMerged: len(alive) - unified,
	}
	for _, g := range groups {
		rp.ByGroup = append(rp.ByGroup, groupStat{Group: g, Total: byGroup[g]["total"], Alive: byGroup[g]["alive"]})
	}
	// All channels in one list: unified alive first, then dead; each flagged.
	all := make([]chJSON, 0, len(outResults)+len(dead))
	for _, r := range outResults {
		all = append(all, chJSON{r.Track.Name, r.Track.URI, r.Group, r.Status, r.LatencyMs, true})
	}
	for _, r := range dead {
		all = append(all, chJSON{r.Track.Name, r.Track.URI, r.Group, r.Status, r.LatencyMs, false})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Group != all[j].Group {
			return all[i].Group < all[j].Group
		}
		return all[i].Name < all[j].Name
	})
	rp.Channels = all

	if err := writeJSON(filename, rp); err != nil {
		fmt.Fprintf(os.Stderr, "WARN cannot write report %s: %v\n", filename, err)
	} else {
		fmt.Printf("wrote %s\n", filename)
	}
}

func printSummary(results []Result, unified int) {
	alive, dead := splitResults(results)
	fmt.Println("==================== SUMMARY ====================")
	fmt.Printf("Total : %d\n", len(results))
	fmt.Printf("Alive : %d\n", len(alive))
	fmt.Printf("Dead  : %d\n", len(dead))
	if unified >= 0 && unified != len(alive) {
		fmt.Printf("Unified: %d  (merged %d duplicate channels)\n", unified, len(alive)-unified)
	}
	if len(results) > 0 {
		fmt.Printf("Rate  : %.1f%%\n", float64(len(alive))/float64(len(results))*100)
	}
	fmt.Println("================================================")
}

// writeJSON marshals v to indented JSON and writes it to filename.
func writeJSON(filename string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0o644)
}
