package playback

import (
	"anime-shelf/internal/catalog"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const skrOrigin = "https://www.skr1.cc"

var skrSearchItemRE = regexp.MustCompile(`(?s)<li class="searchlist_item".*?</li>`)
var skrDetailRE = regexp.MustCompile(`/voddetail/([0-9]+)/`)
var skrItemTitleRE = regexp.MustCompile(`(?s)<h4 class="vodlist_title">\s*<a[^>]*>(.*?)</a>`)
var skrCategoryRE = regexp.MustCompile(`(?s)<span class="info_right">.*?</span>`)
var skrYearRE = regexp.MustCompile(`<em class="voddate voddate_year">([0-9]{4})</em>`)
var skrTitleRE = regexp.MustCompile(`(?s)<h1 class="title">(.*?)</h1>`)
var skrDetailYearRE = regexp.MustCompile(`年份：</span><a[^>]*>([0-9]{4})</a>`)
var skrPlayerRE = regexp.MustCompile(`(?s)player_aaaa\s*=\s*(\{.*?\})\s*</script>`)
var skrPlayRE = regexp.MustCompile(`<a href="(/vodplay/([0-9]+)-([0-9]+)-([0-9]+)/)">([^<]+)</a>`)
var skrPathRE = regexp.MustCompile(`^/vodplay/[0-9]+-[0-9]+-[0-9]+/$`)

type skrMatch struct {
	id, title, category string
	year                int
}

func skrCandidates(body string) []skrMatch {
	var out []skrMatch
	seen := map[string]bool{}
	for _, item := range skrSearchItemRE.FindAllString(body, 30) {
		id := skrDetailRE.FindStringSubmatch(item)
		title := skrItemTitleRE.FindStringSubmatch(item)
		if len(id) < 2 || len(title) < 2 || seen[id[1]] {
			continue
		}
		seen[id[1]] = true
		year := 0
		if found := skrYearRE.FindStringSubmatch(item); len(found) > 1 {
			year, _ = strconv.Atoi(found[1])
		}
		category := ""
		if found := skrCategoryRE.FindString(title[1]); found != "" {
			category = text(found)
		}
		out = append(out, skrMatch{id: id[1], title: text(skrCategoryRE.ReplaceAllString(title[1], "")), category: category, year: year})
	}
	return out
}

func skrMatches(a catalog.Anime, candidate skrMatch) bool {
	if a.Year > 0 && candidate.year > 0 && a.Year != candidate.year {
		return false
	}
	if norm(candidate.title) == norm(a.Title) || a.Native != "" && norm(candidate.title) == norm(a.Native) {
		return true
	}
	for _, alias := range sourceAliases(a) {
		if norm(candidate.title) == norm(alias) {
			return true
		}
	}
	// The source calls this split-cour anime "season 3" while Bangumi lists
	// the exact arc and part. Keep this alias explicit to avoid guessing seasons.
	if a.ID == 472741 && norm(candidate.title) == norm("咒术回战第三季") {
		return true
	}
	if a.ID == 339326 && norm(candidate.title) == norm("异世界归来的舅舅") {
		return true
	}
	// Bangumi calls the 2021 cour "后半部分"; this source calls it "Part2".
	return a.ID == 316247 && norm(candidate.title) == norm("Re：从零开始的异世界生活第二季Part2")
}

func (s *Service) skrGet(ctx context.Context, path string) (string, error) {
	if !strings.HasPrefix(path, "/vodsearch/") && !strings.HasPrefix(path, "/voddetail/") && !strings.HasPrefix(path, "/vodplay/") {
		return "", fmt.Errorf("无效的樱之空路径")
	}
	if err := s.skrUpstream.wait(ctx); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, skrOrigin+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Banxuan/1.0)")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	s.skrUpstream.observe(resp.StatusCode, resp.Header)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("樱之空返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return string(b), err
}

