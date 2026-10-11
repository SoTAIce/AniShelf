package playback

import (
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestPlaylistRewriteScope(t *testing.T) {
	l, e := newLease("https://cdn.example/anime/01/index.m3u8")
	if e != nil {
		t.Fatal(e)
	}
	b, e := l.playlist("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:5,\n000.ts\n", "token", l.root)
	if e != nil || strings.Count(b, "/api/playback/media/token?") != 2 {
		t.Fatalf("%s %v", b, e)
	}
	for _, raw := range []string{"http://cdn.example/anime/01/a.ts", "https://127.0.0.1/anime/01/a.ts", "https://cdn.example/admin", "https://user:pass@cdn.example/anime/01/a.ts"} {
		u, _ := url.Parse(raw)
		if l.permits(u) {
			t.Fatal("accepted", raw)
		}
	}
	if _, e = l.playlist("#EXTM3U\n../../private.ts\n", "token", l.root); e == nil {
		t.Fatal("accepted out-of-scope path")
	}
}
func TestPublicMediaIPs(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.2", "169.254.169.254", "::1", "fc00::1"} {
		if publicIP(net.ParseIP(ip)) {
			t.Fatal("accepted private IP", ip)
		}
	}
}
func TestUnicodePlaylist(t *testing.T) {
	l, _ := newLease("https://c1.rrcdnbf5.com/video/zangsongdefulilian/第01集/index.m3u8")
	if _, e := l.playlist("#EXTM3U\n0000000.ts\n", "token", l.root); e != nil {
		t.Fatal(e)
	}
}

func TestIkunPlaylistPermitsOnlySameEpisodeCDN(t *testing.T) {
	l, err := newLease("https://bfikuncdn.com/20260109/gq6ZEVtp/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	segment := "https://kkzycdn.com:65/20260109/gq6ZEVtp/2960kb/hls/one.ts"
	u, _ := url.Parse(segment)
	if !l.permits(u) {
		t.Fatal("known segment CDN was rejected")
	}
	for _, raw := range []string{"https://evil.example:65/20260109/gq6ZEVtp/one.ts", "https://kkzycdn.com:65/20260109/other-show/one.ts", "http://kkzycdn.com:65/20260109/gq6ZEVtp/one.ts"} {
		u, _ := url.Parse(raw)
		if l.permits(u) {
			t.Fatal("accepted out-of-scope segment", raw)
		}
	}
	if _, err := l.playlist("#EXTM3U\n"+segment+"\n", "token", l.root); err != nil {
		t.Fatal("known segment CDN was not rewritten:", err)
	}
	playlist := "#EXTM3U\n#EXT-X-DISCONTINUITY\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:3,\n/20261009/other-show/ad.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:4,\n" + segment + "\n"
	rewritten, err := l.playlist(playlist, "token", l.root)
	if err != nil || strings.Contains(rewritten, "other-show") || strings.Count(rewritten, "#EXTINF:") != 1 || strings.Count(rewritten, "#EXT-X-DISCONTINUITY") != 1 {
		t.Fatalf("out-of-scope inserted segment was not omitted: %s %v", rewritten, err)
	}
}

func TestSameHostInsertedSegmentsAreOmitted(t *testing.T) {
	l, err := newLease("https://play.example.com/20230915/show/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	playlist := "#EXTM3U\n#EXTINF:4,\n/20230915/show/956kb/hls/main.ts\n#EXT-X-DISCONTINUITY\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:3,\n/20261009/other/ad1.ts\n#EXTINF:3,\n/20261009/other/ad2.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:4,\n/20230915/show/956kb/hls/next.ts\n"
	rewritten, err := l.playlist(playlist, "token", l.root)
	if err != nil || strings.Contains(rewritten, "other") || strings.Count(rewritten, "#EXTINF:") != 2 || strings.Count(rewritten, "/api/playback/media/token?") != 2 {
		t.Fatalf("same-host inserted segments were not omitted: %s %v", rewritten, err)
	}
	if _, err := l.playlist("#EXTM3U\n#EXTINF:3,\n/other-show/video.ts\n", "token", l.root); err == nil {
		t.Fatal("unmarked out-of-scope media was accepted")
	}
}

func TestCrossCDNInsertedBlockWithoutKeyTagIsOmitted(t *testing.T) {
	l, err := newLease("https://bfikuncdn.com/20221127/NwtUt6hw/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	playlist := "#EXTM3U\n#EXTINF:2,\nhttps://kkzycdn.com:65/20221127/NwtUt6hw/hls/first.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:3,\nhttps://kkzycdn.com:65/20261009/other/ad.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:2,\nhttps://kkzycdn.com:65/20221127/NwtUt6hw/hls/next.ts\n"
	rewritten, err := l.playlist(playlist, "token", l.root)
	if err != nil || strings.Contains(rewritten, "/20261009/") || strings.Count(rewritten, "#EXTINF:") != 2 {
		t.Fatalf("cross-CDN inserted block was not omitted: %s %v", rewritten, err)
	}
}
