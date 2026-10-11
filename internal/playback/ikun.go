package playback

import (
	"anime-shelf/internal/catalog"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const ikunAPI = "https://ikunzyapi.com/api.php/provide/vod/"

type ikunResult struct {
	List []ikunVideo `json:"list"`
}
type ikunVideo struct {
	ID       int    `json:"vod_id"`
	Name     string `json:"vod_name"`
	Aliases  string `json:"vod_sub"`
	Year     string `json:"vod_year"`
	Type     string `json:"type_name"`
	PlayFrom string `json:"vod_play_from"`
	PlayURL  string `json:"vod_play_url"`
}

func (s *Service) ikunSearch(ctx context.Context, keyword string) ([]ikunVideo, error) {
	if err := s.ikunUpstream.wait(ctx); err != nil {
		return nil, err
	}
	u := ikunAPI + "?ac=videolist&wd=" + url.QueryEscape(keyword)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Banxuan/1.0)")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	s.ikunUpstream.observe(resp.StatusCode, resp.Header)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("iKun 返回 HTTP %d", resp.StatusCode)
	}
	var result ikunResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return result.List, nil
}

func ikunMatches(a catalog.Anime, v ikunVideo) bool {
	if v.ID <= 0 || (v.Type != "日本动漫" && v.Type != "动画片") {
		return false
	}
	if a.Year > 0 && len(v.Year) >= 4 {
		year, err := strconv.Atoi(v.Year[:4])
		if err == nil && year != a.Year {
			return false
		}
	}
	for _, title := range append([]string{v.Name}, strings.Split(v.Aliases, ",")...) {
		if norm(title) != "" && (norm(title) == norm(a.Title) || a.Native != "" && norm(title) == norm(a.Native)) {
			return true
		}
		for _, alias := range sourceAliases(a) {
			if norm(title) == norm(alias) {
				return true
			}
		}
	}
	return false
}

func ikunEntry(a catalog.Anime, v ikunVideo) (Entry, error) {
	e := Entry{AnimeID: a.ID, Title: v.Name, URL: fmt.Sprintf("https://www.ikunzy.cc/voddetail/%d.html", v.ID), CheckedAt: time.Now().UTC().Format(time.RFC3339), IkunCheckedAt: time.Now().UTC().Format(time.RFC3339), Lines: map[int]string{}, Episodes: []Episode{}}
	groups := strings.Split(v.PlayURL, "$$$")
	from := strings.Split(v.PlayFrom, "$$$")
	for i, group := range groups {
		if i >= len(from) || i >= 20 {
			break
		}
		line := 1000 + i
		for j, item := range strings.Split(group, "#") {
			if j >= 200 {
				break
			}
			label, raw, ok := strings.Cut(item, "$")
			if !ok {
				continue
			}
			raw = strings.TrimSpace(raw)
			mediaURL, err := url.Parse(raw)
			if err != nil || mediaURL.Scheme != "https" || mediaURL.Hostname() == "" || mediaURL.User != nil || mediaURL.Port() != "" && mediaURL.Port() != "443" {
				continue
			}
			path := fmt.Sprintf("/ikun/%d/%d/%d", v.ID, i, j)
			e.Episodes = append(e.Episodes, Episode{Label: strings.TrimSpace(label), Path: path, Line: line, Source: "ikun", DirectURL: raw, SourceURL: e.URL})
			e.Lines[line] = "iKun · " + from[i]
		}
	}
	if len(e.Episodes) == 0 {
		return Entry{}, fmt.Errorf("iKun 没有可直接播放的选集")
	}
	return e, nil
}

func (s *Service) findIkun(ctx context.Context, a catalog.Anime) (Entry, error) {
	var queries []string
	seen := map[string]bool{}
	for _, title := range searchTitles(a) {
		for _, keyword := range ikunQueries(title) {
			if !seen[keyword] {
				queries = append(queries, keyword)
				seen[keyword] = true
			}
		}
	}
	for _, keyword := range queries {
		results, err := s.ikunSearch(ctx, keyword)
		if err != nil {
			return Entry{}, err
		}
		for _, video := range results {
			if ikunMatches(a, video) {
				return ikunEntry(a, video)
			}
		}
	}
	return Entry{}, fmt.Errorf("iKun 未找到同季作品")
}

func ikunQueries(title string) []string {
	query := []rune(title)
	if len(query) > 4 {
		query = query[:4]
	}
	queries := []string{string(query)}
	if string(query) != title {
		queries = append(queries, title)
	}
	// A source may insert a word in the Chinese name. Try one short suffix
	// only after exact searches; title, year and native name still verify it.
	runes := []rune(title)
	if len(runes) >= 4 && !strings.Contains("季篇版集", string(runes[len(runes)-1])) {
		suffix := string(runes[len(runes)-2:])
		if suffix != queries[0] && suffix != title {
			queries = append(queries, suffix)
		}
	}
	return queries
}

func mergeIkun(base, extra Entry) Entry {
	kept := base.Episodes[:0:0]
	for _, ep := range base.Episodes {
		if ep.Source != "ikun" {
			kept = append(kept, ep)
		}
	}
	base.Episodes = kept
	lines := make(map[int]string, len(base.Lines)+len(extra.Lines))
	for line, label := range base.Lines {
		lines[line] = label
	}
	base.Lines = lines
	for line := range lines {
		if line >= 1000 && line < 2000 {
			delete(base.Lines, line)
		}
	}
	base.IkunCheckedAt = time.Now().UTC().Format(time.RFC3339)
	if len(extra.Episodes) == 0 {
		return base
	}
	for line, label := range extra.Lines {
		base.Lines[line] = label
	}
	for _, ep := range extra.Episodes {
		if ep.Source == "ikun" {
			base.Episodes = append(base.Episodes, ep)
		}
	}
	if base.URL == "" {
		base.Title, base.URL, base.CheckedAt = extra.Title, extra.URL, extra.CheckedAt
	}
	return base
}
