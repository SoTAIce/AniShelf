package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RecommendationTags chooses at most two interests to widen the candidate pool.
// Manual genre choices lead; liked and imported anime add their known genres.
func RecommendationTags(d Dataset, p Preferences) []string {
	weights := map[string]float64{}
	for _, g := range p.Genres {
		weights[g] += 4
	}
	seeds := map[int]float64{}
	for _, s := range p.Imported {
		seeds[s.ID] = s.Weight
	}
	for _, id := range p.Saved {
		seeds[id] = 1
	}
	for _, id := range p.Liked {
		seeds[id] = 3
	}
	for _, a := range d.Items {
		for _, g := range a.Genres {
			weights[g] += seeds[a.ID]
		}
	}
	type weighted struct {
		genre  string
		weight float64
	}
	list := []weighted{}
	for g, w := range weights {
		if w > 0 {
			list = append(list, weighted{g, w})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].weight == list[j].weight {
			return list[i].genre < list[j].genre
		}
		return list[i].weight > list[j].weight
	})
	result := []string{}
	for _, item := range list {
		if len(result) == 2 {
			break
		}
		result = append(result, item.genre)
	}
	return result
}

func (e *Explorer) recommendationPage(ctx context.Context, tag string, offset int) ([]Anime, error) {
	key := fmt.Sprintf("%s|%d", tag, offset)
	e.mu.Lock()
	cached, ok := e.recommendCache[key]
	e.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.result.Items, nil
	}
	filter := map[string]any{"type": []int{2}, "rating_count": []string{">=200"}, "nsfw": false}
	if tag != "" {
		filter["tag"] = []string{tag}
	}
	var response struct {
		Data []bgmExploreSubject `json:"data"`
	}
	endpoint := fmt.Sprintf("https://api.bgm.tv/v0/search/subjects?limit=50&offset=%d", offset)
	if err := request(ctx, "POST", endpoint, map[string]any{"keyword": "", "sort": "heat", "filter": filter}, &response); err != nil {
		return nil, err
	}
	items := []Anime{}
	for _, b := range response.Data {
		if !searchableJapanese(b) || b.Rating.Total < 200 || b.Rating.Score <= 0 || b.Rating.Score > 10 || b.Date > time.Now().Format("2006-01-02") && b.Date != "" {
			continue
		}
		items = append(items, b.anime())
	}
	e.mu.Lock()
	if len(e.recommendCache) > 30 {
		e.recommendCache = map[string]exploreCache{}
	}
	e.recommendCache[key] = exploreCache{result: ExploreResult{Items: items}, expires: time.Now().Add(30 * time.Minute)}
	e.mu.Unlock()
	return items, nil
}

// RecommendationCandidates samples popular rated anime plus tag-specific anime.
// Each page is cached so repeated taste changes do not repeatedly hit Bangumi.
func (e *Explorer) RecommendationCandidates(ctx context.Context, tags []string) ([]Anime, error) {
	queries := []struct {
		tag    string
		offset int
	}{{"", 0}, {"", 50}, {"", 100}}
	for _, tag := range tags {
		if strings.TrimSpace(tag) != "" {
			queries = append(queries, struct {
				tag    string
				offset int
			}{tag, 0})
		}
	}
	seen := map[int]bool{}
	items := []Anime{}
	var firstErr error
	for _, q := range queries {
		page, err := e.recommendationPage(ctx, q.tag, q.offset)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, a := range page {
			if !seen[a.ID] {
				seen[a.ID] = true
				items = append(items, a)
			}
		}
	}
	return items, firstErr
}
