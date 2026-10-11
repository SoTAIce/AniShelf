package catalog

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type Source struct {
	Name    string  `json:"name"`
	Score   float64 `json:"score"`
	Scale   float64 `json:"scale"`
	Votes   int     `json:"votes"`
	URL     string  `json:"url"`
	Updated string  `json:"updated"`
	Weight  float64 `json:"weight"`
}
type Anime struct {
	ID         int      `json:"id"`
	MALID      int      `json:"malId"`
	AniListID  int      `json:"anilistId"`
	Title      string   `json:"title"`
	Native     string   `json:"native"`
	English    string   `json:"english"`
	Aliases    []string `json:"aliases,omitempty"`
	Cover      string   `json:"cover"`
	Banner     string   `json:"banner"`
	Date       string   `json:"date"`
	Year       int      `json:"year"`
	Format     string   `json:"format"`
	Episodes   int      `json:"episodes"`
	Studio     string   `json:"studio"`
	Summary    string   `json:"summary"`
	Genres     []string `json:"genres"`
	Sources    []Source `json:"sources"`
	Composite  *float64 `json:"composite"`
	Spread     float64  `json:"spread"`
	Votes      int      `json:"votes"`
	Confidence string   `json:"confidence"`
	Rank       int      `json:"rank"`
}
type Dataset struct {
	FetchedAt string  `json:"fetchedAt"`
	Scope     string  `json:"scope"`
	Items     []Anime `json:"items"`
}

// Equal community weights, adjusted for sample size; no guessed values for missing sources.
// AT/AC fan polls are deliberately excluded because their ranks are not ratings.
func Calculate(a *Anime) {
	sum, weight, low, high, valid := 0.0, 0.0, 10.0, 0.0, 0
	a.Votes = 0
	a.Composite = nil
	a.Spread = 0
	for i := range a.Sources {
		s := &a.Sources[i]
		s.Weight = 0
		if s.Scale <= 0 || s.Score <= 0 || s.Score > s.Scale || s.Votes <= 0 || math.IsNaN(s.Score) || math.IsInf(s.Score, 0) {
			continue
		}
		value := s.Score / s.Scale * 10
		s.Weight = float64(s.Votes) / float64(s.Votes+500)
		sum += value * s.Weight
		weight += s.Weight
		a.Votes += s.Votes
		valid++
		low = math.Min(low, value)
		high = math.Max(high, value)
	}
	if valid >= 2 {
		v := math.Round(sum/weight*100) / 100
		a.Composite = &v
		a.Spread = math.Round((high-low)*100) / 100
	}
	a.Confidence = "来源不足"
	if valid >= 2 {
		a.Confidence = "可参考"
		if a.Votes >= 10000 {
			a.Confidence = "样本充足"
		}
	}
}

type Store struct {
	mu   sync.RWMutex
	data Dataset
	Path string
}

func Load(path string) (*Store, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var d Dataset
	if e = json.Unmarshal(b, &d); e != nil {
		return nil, e
	}
	if len(d.Items) == 0 {
		return nil, fmt.Errorf("empty catalogue")
	}
	s := &Store{data: d, Path: path}
	s.recalculate()
	return s, nil
}
func (s *Store) recalculate() {
	for i := range s.data.Items {
		Calculate(&s.data.Items[i])
	}
	sort.SliceStable(s.data.Items, func(i, j int) bool {
		a, b := s.data.Items[i], s.data.Items[j]
		if a.Composite == nil {
			return false
		}
		if b.Composite == nil {
			return true
		}
		return *a.Composite > *b.Composite
	})
	rank := 0
	for i := range s.data.Items {
		if s.data.Items[i].Composite != nil {
			rank++
			s.data.Items[i].Rank = rank
		} else {
			s.data.Items[i].Rank = 0
		}
	}
}
func (s *Store) Snapshot() Dataset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, _ := json.Marshal(s.data)
	var d Dataset
	json.Unmarshal(b, &d)
	return d
}
func (s *Store) Replace(d Dataset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data
	s.data = d
	s.recalculate()
	b, e := json.MarshalIndent(s.data, "", "  ")
	if e == nil {
		e = os.WriteFile(s.Path+".tmp", b, 0644)
	}
	if e == nil {
		e = os.Rename(s.Path+".tmp", s.Path)
	}
	if e != nil {
		s.data = old
	}
	return e
}
func LatestSourceDate(a Anime) time.Time {
	var last time.Time
	for _, s := range a.Sources {
		t, _ := time.Parse(time.RFC3339, s.Updated)
		if t.After(last) {
			last = t
		}
	}
	return last
}
func Match(a Anime, q string) bool {
	return strings.Contains(strings.ToLower(a.Title+" "+a.Native+" "+a.English), strings.ToLower(q))
}
