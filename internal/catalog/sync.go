package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var client = &http.Client{Timeout: 12 * time.Second}

func request(ctx context.Context, method, url string, body any, out any) error {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, e := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(b))
		if e != nil {
			return e
		}
		req.Header.Set("User-Agent", "AnimeShelf/0.2 (personal-use; https://github.com/bangumi/api)")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, e := client.Do(req)
		if e == nil {
			if res.StatusCode == 200 {
				e = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
				res.Body.Close()
				return e
			}
			res.Body.Close()
			e = fmt.Errorf("upstream status %d", res.StatusCode)
			if res.StatusCode == 404 || res.StatusCode == 400 {
				return e
			}
		}
		err = e
		if attempt == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1500 * time.Millisecond):
			}
		}
	}
	return err
}
func Refresh(ctx context.Context, d Dataset) (Dataset, []string) {
	return RefreshProgress(ctx, d, func(int, int, int) {})
}

// Per-provider throttling and circuit breaking prevent a failed source from blocking all others.
func RefreshProgress(ctx context.Context, d Dataset, progress func(int, int, int)) (Dataset, []string) {
	d, discoveryIssues := Discover(ctx, d)
	done, total, updated := 0, len(d.Items), 0
	ids := []int{}
	issues := discoveryIssues
	failures := map[string]int{}
	warned := map[string]bool{}
	for _, a := range d.Items {
		if a.MALID > 0 {
			total++
		}
		if a.AniListID > 0 {
			ids = append(ids, a.AniListID)
		}
	}
	if len(ids) > 0 {
		total += (len(ids) + 49) / 50
	}
	advance := func() { done++; progress(done, total, updated) }
	warn := func(name string) {
		if !warned[name] {
			issues = append(issues, name+" 暂时无法更新，未成功的条目保留上次评分。")
			warned[name] = true
		}
	}
	valid := func(score, scale float64, n int) bool { return score > 0 && score <= scale && n > 0 }
	fresh := func() string { return time.Now().UTC().Format(time.RFC3339) }
	save := func(a *Anime, s Source) { put(a, s); updated++; d.FetchedAt = s.Updated }
	wait := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(1100 * time.Millisecond):
			return true
		}
	}
	progress(0, total, 0)
	for i := range d.Items {
		a := &d.Items[i]
		if ctx.Err() != nil {
			warn("本次抓取已结束")
			return d, issues
		}
		if failures["Bangumi"] < 3 {
			var b struct {
				ID     int `json:"id"`
				Rating struct {
					Score float64 `json:"score"`
					Total int     `json:"total"`
				} `json:"rating"`
			}
			e := request(ctx, "GET", fmt.Sprintf("https://api.bgm.tv/v0/subjects/%d", a.ID), nil, &b)
			if e == nil && b.ID == a.ID && valid(b.Rating.Score, 10, b.Rating.Total) {
				save(a, Source{Name: "Bangumi", Score: b.Rating.Score, Scale: 10, Votes: b.Rating.Total, URL: fmt.Sprintf("https://bgm.tv/subject/%d", a.ID), Updated: fresh()})
				failures["Bangumi"] = 0
			} else {
				failures["Bangumi"]++
				warn("Bangumi")
			}
			if !wait() {
				return d, append(issues, "抓取超时或服务停止，保留已成功的数据。")
			}
		}
		advance()
		if a.MALID > 0 {
			if failures["MAL"] < 3 {
				var m struct {
					Data struct {
						ID    int     `json:"mal_id"`
						Score float64 `json:"score"`
						Votes int     `json:"scored_by"`
					} `json:"data"`
				}
				e := request(ctx, "GET", fmt.Sprintf("https://api.jikan.moe/v4/anime/%d", a.MALID), nil, &m)
				if e == nil && m.Data.ID == a.MALID && valid(m.Data.Score, 10, m.Data.Votes) {
					save(a, Source{Name: "MAL", Score: m.Data.Score, Scale: 10, Votes: m.Data.Votes, URL: fmt.Sprintf("https://myanimelist.net/anime/%d", a.MALID), Updated: fresh()})
					failures["MAL"] = 0
				} else {
					failures["MAL"]++
					warn("MAL")
				}
				if !wait() {
					return d, append(issues, "抓取超时或服务停止，保留已成功的数据。")
				}
			}
			advance()
		}
	}
	// AniList allows batches of 50, reducing requests while preserving exact ID matching.
	for start := 0; start < len(ids); start += 50 {
		end := start + 50
		if end > len(ids) {
			end = len(ids)
		}
		var n struct {
			Data struct {
				Page struct {
					Media []struct {
						ID    int `json:"id"`
						Score int `json:"averageScore"`
						Stats struct {
							Distribution []struct {
								Amount int `json:"amount"`
							} `json:"scoreDistribution"`
						} `json:"stats"`
					} `json:"media"`
				} `json:"Page"`
			} `json:"data"`
		}
		query := map[string]any{"query": "query($ids:[Int]){Page(perPage:50){media(id_in:$ids,type:ANIME){id averageScore stats{scoreDistribution{amount}}}}}", "variables": map[string]any{"ids": ids[start:end]}}
		e := request(ctx, "POST", "https://graphql.anilist.co", query, &n)
		found := map[int]bool{}
		if e == nil {
			for _, m := range n.Data.Page.Media {
				count := 0
				for _, v := range m.Stats.Distribution {
					count += v.Amount
				}
				if !valid(float64(m.Score), 100, count) {
					continue
				}
				for i := range d.Items {
					a := &d.Items[i]
					if a.AniListID == m.ID {
						save(a, Source{Name: "AniList", Score: float64(m.Score), Scale: 100, Votes: count, URL: fmt.Sprintf("https://anilist.co/anime/%d", m.ID), Updated: fresh()})
						found[m.ID] = true
					}
				}
			}
		}
		if e != nil || len(found) < end-start {
			warn("AniList")
		}
		advance()
		if !wait() {
			return d, issues
		}
	}
	return d, issues
}
func put(a *Anime, s Source) {
	for i := range a.Sources {
		if a.Sources[i].Name == s.Name {
			a.Sources[i] = s
			return
		}
	}
	a.Sources = append(a.Sources, s)
}
