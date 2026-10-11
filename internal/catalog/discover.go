package catalog

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type bgmSubject struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Title    string `json:"name_cn"`
	Date     string `json:"date"`
	Platform string `json:"platform"`
	Summary  string `json:"summary"`
	Episodes int    `json:"eps"`
	Tags     []struct {
		Name string `json:"name"`
	} `json:"tags"`
	Rating struct {
		Score float64 `json:"score"`
		Total int     `json:"total"`
	} `json:"rating"`
	Infobox []struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	} `json:"infobox"`
}

// Conservative discovery: only exact normalized native-title matches from Japanese AniList records.
// Split cours with different titles stay unmatched until reviewed, rather than guessing their IDs.
func Discover(ctx context.Context, d Dataset) (Dataset, []string) {
	year := time.Now().Year()
	var b struct {
		Data []bgmSubject `json:"data"`
	}
	if err := request(ctx, "GET", fmt.Sprintf("https://api.bgm.tv/v0/subjects?type=2&sort=rank&year=%d&limit=24", year), nil, &b); err != nil {
		return d, []string{"今年新番暂时无法抓取，保留已有片库。"}
	}
	var n struct {
		Data struct {
			Page struct {
				Media []struct {
					ID      int                                      `json:"id"`
					MALID   int                                      `json:"idMal"`
					Country string                                   `json:"countryOfOrigin"`
					Score   int                                      `json:"averageScore"`
					Title   struct{ Native, English, Romaji string } `json:"title"`
					Cover   struct {
						URL string `json:"extraLarge"`
					} `json:"coverImage"`
					Banner string `json:"bannerImage"`
					Stats  struct {
						Distribution []struct {
							Amount int `json:"amount"`
						} `json:"scoreDistribution"`
					} `json:"stats"`
				} `json:"media"`
			} `json:"Page"`
		} `json:"data"`
	}
	q := map[string]any{"query": "query($year:Int){Page(perPage:50){media(type:ANIME,seasonYear:$year,sort:SCORE_DESC){id idMal countryOfOrigin title{native romaji english} averageScore bannerImage coverImage{extraLarge} stats{scoreDistribution{amount}}}}}", "variables": map[string]int{"year": year}}
	if err := request(ctx, "POST", "https://graphql.anilist.co", q, &n); err != nil {
		return d, []string{"新番跨站匹配暂不可用，保留已有片库。"}
	}
	existing := map[int]bool{}
	for _, a := range d.Items {
		existing[a.ID] = true
	}
	allowed := map[string]bool{}
	for _, g := range []string{"奇幻", "科幻", "日常", "剧情", "悬疑", "恋爱", "冒险", "音乐", "运动", "战斗", "美食", "职场", "历史", "治愈", "喜剧", "战争", "机战", "校园"} {
		allowed[g] = true
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, s := range b.Data {
		if existing[s.ID] || s.Rating.Score <= 0 || s.Rating.Score > 10 || s.Rating.Total <= 0 {
			continue
		}
		for _, a := range n.Data.Page.Media {
			if a.Country != "JP" || normalize(s.Name) == "" || normalize(s.Name) != normalize(a.Title.Native) || a.Score <= 0 || a.Score > 100 {
				continue
			}
			count := 0
			for _, v := range a.Stats.Distribution {
				count += v.Amount
			}
			if count <= 0 {
				continue
			}
			genres := []string{}
			for _, g := range s.Tags {
				if allowed[g.Name] && !contains(genres, g.Name) && len(genres) < 4 {
					genres = append(genres, g.Name)
				}
			}
			title := s.Title
			if title == "" {
				title = s.Name
			}
			english := a.Title.English
			if english == "" {
				english = a.Title.Romaji
			}
			studio := ""
			for _, v := range s.Infobox {
				if v.Key == "动画制作" {
					studio, _ = v.Value.(string)
				}
			}
			itemYear, _ := strconv.Atoi(strings.Split(s.Date, "-")[0])
			item := Anime{ID: s.ID, MALID: a.MALID, AniListID: a.ID, Title: title, Native: s.Name, English: english, Cover: a.Cover.URL, Banner: a.Banner, Date: s.Date, Year: itemYear, Format: s.Platform, Episodes: s.Episodes, Studio: studio, Summary: s.Summary, Genres: genres, Sources: []Source{{Name: "Bangumi", Score: s.Rating.Score, Scale: 10, Votes: s.Rating.Total, URL: fmt.Sprintf("https://bgm.tv/subject/%d", s.ID), Updated: now}, {Name: "AniList", Score: float64(a.Score), Scale: 100, Votes: count, URL: fmt.Sprintf("https://anilist.co/anime/%d", a.ID), Updated: now}}}
			Calculate(&item)
			d.Items = append(d.Items, item)
			existing[s.ID] = true
			d.FetchedAt = now
			break
		}
	}
	return d, []string{}
}
func normalize(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= 0xff01 && c <= 0xff5e {
			c -= 0xfee0
		}
		if unicode.IsLetter(c) || unicode.IsNumber(c) {
			b.WriteRune(unicode.ToLower(c))
		}
	}
	return b.String()
}
