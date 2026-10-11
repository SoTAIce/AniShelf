package catalog

import "testing"

func TestRecommendationsRespectTasteAndExclusions(t *testing.T) {
	score := func(v float64) *float64 { return &v }
	d := Dataset{Items: []Anime{{ID: 1, Title: "科幻种子", Genres: []string{"科幻"}, Composite: score(9)}, {ID: 2, Genres: []string{"科幻"}, Composite: score(8)}, {ID: 3, Genres: []string{"恋爱"}, Composite: score(9.5)}, {ID: 4, Genres: []string{"科幻"}, Composite: score(9)}, {ID: 5, Genres: []string{"科幻"}, Composite: score(9)}, {ID: 6, Genres: []string{"科幻"}, Composite: score(9)}}}
	cold := Recommend(d, Preferences{})
	if cold[0].Anime.ID != 3 {
		t.Fatal("cold start should favor quality")
	}
	got := Recommend(d, Preferences{Liked: []int{1}, Seen: []int{4}, Saved: []int{5}, Disliked: []int{6}})
	if len(got) != 2 || got[0].Anime.ID != 2 {
		t.Fatalf("taste and exclusions: %#v", got)
	}
	if len(got[0].Reasons) == 0 {
		t.Fatal("recommendation must explain why")
	}
}

func TestImportedSeedsAreWeakInterestAndDislikeWins(t *testing.T) {
	score := func(v float64) *float64 { return &v }
	d := Dataset{Items: []Anime{{ID: 1, Title: "追番种子", Genres: []string{"科幻"}, Composite: score(9)}, {ID: 2, Genres: []string{"科幻"}, Composite: score(8)}, {ID: 3, Genres: []string{"恋爱"}, Composite: score(8)}}}
	got := Recommend(d, Preferences{Imported: []Seed{{ID: 1, Weight: 1}}})
	if got[0].Anime.ID != 2 || len(got) != 2 {
		t.Fatal("imported interest not applied or not excluded")
	}
	got = Recommend(d, Preferences{Imported: []Seed{{ID: 1, Weight: 1}}, Disliked: []int{1}})
	if got[0].Anime.ID != 3 {
		t.Fatal("explicit dislike must override weak follow interest")
	}
}

func TestSingleSourceCandidatesAndTasteTags(t *testing.T) {
	source := func(score float64, votes int) []Source {
		return []Source{{Name: "Bangumi", Score: score, Scale: 10, Votes: votes}}
	}
	d := Dataset{Items: []Anime{
		{ID: 1, Title: "已喜欢的奇幻番", Genres: []string{"奇幻"}, Sources: source(8, 1000)},
		{ID: 2, Title: "全库奇幻候选", Genres: []string{"奇幻"}, Sources: source(8.2, 900)},
		{ID: 3, Title: "评分人数不足", Genres: []string{"奇幻"}, Sources: source(9.9, 3)},
		{ID: 4, Title: "全库恋爱候选", Genres: []string{"恋爱"}, Sources: source(8.3, 800)},
	}}
	p := Preferences{Liked: []int{1}}
	tags := RecommendationTags(d, p)
	if len(tags) == 0 || tags[0] != "奇幻" {
		t.Fatalf("taste tags: %v", tags)
	}
	got := Recommend(d, p)
	if len(got) != 2 || got[0].Anime.ID != 2 {
		t.Fatalf("broad recommendation and exclusions: %#v", got)
	}
	if got[0].Anime.Composite != nil {
		t.Fatal("single source rating must not be presented as composite")
	}
}
