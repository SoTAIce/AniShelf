package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

type ExploreResult struct {
	Items     []Anime `json:"items"`
	Total     int     `json:"total"`
	Offset    int     `json:"offset"`
	Limit     int     `json:"limit"`
	Query     string  `json:"query"`
	FetchedAt string  `json:"fetchedAt"`
	Source    string  `json:"source"`
}
type exploreCache struct {
	result  ExploreResult
	expires time.Time
}
type subjectCache struct {
	anime   Anime
	expires time.Time
}
type Explorer struct {
	mu             sync.Mutex
	cache          map[string]exploreCache
	subjectCache   map[int]subjectCache
	recommendCache map[string]exploreCache
	store          *Store
}

func NewExplorer(store *Store) *Explorer {
	return &Explorer{store: store, cache: map[string]exploreCache{}, subjectCache: map[int]subjectCache{}, recommendCache: map[string]exploreCache{}}
}

var seasonAlias = regexp.MustCompile(`第([1-9])季`)
var seasonWords = []string{"", "一", "二", "三", "四", "五", "六", "七", "八", "九"}

func NormalizeQuery(q string) string {
	q = strings.TrimSpace(q)
	if strings.HasPrefix(q, "蓝色禁区") {
		q = "蓝色监狱" + strings.TrimPrefix(q, "蓝色禁区")
	}
	q = seasonAlias.ReplaceAllStringFunc(q, func(s string) string { n := int([]rune(s)[1] - '0'); return "第" + seasonWords[n] + "季" })
	q = strings.ReplaceAll(q, "  ", " ")
	if at := strings.Index(q, "第"); at > 0 && q[at-1] != ' ' {
		q = q[:at] + " " + q[at:]
	}
	return q
}

