package main

// Track is a single channel parsed from an M3U playlist.
// RawAttrs keeps the original attribute string between "#EXTINF:" and the ","
// so we can reconstruct the line when writing the output.
type Track struct {
	RawAttrs string // e.g. `-1 tvg-id="x" group-title="y"`
	Name     string // channel name after the comma
	URI      string // stream URL on the following line
}

// Result holds the probe outcome for one Track.
type Result struct {
	Track     Track
	Alive     bool
	Reason    string
	Status    int
	LatencyMs int64
	Group     string
}

const defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

// CnSources are China-region (合规) IPTV sources, maintained as one task.
// Extend this list with additional China-compliant playlists as needed.
var CnSources = []string{
	"https://iptv-org.github.io/iptv/countries/cn.m3u",
	"https://gh-proxy.com/raw.githubusercontent.com/vbskycn/iptv/refs/heads/master/tv/iptv4.m3u",
	"https://raw.githubusercontent.com/Guovin/iptv-api/gd/output/ipv6/result.m3u",
	"https://raw.githubusercontent.com/YueChan/Live/refs/heads/main/IPTV.m3u",
}

// GlobalSources are worldwide IPTV sources, maintained as a separate task.
var GlobalSources = []string{
	"https://iptv-org.github.io/iptv/index.m3u",
	"https://raw.githubusercontent.com/Free-TV/IPTV/master/playlist.m3u8",
}

// DefaultSources is used when neither -region nor -sources is given.
var DefaultSources = GlobalSources
