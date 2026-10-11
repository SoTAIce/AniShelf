package playback

import (
	"anime-shelf/internal/catalog"
	"context"
	"testing"
)

func TestParseSeasonAndEpisodes(t *testing.T) {
	body := `<h2 class="video_detail_title">葬送的芙莉莲</h2><span>原版名称：</span><span>葬送のフリーレン</span><button data-bs-target="#playlist-source-xigua">VIP 西瓜</button><a href="/play/20230207/1/1">第01集</a><a href="/play/20230207/2/1">第01集</a><a href="/play/999/1/1">其他作品</a>`
	a := catalog.Anime{ID: 400602, Title: "葬送的芙莉莲", Native: "葬送のフリーレン"}
	entry, e := parse(a, "20230207", body)
	if e != nil || len(entry.Episodes) != 2 || len(entry.Lines) != 2 {
		t.Fatalf("parse: %+v %v", entry, e)
	}
	a.Title = "葬送的芙莉莲 第二季"
	a.Native = "葬送のフリーレン 第2期"
	if _, e = parse(a, "20230207", body); e == nil {
		t.Fatal("matched wrong season")
	}
}
func TestRejectUncollectedPath(t *testing.T) {
	s := &Service{state: View{Items: []Entry{}}}
	if _, e := s.Resolve(context.Background(), 400602, "https://example.com"); e == nil {
		t.Fatal("accepted arbitrary URL")
	}
}

func TestEpisodeKeyKeepsSameEpisodeAcrossLineLabels(t *testing.T) {
	if episodeKey("第01集") != episodeKey("第1集") {
		t.Fatal("same episode was split by a leading zero")
	}
	if episodeKey("第01话") != episodeKey("第1集") || episodeKey("第01回") != episodeKey("第1集") {
		t.Fatal("same episode was split by source-specific units")
	}
	if episodeKey("第01集") == episodeKey("第10集") {
		t.Fatal("different episodes were merged")
	}
	entry := Entry{Episodes: []Episode{{Label: "第01集", Path: "/play/1/1/1", Line: 1}, {Label: "第01集", Path: "/play/1/2/1", Line: 2}, {Label: "第1集", Path: "/play/1/3/1", Line: 3}, {Label: "第10集", Path: "/play/1/4/10", Line: 4}}}
	candidates := episodeCandidates(entry, entry.Episodes[0])
	if len(candidates) != 3 || candidates[2].Line != 3 {
		t.Fatalf("alternate line was missed: %+v", candidates)
	}
}

func TestMovieLabelsUseSameFilmWithoutMixingDubs(t *testing.T) {
	if !sameEpisodeLabel("日语版", "正片") || !sameEpisodeLabel("全集", "正片") || !sameEpisodeLabel("全集", "HD中字") || !sameEpisodeLabel("日语版", "HD中字") || !sameEpisodeLabel("日语版", "HD高清日语版") || !sameEpisodeLabel("国语版", "HD国语") || !sameEpisodeLabel("国语版", "HD高清国语版") {
		t.Fatal("same movie edition was not grouped")
	}
	if sameEpisodeLabel("日语版", "国语版") || sameEpisodeLabel("第01集", "第02集") {
		t.Fatal("different audio edition or episode was grouped")
	}
	entry := Entry{Episodes: []Episode{{Label: "日语版", Path: "/play/1/1/1", Line: 1}, {Label: "国语版", Path: "/play/1/1/2", Line: 1}, {Label: "正片", Path: "/ikun/1/0/0", Line: 1000, Source: "ikun"}}}
	candidates := episodeCandidates(entry, entry.Episodes[0])
	if len(candidates) != 2 || candidates[1].Source != "ikun" {
		t.Fatalf("movie fallback not found: %+v", candidates)
	}
}

func TestRejectedLineIsCachedWithoutBlockingOtherLines(t *testing.T) {
	s := &Service{}
	s.rememberLineFailure("/play/show/2/1", "视频返回 HTTP 403")
	if s.lineFailure("/play/show/2/1") == "" {
		t.Fatal("rejected line was not cached")
	}
	if s.lineFailure("/play/show/3/1") != "" {
		t.Fatal("different line was blocked")
	}
}
