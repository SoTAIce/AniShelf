package playback

import (
	"anime-shelf/internal/catalog"
	"testing"
)

func TestIkunMatchesExactSeasonAndYear(t *testing.T) {
	a := catalog.Anime{ID: 472741, Title: "咒术回战 死灭回游 前篇", Native: "呪術廻戦 死滅回游 前編", Year: 2026}
	v := ikunVideo{ID: 66961, Name: "咒术回战第三季", Aliases: "呪術廻戦 死滅回游 前編,咒术回战 死灭回游 前篇", Year: "2026", Type: "日本动漫"}
	if !ikunMatches(a, v) {
		t.Fatal("exact same-season alias should match")
	}
	v.Year = "2023"
	if ikunMatches(a, v) {
		t.Fatal("wrong release year should not match")
	}
	v.Year = "2026"
	v.Aliases = "咒术回战 死灭回游 后篇"
	if ikunMatches(a, v) {
		t.Fatal("wrong part should not match")
	}
}

func TestIkunAddsAlternativeSourceWithoutDuplicatingAge(t *testing.T) {
	a := catalog.Anime{ID: 472741, Title: "咒术回战 死灭回游 前篇"}
	v := ikunVideo{ID: 66961, Name: "咒术回战第三季", PlayFrom: "ikm3u8", PlayURL: "第01集$https://cdn.example.com/show/1/index.m3u8#第02集$https://cdn.example.com/show/2/index.m3u8"}
	extra, err := ikunEntry(a, v)
	if err != nil || len(extra.Episodes) != 2 || extra.Episodes[0].Source != "ikun" {
		t.Fatalf("iKun episodes: %+v, %v", extra, err)
	}
	base := Entry{AnimeID: a.ID, AgeID: "20260024", Episodes: []Episode{{Label: "第01集", Path: "/play/20260024/1/1", Line: 1}, {Label: "第01集", Path: "/play/20260024/2/1", Line: 2}, {Label: "第1集", Path: "/play/20260024/3/1", Line: 3}}, Lines: map[int]string{1: "VIP 西瓜", 2: "非凡", 3: "暴风"}}
	merged := mergeIkun(base, extra)
	if len(merged.Episodes) != 5 || merged.Lines[1000] != "iKun · ikm3u8" {
		t.Fatalf("source was not merged: %+v", merged)
	}
	candidates := episodeCandidates(merged, merged.Episodes[0])
	if len(candidates) != 4 || candidates[2].Source != "ikun" {
		t.Fatalf("cross-source fallback not included within four attempts: %+v", candidates)
	}
	merged = mergeIkun(merged, extra)
	if len(merged.Episodes) != 5 {
		t.Fatal("repeat enrichment duplicated episodes")
	}
}

func TestIkunSearchIncludesDistinctiveTitleSuffix(t *testing.T) {
	queries := ikunQueries("异世界舅舅")
	if len(queries) != 3 || queries[0] != "异世界舅" || queries[1] != "异世界舅舅" || queries[2] != "舅舅" {
		t.Fatalf("alternate title could not be found by suffix: %v", queries)
	}
	a := catalog.Anime{ID: 339326, Title: "异世界舅舅", Native: "異世界おじさん", Year: 2022}
	v := ikunVideo{ID: 34404, Name: "异世界归来的舅舅", Aliases: "異世界おじさん", Year: "2022–", Type: "日本动漫"}
	if !ikunMatches(a, v) {
		t.Fatal("same-year original-title match was rejected")
	}
}

func TestPerfectBlueVerifiedAlternateTitle(t *testing.T) {
	a := catalog.Anime{ID: 839, Title: "蓝色恐惧", Native: "PERFECT BLUE", Year: 1997}
	if titles := searchTitles(a); len(titles) != 2 || titles[1] != "未麻的部屋" {
		t.Fatalf("missing source search alias: %v", titles)
	}
	v := ikunVideo{ID: 1, Name: "未麻的部屋", Year: "1997", Type: "动画片"}
	if !ikunMatches(a, v) {
		t.Fatal("verified alternate title was rejected")
	}
	v.Year = "2001"
	if ikunMatches(a, v) {
		t.Fatal("wrong-year alternate title was accepted")
	}
}

func TestBlueLockSecondSeasonAlias(t *testing.T) {
	a := catalog.Anime{ID: 426123, Title: "蓝色监狱 VS. U-20 日本代表队", Native: "ブルーロック VS. U-20 JAPAN", Year: 2024}
	v := ikunVideo{ID: 2, Name: "蓝色监狱第二季", Year: "2024", Type: "日本动漫"}
	if !ikunMatches(a, v) {
		t.Fatal("verified second-season alias was rejected")
	}
	v.Year = "2022"
	if ikunMatches(a, v) {
		t.Fatal("first season was accepted as the second season")
	}
}

func TestBangumiAliasesCanMatchSourceWithoutHardcodedID(t *testing.T) {
	a := catalog.Anime{ID: 987654, Title: "正式季度标题", Native: "Original Season Title", Aliases: []string{"不同译名 第二季"}, Year: 2024}
	v := ikunVideo{ID: 2, Name: "不同译名第二季", Year: "2024", Type: "日本动漫"}
	if !ikunMatches(a, v) || len(searchTitles(a)) != 2 {
		t.Fatal("Bangumi alias did not reach the provider matcher")
	}
}
