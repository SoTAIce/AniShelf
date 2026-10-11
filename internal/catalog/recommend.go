package catalog

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

type Seed struct {
	ID     int     `json:"id"`
	Weight float64 `json:"weight"`
}
type Preferences struct {
	Genres   []string `json:"genres"`
	Liked    []int    `json:"liked"`
	Saved    []int    `json:"saved"`
	Seen     []int    `json:"seen"`
	Disliked []int    `json:"disliked"`
	Imported []Seed   `json:"-"`
}
type Recommendation struct {
	Anime     Anime    `json:"anime"`
	Reasons   []string `json:"reasons"`
	Relevance float64  `json:"relevance"`
}

func recommendationQuality(a Anime) (float64, bool) {
	if a.Composite != nil && *a.Composite > 0 {
		return *a.Composite / 10, true
	}
	for _, s := range a.Sources {
		if s.Name == "Bangumi" && s.Scale > 0 && s.Score > 0 && s.Score <= s.Scale && s.Votes >= 50 {
			// Shrink small samples toward 7/10 without presenting this as a rating.
			value := (float64(s.Votes)*(s.Score/s.Scale*10) + 300*7) / float64(s.Votes+300)
			return value / 10, true
		}
	}
	return 0, false
}

func franchiseKey(title string) string {
	runes := []rune{}
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			runes = append(runes, r)
			if len(runes) == 4 {
				break
			}
		}
	}
	return string(runes)
}

type vector map[string]float64

func normalizeVector(v vector) vector {
	n := 0.
	for _, w := range v {
		n += w * w
	}
	if n > 0 {
		n = math.Sqrt(n)
		for k, w := range v {
			v[k] = w / n
		}
	}
	return v
}
func cosine(a, b vector) float64 {
	sum := 0.
	for k, w := range a {
		sum += w * b[k]
	}
	return sum
}
func features(a Anime) vector {
	v := vector{}
	for _, g := range a.Genres {
		v["genre:"+g] = 1
	}
	if a.Studio != "" {
		v["studio:"+strings.ToLower(a.Studio)] = .45
	}
	if a.Format != "" {
		v["format:"+a.Format] = .2
	}
	return v
}

// TF-IDF feature vectors + weighted taste profile + MMR diversity.
// Followed and watched shows are weak interest signals, not explicit likes.
func Recommend(d Dataset, p Preferences) []Recommendation {
	excluded := map[int]bool{}
	seeds := map[int]float64{}
	negative := map[int]bool{}
	for _, s := range p.Imported {
		excluded[s.ID] = true
		seeds[s.ID] = math.Min(1.2, math.Max(0, s.Weight))
	}
	for _, id := range p.Saved {
		excluded[id] = true
		seeds[id] = math.Max(seeds[id], 1)
	}
	for _, id := range p.Liked {
		excluded[id] = true
		seeds[id] = 3
	}
	for _, id := range p.Seen {
		excluded[id] = true
	}
	for _, id := range p.Disliked {
		excluded[id] = true
		negative[id] = true
		delete(seeds, id)
	}
	df := map[string]int{}
	vectors := map[int]vector{}
	for _, a := range d.Items {
		v := features(a)
		vectors[a.ID] = v
		for k := range v {
			df[k]++
		}
	}
	idf := func(k string) float64 { return math.Log((float64(len(d.Items))+1)/(float64(df[k])+1)) + 1 }
	profile := vector{}
	avoid := vector{}
	seedNames := map[string]string{}
	for _, g := range p.Genres {
		profile["genre:"+g] += 3 * idf("genre:"+g)
	}
	for _, a := range d.Items {
		v := vectors[a.ID]
		for k, w := range v {
			v[k] = w * idf(k)
		}
		normalizeVector(v)
		if w := seeds[a.ID]; w > 0 {
			for k, x := range v {
				profile[k] += w * x
			}
			for _, g := range a.Genres {
				seedNames[g] = a.Title
			}
		}
		if negative[a.ID] {
			for k, x := range v {
				avoid[k] += x
			}
		}
	}
	normalizeVector(profile)
	normalizeVector(avoid)
	personalized := len(profile) > 0
	candidates := []Recommendation{}
	for _, a := range d.Items {
		quality, rated := recommendationQuality(a)
		if excluded[a.ID] || !rated {
			continue
		}
		fit := cosine(profile, vectors[a.ID])
		penalty := cosine(avoid, vectors[a.ID])
		relevance := quality
		if personalized {
			relevance = .72*fit + .28*quality
		}
		relevance -= .15 * penalty
		reasons := []string{}
		type genreWeight struct {
			g string
			w float64
		}
		shared := []genreWeight{}
		for _, g := range a.Genres {
			if w := profile["genre:"+g]; w > 0 {
				shared = append(shared, genreWeight{g, w})
			}
		}
		sort.Slice(shared, func(i, j int) bool {
			if shared[i].w == shared[j].w {
				return shared[i].g < shared[j].g
			}
			return shared[i].w > shared[j].w
		})
		for _, g := range shared {
			if len(reasons) == 2 {
				break
			}
			if contains(p.Genres, g.g) {
				reasons = append(reasons, "符合你偏爱的"+g.g+"类型")
			} else if seedNames[g.g] != "" {
				reasons = append(reasons, "与你关注的《"+seedNames[g.g]+"》同属"+g.g)
			}
		}
		if len(reasons) == 0 {
			if personalized {
				reasons = append(reasons, "换个类型，试试高口碑作品")
			} else {
				reasons = append(reasons, "还不了解你的偏好，先从高口碑作品开始")
			}
		}
		candidates = append(candidates, Recommendation{a, reasons, math.Round(relevance*10000) / 10000})
	}
	result := []Recommendation{}
	for len(candidates) > 0 && len(result) < 12 {
		best := 0
		bestScore := math.Inf(-1)
		for i, r := range candidates {
			noveltyPenalty := 0.
			familyCount := 0
			if personalized {
				for _, chosen := range result {
					noveltyPenalty = math.Max(noveltyPenalty, cosine(vectors[r.Anime.ID], vectors[chosen.Anime.ID]))
					if key := franchiseKey(r.Anime.Title); key != "" && key == franchiseKey(chosen.Anime.Title) {
						familyCount++
					}
				}
			}
			familyPenalty := 0.
			if familyCount == 1 {
				familyPenalty = .12
			} else if familyCount >= 2 {
				familyPenalty = .3
			}
			value := r.Relevance - .10*noveltyPenalty - familyPenalty
			if value > bestScore || (value == bestScore && r.Anime.ID < candidates[best].Anime.ID) {
				best = i
				bestScore = value
			}
		}
		result = append(result, candidates[best])
		candidates = append(candidates[:best], candidates[best+1:]...)
	}
	return result
}
func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
