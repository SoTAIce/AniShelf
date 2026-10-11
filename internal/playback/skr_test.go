package playback

import (
	"anime-shelf/internal/catalog"
	"encoding/base64"
	"net/url"
	"testing"
)

func TestSkrMatchesExactSeasonAndMergesThirdSource(t *testing.T) {
	a := catalog.Anime{ID: 472741, Title: "咒术回战 死灭回游 前篇", Year: 2026}
	search := `<li class="searchlist_item"><a href="/voddetail/247624/"></a><h4 class="vodlist_title"><a><span class="info_right">日漫</span>咒术回战第三季</a></h4><em class="voddate voddate_year">2026</em></li>`
	candidates := skrCandidates(search)
	if len(candidates) != 1 || candidates[0].category != "日漫" || !skrMatches(a, candidates[0]) {
		t.Fatalf("same-season search result not matched: %+v", candidates)
	}
	if skrMatches(a, skrMatch{id: "x", title: "咒术回战第三季", year: 2023}) {
		t.Fatal("wrong release year matched")
	}
	detail := `<h1 class="title">咒术回战第三季</h1><span>年份：</span><a href="/year">2026</a><a href="/vodplay/247624-1-1/">第01集</a><a href="/vodplay/247624-2-1/">第1集</a>`
	extra, err := skrEntry(a, "247624", detail)
	if err != nil || len(extra.Episodes) != 2 || extra.Episodes[0].Source != "skr" {
		t.Fatalf("source detail not parsed: %+v, %v", extra, err)
	}
	base := Entry{AnimeID: a.ID, Episodes: []Episode{{Label: "第01集", Path: "/play/1/1/1", Line: 1}, {Label: "第01集", Path: "/ikun/1/1/1", Line: 1000, Source: "ikun"}}, Lines: map[int]string{1: "AGE", 1000: "iKun"}}
	merged := mergeSkr(base, extra)
	if len(merged.Episodes) != 4 || merged.Lines[2001] == "" || merged.Lines[1000] == "" {
		t.Fatalf("third source was not merged: %+v", merged)
	}
	options := episodeCandidates(merged, merged.Episodes[0])
	if len(options) != 4 || options[1].Source != "ikun" || options[2].Source != "skr" {
		t.Fatalf("cross-site alternatives not prioritized: %+v", options)
	}
	merged = mergeSkr(merged, extra)
	if len(merged.Episodes) != 4 {
		t.Fatal("repeat enrichment duplicated episodes")
	}
}

func TestReportFailureSkipsPreviouslyPreferredLine(t *testing.T) {
	s := &Service{state: View{Items: []Entry{{AnimeID: 7, Episodes: []Episode{{Label: "第01集", Path: "/play/7/1/1"}}}}}, preferred: map[string]string{"7:第1集": "/play/7/1/1"}}
	if !s.ReportFailure(7, "/play/7/1/1") || s.lineFailure("/play/7/1/1") == "" || s.preferred["7:第1集"] != "" {
		t.Fatal("failed media line remains preferred")
	}
	if s.ReportFailure(7, "/play/other/1/1") {
		t.Fatal("uncollected media path accepted")
	}
}

func TestSkrPlayerDecodesDirectHTTPSOnly(t *testing.T) {
	media := "https://cdn.example.com/show/episode-1/index.m3u8"
	encoded := base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(media)))
	actual, err := parseSkrPlayer(`<script>player_aaaa = {"encrypt":2,"url":"` + encoded + `"}</script>`)
	if err != nil || actual != media {
		t.Fatalf("encoded HLS address: %q, %v", actual, err)
	}
	if _, err := parseSkrPlayer(`<script>player_aaaa = {"encrypt":0,"url":"http://localhost/private"}</script>`); err == nil {
		t.Fatal("untrusted non-HTTPS address accepted")
	}
}

func TestSkrReZeroSecondSeasonPartTwoAlias(t *testing.T) {
	a := catalog.Anime{ID: 316247, Title: "Re：从零开始的异世界生活 第二季 后半部分", Year: 2021}
	if !skrMatches(a, skrMatch{id: "72367", title: "Re：从零开始的异世界生活第二季Part2", year: 2021}) {
		t.Fatal("verified 2021 Part2 entry did not match")
	}
	if skrMatches(a, skrMatch{id: "13632", title: "Re：从零开始的异世界生活第二季", year: 2020}) {
		t.Fatal("2020 first cour was mistaken for the second")
	}
}

func TestSkrUncleFromAnotherWorldAlias(t *testing.T) {
	a := catalog.Anime{ID: 339326, Title: "异世界舅舅", Year: 2022}
	if !skrMatches(a, skrMatch{id: "54241", title: "异世界归来的舅舅", year: 2022}) {
		t.Fatal("verified Chinese alias was rejected")
	}
	if skrMatches(a, skrMatch{id: "54241", title: "异世界归来的舅舅", year: 2021}) {
		t.Fatal("wrong-year title was accepted")
	}
}

func TestSkrPerfectBlueAlternateTitle(t *testing.T) {
	a := catalog.Anime{ID: 839, Title: "蓝色恐惧", Year: 1997}
	if !skrMatches(a, skrMatch{title: "未麻的部屋", year: 1997}) || skrMatches(a, skrMatch{title: "未麻的部屋", year: 2001}) {
		t.Fatal("verified movie alias or release year matched incorrectly")
	}
}

func TestSkrBlueLockSecondSeasonAlias(t *testing.T) {
	a := catalog.Anime{ID: 426123, Title: "蓝色监狱 VS. U-20 日本代表队", Year: 2024}
	if !skrMatches(a, skrMatch{title: "蓝色监狱第二季", year: 2024}) || skrMatches(a, skrMatch{title: "蓝色监狱第二季", year: 2022}) {
		t.Fatal("Blue Lock season alias or release year matched incorrectly")
	}
}