type bgmExploreSubject struct {
	ID       int    `json:"id"`
	Type     int    `json:"type"`
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Date     string `json:"date"`
	Platform string `json:"platform"`
	Summary  string `json:"summary"`
	Eps      int    `json:"eps"`
	Images   struct {
		Large  string `json:"large"`
		Common string `json:"common"`
	} `json:"images"`
	Rating struct {
		Score float64 `json:"score"`
		Total int     `json:"total"`
	} `json:"rating"`
	Tags []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"tags"`
	Infobox []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	} `json:"infobox"`
}

func (b bgmExploreSubject) aliases() []string {
	seen := map[string]bool{strings.ToLower(strings.TrimSpace(b.Name)): true, strings.ToLower(strings.TrimSpace(b.NameCN)): true}
	var aliases []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if length := len([]rune(value)); length < 3 || length > 100 || seen[key] {
			return
		}
		seen[key] = true
		aliases = append(aliases, value)
	}
	for _, field := range b.Infobox {
		if field.Key != "别名" && field.Key != "其他名称" {
			continue
		}
		var single string
		if json.Unmarshal(field.Value, &single) == nil {
			add(single)
			continue
		}
		var values []struct {
			V string `json:"v"`
		}
		if json.Unmarshal(field.Value, &values) == nil {
			for _, item := range values {
				if len(aliases) >= 12 {
					break
				}
				add(item.V)
			}
		}
	}
	return aliases
}

func searchableJapanese(a bgmExploreSubject) bool {
	if a.Type != 2 || a.ID <= 0 || a.Name == "" {
		return false
	}
	japan := false
	foreign := false
	for _, t := range a.Tags {
		switch t.Name {
		case "日本", "日本动画":
			japan = true
		case "中国", "国产", "美国", "韩国", "国产动画", "中国动画":
			foreign = true
		}
	}
	return !foreign && (japan || hasJapanese(a.Name))
}
func hasJapanese(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana) {
			return true
		}
	}
	return false
}
func (b bgmExploreSubject) anime() Anime {
	title := b.NameCN
	if title == "" {
		title = b.Name
	}
	cover := b.Images.Large
	if cover == "" {
		cover = b.Images.Common
	}
	year := 0
	if len(b.Date) >= 4 {
		year, _ = strconv.Atoi(b.Date[:4])
	}
	genres := []string{}
	allowed := map[string]bool{"奇幻": true, "科幻": true, "日常": true, "剧情": true, "悬疑": true, "恋爱": true, "冒险": true, "音乐": true, "运动": true, "战斗": true, "治愈": true, "喜剧": true, "校园": true, "历史": true, "职场": true, "美食": true}
	for _, t := range b.Tags {
		if allowed[t.Name] && t.Count > 0 && len(genres) < 4 {
			genres = append(genres, t.Name)
		}
	}
	a := Anime{ID: b.ID, Title: title, Native: b.Name, Aliases: b.aliases(), Cover: cover, Date: b.Date, Year: year, Format: b.Platform, Episodes: b.Eps, Summary: b.Summary, Genres: genres, Sources: []Source{}, Confidence: "来源不足"}
	if b.Rating.Score > 0 && b.Rating.Score <= 10 && b.Rating.Total > 0 {
		a.Sources = append(a.Sources, Source{Name: "Bangumi", Score: b.Rating.Score, Scale: 10, Votes: b.Rating.Total, URL: fmt.Sprintf("https://bgm.tv/subject/%d", b.ID), Updated: time.Now().UTC().Format(time.RFC3339)})
	}
	Calculate(&a)
	return a
}
func (e *Explorer) Search(ctx context.Context, q string, year, offset, limit int) (ExploreResult, error) {
	q = NormalizeQuery(q)
	if len([]rune(q)) > 80 {
		return ExploreResult{}, fmt.Errorf("搜索词过长")
	}
	if offset < 0 || offset > 3000 {
		return ExploreResult{}, fmt.Errorf("分页范围无效")
	}
	if limit < 1 || limit > 50 {
		limit = 24
	}
	if year < 1970 || year > time.Now().Year()+2 {
		year = time.Now().Year()
	}
	key := fmt.Sprintf("%s|%d|%d|%d", q, year, offset, limit)
	e.mu.Lock()
	cached, ok := e.cache[key]
	e.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.result, nil
	}
	var response struct {
		Data  []bgmExploreSubject `json:"data"`
		Total int                 `json:"total"`
	}
	endpoint := fmt.Sprintf("https://api.bgm.tv/v0/subjects?type=2&sort=date&year=%d&limit=%d&offset=%d", year, limit, offset)
	method := "GET"
	var body any
	if q != "" {
		endpoint = fmt.Sprintf("https://api.bgm.tv/v0/search/subjects?limit=%d&offset=%d", limit, offset)
		method = "POST"
		body = map[string]any{"keyword": q, "filter": map[string]any{"type": []int{2}}}
	}
	if err := request(ctx, method, endpoint, body, &response); err != nil {
		return ExploreResult{}, err
	}
	curated := map[int]Anime{}
	for _, a := range e.store.Snapshot().Items {
		curated[a.ID] = a
	}
	items := []Anime{}
	for _, subject := range response.Data {
		if !searchableJapanese(subject) {
			continue
		}
		a, ok := curated[subject.ID]
		if !ok {
			a = subject.anime()
		}
		items = append(items, a)
	}
	result := ExploreResult{Items: items, Total: response.Total, Offset: offset, Limit: limit, Query: q, FetchedAt: time.Now().UTC().Format(time.RFC3339), Source: "Bangumi"}
	e.mu.Lock()
	if len(e.cache) > 100 {
		e.cache = map[string]exploreCache{}
	}
	e.cache[key] = exploreCache{result: result, expires: time.Now().Add(10 * time.Minute)}
	e.mu.Unlock()
	return result, nil
}
func (e *Explorer) Subject(ctx context.Context, id int) (Anime, error) {
	if id <= 0 {
		return Anime{}, fmt.Errorf("invalid subject")
	}
	e.mu.Lock()
	cached, ok := e.subjectCache[id]
	e.mu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.anime, nil
	}
	var curated *Anime
	for _, a := range e.store.Snapshot().Items {
		if a.ID == id {
			curated = &a
			break
		}
	}
	var b bgmExploreSubject
	endpoint := "https://api.bgm.tv/v0/subjects/" + strconv.Itoa(id)
	err := request(ctx, "GET", endpoint, nil, &b)
	if err != nil {
		if curated == nil {
			return Anime{}, err
		}
		// The local multi-source score and existing playback catalog remain usable
		// when Bangumi is temporarily unavailable.
		e.cacheSubject(id, *curated, 5*time.Minute)
		return *curated, nil
	}
	if !searchableJapanese(b) {
		return Anime{}, fmt.Errorf("subject is not Japanese anime")
	}
	a := b.anime()
	if curated != nil {
		a = mergeSubjectAliases(*curated, a)
	}
	e.cacheSubject(id, a, 24*time.Hour)
	return a, nil
}

func mergeSubjectAliases(curated, live Anime) Anime {
	seen := map[string]bool{}
	for _, alias := range append([]string{curated.Title, curated.Native}, curated.Aliases...) {
		seen[strings.ToLower(strings.TrimSpace(alias))] = true
	}
	for _, alias := range []string{live.Title, live.Native} {
		key := strings.ToLower(strings.TrimSpace(alias))
		if key != "" && !seen[key] {
			curated.Aliases = append(curated.Aliases, alias)
			seen[key] = true
		}
	}
	for _, alias := range live.Aliases {
		key := strings.ToLower(strings.TrimSpace(alias))
		if !seen[key] {
			curated.Aliases = append(curated.Aliases, alias)
			seen[key] = true
		}
	}
	return curated
}

func (e *Explorer) cacheSubject(id int, a Anime, ttl time.Duration) {
	e.mu.Lock()
	if len(e.subjectCache) > 500 {
		e.subjectCache = map[int]subjectCache{}
	}
	e.subjectCache[id] = subjectCache{anime: a, expires: time.Now().Add(ttl)}
	e.mu.Unlock()
}