func skrEntry(a catalog.Anime, id, body string) (Entry, error) {
	title := skrTitleRE.FindStringSubmatch(body)
	year := 0
	if match := skrDetailYearRE.FindStringSubmatch(body); len(match) > 1 {
		year, _ = strconv.Atoi(match[1])
	}
	if len(title) < 2 || !skrMatches(a, skrMatch{id: id, title: text(title[1]), year: year}) {
		return Entry{}, fmt.Errorf("樱之空作品名称或季度不匹配")
	}
	e := Entry{AnimeID: a.ID, Title: text(title[1]), URL: skrOrigin + "/voddetail/" + id + "/", CheckedAt: time.Now().UTC().Format(time.RFC3339), SkrCheckedAt: time.Now().UTC().Format(time.RFC3339), Episodes: []Episode{}, Lines: map[int]string{}}
	seen := map[string]bool{}
	for _, match := range skrPlayRE.FindAllStringSubmatch(body, 500) {
		if match[2] != id || seen[match[1]] {
			continue
		}
		seen[match[1]] = true
		line, _ := strconv.Atoi(match[3])
		if line < 1 || line > 50 {
			continue
		}
		line += 2000
		if _, exists := e.Lines[line]; !exists {
			e.Lines[line] = fmt.Sprintf("樱之空 · 线路 %d", line-2000)
		}
		e.Episodes = append(e.Episodes, Episode{Label: text(match[5]), Path: match[1], Line: line, Source: "skr", SourceURL: skrOrigin + match[1]})
	}
	if len(e.Episodes) == 0 {
		return Entry{}, fmt.Errorf("樱之空暂无选集")
	}
	return e, nil
}

func (s *Service) findSkr(ctx context.Context, a catalog.Anime) (Entry, error) {
	var queries []string
	seen := map[string]bool{}
	for _, title := range searchTitles(a) {
		query := []rune(title)
		if len(query) > 4 {
			query = query[:4]
		}
		for _, keyword := range []string{string(query), title} {
			if !seen[keyword] {
				queries = append(queries, keyword)
				seen[keyword] = true
			}
		}
	}
	for _, keyword := range queries {
		body, err := s.skrGet(ctx, "/vodsearch/-------------/?wd="+url.QueryEscape(keyword))
		if err != nil {
			return Entry{}, err
		}
		candidates := skrCandidates(body)
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].category == "日漫" && candidates[j].category != "日漫"
		})
		for _, candidate := range candidates {
			if !skrMatches(a, candidate) {
				continue
			}
			detail, err := s.skrGet(ctx, "/voddetail/"+candidate.id+"/")
			if err != nil {
				continue
			}
			if entry, err := skrEntry(a, candidate.id, detail); err == nil {
				return entry, nil
			}
		}
	}
	return Entry{}, fmt.Errorf("樱之空未找到同季作品")
}

func (s *Service) resolveSkrEpisode(ctx context.Context, ep Episode) (string, error) {
	if !skrPathRE.MatchString(ep.Path) {
		return "", fmt.Errorf("无效的樱之空选集")
	}
	body, err := s.skrGet(ctx, ep.Path)
	if err != nil {
		return "", err
	}
	return parseSkrPlayer(body)
}

func parseSkrPlayer(body string) (string, error) {
	match := skrPlayerRE.FindStringSubmatch(body)
	if len(match) < 2 {
		return "", fmt.Errorf("樱之空未提供播放器配置")
	}
	var config struct {
		URL     string `json:"url"`
		Encrypt int    `json:"encrypt"`
	}
	if err := json.Unmarshal([]byte(match[1]), &config); err != nil {
		return "", err
	}
	raw := config.URL
	var err error
	if config.Encrypt == 2 {
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return "", err
		}
		raw = string(b)
	}
	if config.Encrypt == 1 || config.Encrypt == 2 {
		raw, err = url.PathUnescape(raw)
		if err != nil {
			return "", err
		}
	}
	raw = html.UnescapeString(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", fmt.Errorf("樱之空未提供可直接播放的 HTTPS 地址")
	}
	return raw, nil
}

func mergeSkr(base, extra Entry) Entry {
	kept := base.Episodes[:0:0]
	for _, ep := range base.Episodes {
		if ep.Source != "skr" {
			kept = append(kept, ep)
		}
	}
	base.Episodes = kept
	lines := make(map[int]string, len(base.Lines)+len(extra.Lines))
	for line, label := range base.Lines {
		if line < 2000 {
			lines[line] = label
		}
	}
	for line, label := range extra.Lines {
		if line >= 2000 {
			lines[line] = label
		}
	}
	base.Lines = lines
	base.SkrCheckedAt = time.Now().UTC().Format(time.RFC3339)
	for _, ep := range extra.Episodes {
		if ep.Source == "skr" {
			base.Episodes = append(base.Episodes, ep)
		}
	}
	if base.AgeID == "" && base.URL == "" && extra.URL != "" {
		base.Title, base.URL, base.CheckedAt = extra.Title, extra.URL, extra.CheckedAt
	}
	return base
}
