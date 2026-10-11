package catalog

import (
	"encoding/json"
	"testing"
)

func TestNormalizeQuerySeasonAlias(t *testing.T) {
	if got := NormalizeQuery("无职转生第2季"); got != "无职转生 第二季" {
		t.Fatalf("season alias: got %q", got)
	}
	if got := NormalizeQuery("葬送的芙莉莲 第二季"); got != "葬送的芙莉莲 第二季" {
		t.Fatalf("existing season: got %q", got)
	}
	if got := NormalizeQuery("蓝色禁区第2季"); got != "蓝色监狱 第二季" {
		t.Fatalf("Blue Lock alternate title: got %q", got)
	}
}

func TestExploreDoesNotInventCompositeScore(t *testing.T) {
	b := bgmExploreSubject{ID: 373247, Type: 2, Name: "無職転生Ⅱ ～異世界行ったら本気だす～", NameCN: "无职转生 第二季", Date: "2023-07-02"}
	b.Rating.Score = 7.3
	b.Rating.Total = 17000
	if !searchableJapanese(b) {
		t.Fatal("Japanese anime was filtered out")
	}
	a := b.anime()
	if a.Composite != nil {
		t.Fatalf("one rating source must not produce a composite: %+v", a.Composite)
	}
	if len(a.Sources) != 1 || a.Sources[0].Name != "Bangumi" {
		t.Fatalf("wrong rating source: %+v", a.Sources)
	}
}

func TestBangumiSubjectAliasesAreKeptForSourceMatching(t *testing.T) {
	var b bgmExploreSubject
	data := `{"id":426123,"type":2,"name":"ブルーロック VS. U-20 JAPAN","name_cn":"蓝色监狱 VS. U-20 日本代表队","date":"2024-10-05","infobox":[{"key":"别名","value":[{"v":"蓝色监狱 第二季"},{"v":"蓝色禁区 第二季"},{"v":"蓝色监狱 第二季"}]}]}`
	if err := json.Unmarshal([]byte(data), &b); err != nil {
		t.Fatal(err)
	}
	a := b.anime()
	if len(a.Aliases) != 2 || a.Aliases[0] != "蓝色监狱 第二季" || a.Aliases[1] != "蓝色禁区 第二季" {
		t.Fatalf("official alternate titles were not preserved: %v", a.Aliases)
	}
}

func TestCuratedSubjectKeepsCompositeAndAddsLiveAliases(t *testing.T) {
	score := 8.9
	curated := Anime{ID: 426124, Title: "剧场版 蓝色监狱 -EPISODE 凪-", Native: "劇場版ブルーロック -EPISODE 凪-", Composite: &score, Aliases: []string{"蓝色监狱 剧场版"}}
	live := Anime{ID: 426124, Title: "剧场版 蓝色监狱 -EPISODE 凪-", Native: curated.Native, Aliases: []string{"蓝色禁区：凪", "蓝色监狱 剧场版"}}
	got := mergeSubjectAliases(curated, live)
	if got.Composite == nil || *got.Composite != score {
		t.Fatalf("curated rating was lost: %+v", got.Composite)
	}
	if len(got.Aliases) != 2 || got.Aliases[1] != "蓝色禁区：凪" {
		t.Fatalf("live aliases were not merged without duplicates: %v", got.Aliases)
	}
}
