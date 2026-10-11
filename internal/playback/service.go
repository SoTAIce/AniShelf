package playback

import (
	"anime-shelf/internal/catalog"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const origin = "https://www.agedm.io"

type Episode struct {
	Label     string `json:"label"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Source    string `json:"source,omitempty"`
	DirectURL string `json:"directUrl,omitempty"`
	SourceURL string `json:"sourceUrl,omitempty"`
}
type Entry struct {
	AnimeID       int            `json:"animeId"`
	AgeID         string         `json:"ageId"`
	Title         string         `json:"title"`
	URL           string         `json:"url"`
	Episodes      []Episode      `json:"episodes"`
	Lines         map[int]string `json:"lines"`
	CheckedAt     string         `json:"checkedAt"`
	Issue         string         `json:"issue,omitempty"`
	IkunCheckedAt string         `json:"ikunCheckedAt,omitempty"`
	SkrCheckedAt  string         `json:"skrCheckedAt,omitempty"`
}
type View struct {
	Items           []Entry  `json:"items"`
	Running         bool     `json:"running"`
	Done            int      `json:"done"`
	Total           int      `json:"total"`
	LastAttempt     string   `json:"lastAttempt"`
	IntervalHours   int      `json:"intervalHours"`
	Issues          []string `json:"issues"`
	CooldownSeconds int      `json:"cooldownSeconds,omitempty"`
}
type Service struct {
	leases          map[string]mediaLease
	lookups         map[int]bool
	resolving       map[string]bool
	resolveFailures map[string]resolveFailure
	lineFailures    map[string]resolveFailure
	mu              sync.Mutex
	cooldownMu      sync.Mutex
	state           View
	path            string
	cooldownPath    string
	store           *catalog.Store
	client          *http.Client
	upstream        *upstreamGate
	ikunUpstream    *upstreamGate
	skrUpstream     *upstreamGate
	preferred       map[string]string
	probeGates      *hostGates
	mediaGates      *hostGates
	mediaSlots      chan struct{}
}

type resolveFailure struct {
	message string
	until   time.Time
}

func sourceFresh(checkedAt string) bool {
	checked, err := time.Parse(time.RFC3339, checkedAt)
	return err == nil && time.Since(checked) < 24*time.Hour
}

func Open(path string, store *catalog.Store) *Service {
	s := &Service{path: path, cooldownPath: path + ".cooldown", store: store, client: &http.Client{Timeout: 15 * time.Second}, upstream: newUpstreamGate(1500 * time.Millisecond), ikunUpstream: newUpstreamGate(1500 * time.Millisecond), skrUpstream: newUpstreamGate(1500 * time.Millisecond), probeGates: newHostGates(time.Second), mediaGates: newHostGates(250 * time.Millisecond), mediaSlots: make(chan struct{}, 4), state: View{Items: []Entry{}, Issues: []string{}, IntervalHours: 6}}
	if b, e := os.ReadFile(s.cooldownPath); e == nil {
		if until, e := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); e == nil && until.After(time.Now()) {
			s.upstream.blocked = until
		}
	}
	s.upstream.onBlock = s.persistUpstreamCooldown
	if b, e := os.ReadFile(s.cooldownPath + ".ikun"); e == nil {
		if until, e := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); e == nil && until.After(time.Now()) {
			s.ikunUpstream.blocked = until
		}
	}
	s.ikunUpstream.onBlock = func(until time.Time) { s.persistCooldown(s.cooldownPath+".ikun", s.ikunUpstream, until) }
	if b, e := os.ReadFile(s.cooldownPath + ".skr"); e == nil {
		if until, e := time.Parse(time.RFC3339, strings.TrimSpace(string(b))); e == nil && until.After(time.Now()) {
			s.skrUpstream.blocked = until
		}
	}
	s.skrUpstream.onBlock = func(until time.Time) { s.persistCooldown(s.cooldownPath+".skr", s.skrUpstream, until) }
	if b, e := os.ReadFile(path); e == nil {
		var old View
		if json.Unmarshal(b, &old) == nil && old.IntervalHours == 6 {
			s.state = old
			s.state.Running = false
		}
	}
	return s
}

func (s *Service) persistUpstreamCooldown(until time.Time) {
	s.persistCooldown(s.cooldownPath, s.upstream, until)
}

func (s *Service) persistCooldown(path string, gate *upstreamGate, until time.Time) {
	s.cooldownMu.Lock()
	defer s.cooldownMu.Unlock()
	if path == "" {
		return
	}
	gate.mu.Lock()
	if gate.blocked.After(until) {
		until = gate.blocked
	}
	gate.mu.Unlock()
	if e := os.WriteFile(path+".tmp", []byte(until.UTC().Format(time.RFC3339)+"\n"), 0600); e == nil {
		os.Rename(path+".tmp", path)
	}
}
func (s *Service) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.state)
	var v View
	json.Unmarshal(b, &v)
	if remaining := s.upstream.cooldownRemaining(); remaining > 0 {
		v.CooldownSeconds = int(remaining.Seconds()) + 1
	} else {
		v.CooldownSeconds = 0
	}
	return v
}
func (s *Service) Start(ctx context.Context) bool {
	if s.upstream.cooldownRemaining() > 0 {
		return false
	}
	s.mu.Lock()
	t, _ := time.Parse(time.RFC3339, s.state.LastAttempt)
	if s.state.Running || time.Since(t) < time.Minute {
		s.mu.Unlock()
		return false
	}
	s.state.Running = true
	s.state.LastAttempt = time.Now().UTC().Format(time.RFC3339)
	s.state.Done = 0
	s.state.Total = len(s.store.Snapshot().Items)
	s.state.Issues = []string{}
	s.mu.Unlock()
	go s.refresh(ctx)
	return true
}
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		v := s.View()
		t, _ := time.Parse(time.RFC3339, v.LastAttempt)
		if time.Since(t) >= 6*time.Hour {
			s.Start(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (s *Service) get(ctx context.Context, path string) (string, error) {
	if e := s.upstream.wait(ctx); e != nil {
		return "", e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", origin+path, nil)
	if e != nil {
		return "", e
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Banxuan/1.0)")
	resp, e := s.client.Do(req)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	s.upstream.observe(resp.StatusCode, resp.Header)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return string(b), e
}

var tags = regexp.MustCompile(`<[^>]*>`)

func text(s string) string {
	return strings.TrimSpace(html.UnescapeString(tags.ReplaceAllString(s, "")))
}
func norm(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

var detailRE = regexp.MustCompile(`/detail/([0-9]+)`)
var titleRE = regexp.MustCompile(`(?s)<h2[^>]*class="video_detail_title"[^>]*>(.*?)</h2>`)
var nativeRE = regexp.MustCompile(`原版名称：</span>\s*<span[^>]*>(.*?)</span>`)
var episodeRE = regexp.MustCompile(`<a href="(/play/([0-9]+)/([0-9]+)/([0-9]+))"[^>]*>([^<]+)</a>`)
var lineRE = regexp.MustCompile(`(?s)<button[^>]*data-bs-target="#playlist-source-[^"]+"[^>]*>(.*?)</button>`)

func parse(a catalog.Anime, id, body string) (Entry, error) {
	title := titleRE.FindStringSubmatch(body)
	native := nativeRE.FindStringSubmatch(body)
	if len(title) < 2 || !((len(native) > 1 && a.Native != "" && norm(text(native[1])) == norm(a.Native)) || norm(text(title[1])) == norm(a.Title)) {
		return Entry{}, fmt.Errorf("作品名称或季数不匹配")
	}
	entry := Entry{AnimeID: a.ID, AgeID: id, Title: text(title[1]), URL: origin + "/detail/" + id, CheckedAt: time.Now().UTC().Format(time.RFC3339), Episodes: []Episode{}, Lines: map[int]string{}}
	labels := []string{}
	for _, m := range lineRE.FindAllStringSubmatch(body, -1) {
		labels = append(labels, text(m[1]))
	}
	lineOrder := []int{}
	for _, m := range episodeRE.FindAllStringSubmatch(body, -1) {
		if m[2] != id {
			continue
		}
		line, _ := strconv.Atoi(m[3])
		if _, ok := entry.Lines[line]; !ok {
			entry.Lines[line] = "线路 " + strconv.Itoa(line)
			lineOrder = append(lineOrder, line)
		}
		entry.Episodes = append(entry.Episodes, Episode{Label: text(m[5]), Path: m[1], Line: line})
	}
	if len(labels) == len(lineOrder) {
		for i, line := range lineOrder {
			entry.Lines[line] = labels[i]
		}
	}
	if len(entry.Episodes) == 0 {
		return Entry{}, fmt.Errorf("暂未发现选集")
	}
	return entry, nil
}
func (s *Service) find(ctx context.Context, a catalog.Anime) (Entry, error) {
	for _, old := range s.View().Items {
		if old.AnimeID == a.ID && old.AgeID != "" {
			b, e := s.get(ctx, "/detail/"+old.AgeID)
			if e != nil {
				return Entry{}, e
			}
			return parse(a, old.AgeID, b)
		}
	}
	if a.ID == 501963 {
		body, err := s.get(ctx, "/detail/20260205")
		if err != nil {
			return Entry{}, err
		}
		return parse(a, "20260205", body)
	}
	query := []rune(a.Title)
	if len(query) > 8 {
		query = query[:8]
	}
	queries := []string{string(query)}
	// Source sites often write a sequel as Ⅱ instead of “第二季”. Search the
	// franchise name too, then accept only an exact original-title match.
	if at := strings.Index(a.Title, "第"); at > 0 {
		base := strings.TrimSpace(a.Title[:at])
		if len([]rune(base)) >= 2 && base != queries[0] {
			queries = append(queries, base)
		}
	}
	seen := map[string]bool{}
	count := 0
	for _, search := range queries {
		b, e := s.get(ctx, "/search?query="+url.QueryEscape(search))
		if e != nil {
			continue
		}
		for _, m := range detailRE.FindAllStringSubmatch(b, -1) {
			id := m[1]
			if seen[id] {
				continue
			}
			seen[id] = true
			count++
			if count > 12 {
				return s.findIkun(ctx, a)
			}
			body, err := s.get(ctx, "/detail/"+id)
			if err != nil {
				continue
			}
			entry, err := parse(a, id, body)
			if err == nil {
				return entry, nil
			}
		}
	}
	return s.findIkun(ctx, a)
}
func (s *Service) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	for _, a := range s.store.Snapshot().Items {
		if ctx.Err() != nil {
			break
		}
		entry, e := s.find(ctx, a)
		s.mu.Lock()
		s.state.Done++
		if e != nil {
			s.state.Issues = append(s.state.Issues, a.Title+"："+e.Error())
			for i, v := range s.state.Items {
				if v.AnimeID == a.ID {
					s.state.Items[i].Issue = e.Error()
				}
			}
		} else {
			found := false
			for i, v := range s.state.Items {
				if v.AnimeID == a.ID {
					entry = mergeIkun(entry, Entry{Episodes: v.Episodes, Lines: v.Lines})
					entry.IkunCheckedAt = v.IkunCheckedAt
					entry = mergeSkr(entry, Entry{Episodes: v.Episodes, Lines: v.Lines})
					entry.SkrCheckedAt = v.SkrCheckedAt
					s.state.Items[i] = entry
					found = true
				}
			}
			if !found {
				s.state.Items = append(s.state.Items, entry)
			}
		}
		s.mu.Unlock()
		if s.upstream.cooldownRemaining() > 0 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Running = false
	b, e := json.MarshalIndent(s.state, "", "  ")
	if e == nil {
		e = os.WriteFile(s.path+".tmp", b, 0600)
	}
	if e == nil {
		e = os.Rename(s.path+".tmp", s.path)
	}
	if e != nil {
		s.state.Issues = append(s.state.Issues, "片源快照保存失败")
	}
}

var iframeRE = regexp.MustCompile(`<iframe\s+id="iframeForVideo"\s+src="([^"]+)"`)

type Resolved struct {
	Src      string `json:"src"`
	Kind     string `json:"kind"`
	Line     int    `json:"line"`
	Path     string `json:"path"`
	Label    string `json:"label"`
	Fallback bool   `json:"fallback"`
}

var videoRE = regexp.MustCompile(`var Vurl\s*=\s*['"]([^'"]+)['"]`)
var episodeNumberRE = regexp.MustCompile(`[0-9]+`)
var numberedEpisodeRE = regexp.MustCompile(`^第?([0-9]+)[集话回]$`)

func episodeKey(label string) string {
	key := norm(episodeNumberRE.ReplaceAllStringFunc(label, func(part string) string {
		part = strings.TrimLeft(part, "0")
		if part == "" {
			return "0"
		}
		return part
	}))
	if match := numberedEpisodeRE.FindStringSubmatch(key); len(match) > 1 {
		return "第" + match[1] + "集"
	}
	return key
}

func movieVersion(label string) string {
	if episodeNumberRE.MatchString(label) {
		return ""
	}
	key := episodeKey(label)
	switch {
	case strings.Contains(key, "国语"):
		return "zh"
	case strings.Contains(key, "日语"), strings.Contains(key, "日文"), strings.Contains(key, "中字"), strings.Contains(key, "原声"):
		return "ja"
	case key == "正片", key == "全集", key == "hd", key == "高清", key == "hd高清", key == "完整版":
		return "any"
	}
	return ""
}

func sameEpisodeLabel(a, b string) bool {
	if episodeKey(a) == episodeKey(b) {
		return true
	}
	av, bv := movieVersion(a), movieVersion(b)
	return av != "" && bv != "" && (av == bv || av == "any" || bv == "any")
}

func episodeCandidates(entry Entry, selected Episode) []Episode {
	candidates := []Episode{selected}
	sameSource := []Episode{}
	otherSource := []Episode{}
	for _, ep := range entry.Episodes {
		if ep.Path != selected.Path && sameEpisodeLabel(ep.Label, selected.Label) {
			if ep.Source == selected.Source {
				sameSource = append(sameSource, ep)
			} else {
				otherSource = append(otherSource, ep)
			}
		}
	}
	if len(sameSource) > 0 {
		candidates = append(candidates, sameSource[0])
		sameSource = sameSource[1:]
	}
	seen := map[string]bool{selected.Source: true}
	for _, ep := range otherSource {
		if !seen[ep.Source] {
			candidates = append(candidates, ep)
			seen[ep.Source] = true
		}
	}
	for _, ep := range otherSource {
		found := false
		for _, chosen := range candidates {
			if chosen.Path == ep.Path {
				found = true
				break
			}
		}
		if !found {
			candidates = append(candidates, ep)
		}
	}
	candidates = append(candidates, sameSource...)
	return candidates
}

func (s *Service) ResolveAuto(ctx context.Context, animeID int, label string) (Resolved, error) {
	var matches []Episode
	for _, entry := range s.View().Items {
		if entry.AnimeID == animeID {
			for _, ep := range entry.Episodes {
				if sameEpisodeLabel(ep.Label, label) {
					matches = append(matches, ep)
				}
			}
			break
		}
	}
	if len(matches) == 0 || label == "" {
		return Resolved{}, fmt.Errorf("未收录的选集")
	}
	key := fmt.Sprintf("%d:%s", animeID, episodeKey(label))
	s.mu.Lock()
	preferred := s.preferred[key]
	s.mu.Unlock()
	for _, ep := range matches {
		if ep.Path == preferred && s.lineFailure(ep.Path) == "" {
			return s.Resolve(ctx, animeID, ep.Path)
		}
	}
	// A generic "正片" does not say which dub it carries. Prefer a labelled
	// Chinese edition when the viewer explicitly selected 国语版.
	if movieVersion(label) == "zh" {
		for _, ep := range matches {
			if ep.Source == "skr" && movieVersion(ep.Label) == "zh" && s.lineFailure(ep.Path) == "" {
				return s.Resolve(ctx, animeID, ep.Path)
			}
		}
	}
	// iKun provides direct media addresses. Keep AGE and Skr as verified fallbacks.
	for _, ep := range matches {
		if ep.Source == "ikun" && s.lineFailure(ep.Path) == "" {
			return s.Resolve(ctx, animeID, ep.Path)
		}
	}
	for _, ep := range matches {
		if s.lineFailure(ep.Path) == "" {
			return s.Resolve(ctx, animeID, ep.Path)
		}
	}
	return Resolved{}, fmt.Errorf("该集的片源暂不可用，请稍后重试")
}

func (s *Service) ReportFailure(animeID int, path string) bool {
	for _, entry := range s.View().Items {
		if entry.AnimeID != animeID {
			continue
		}
		for _, ep := range entry.Episodes {
			if ep.Path == path {
				s.rememberLineFailure(path, "播放器加载失败")
				s.mu.Lock()
				key := fmt.Sprintf("%d:%s", animeID, episodeKey(ep.Label))
				if s.preferred[key] == path {
					delete(s.preferred, key)
				}
				s.mu.Unlock()
				return true
			}
		}
	}
	return false
}

func (s *Service) lineFailure(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if failure, ok := s.lineFailures[path]; ok && time.Now().Before(failure.until) {
		return failure.message
	}
	return ""
}
func (s *Service) rememberLineFailure(path, message string) {
	s.mu.Lock()
	if s.lineFailures == nil || len(s.lineFailures) > 500 {
		s.lineFailures = map[string]resolveFailure{}
	}
	s.lineFailures[path] = resolveFailure{message: message, until: time.Now().Add(10 * time.Minute)}
	s.mu.Unlock()
}

func (s *Service) resolveEpisode(ctx context.Context, ep Episode) (string, error) {
	if ep.Source == "skr" {
		return s.resolveSkrEpisode(ctx, ep)
	}
	if ep.Source == "ikun" {
		if ep.DirectURL == "" {
			return "", fmt.Errorf("缺少播放地址")
		}
		return ep.DirectURL, nil
	}
	body, e := s.get(ctx, ep.Path)
	if e != nil {
		return "", e
	}
	m := iframeRE.FindStringSubmatch(body)
	if len(m) < 2 {
		return "", fmt.Errorf("未找到播放器")
	}
	src := html.UnescapeString(m[1])
	u, e := url.Parse(src)
	if e != nil || u.Scheme != "https" || u.Host != "jx.wuzhoupai.com:8443" || u.User != nil {
		return "", fmt.Errorf("播放器地址尚未核验")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", src, nil)
	if e != nil {
		return "", e
	}
	req.Header.Set("Referer", origin+ep.Path)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if e := s.upstream.wait(ctx); e != nil {
		return "", e
	}
	response, e := s.client.Do(req)
	if e != nil {
		return "", e
	}
	defer response.Body.Close()
	s.upstream.observe(response.StatusCode, response.Header)
	if response.StatusCode != 200 {
		return "", fmt.Errorf("播放器返回 HTTP %d", response.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil {
		return "", e
	}
	m = videoRE.FindStringSubmatch(string(b))
	if len(m) < 2 {
		return "", fmt.Errorf("该线路未提供可直接播放的视频地址")
	}
	if !strings.HasPrefix(m[1], "https://") {
		return "", fmt.Errorf("该线路使用源站加密播放器，请切换线路")
	}
	return html.UnescapeString(m[1]), nil
}
func (s *Service) Resolve(ctx context.Context, animeID int, path string) (Resolved, error) {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	var entry Entry
	var selected Episode
	found := false
	for _, v := range s.View().Items {
		if v.AnimeID == animeID {
			entry = v
			for _, ep := range v.Episodes {
				if ep.Path == path {
					selected = ep
					found = true
				}
			}
		}
	}
	if !found {
		return Resolved{}, fmt.Errorf("未收录的选集")
	}
	key := fmt.Sprintf("%d:%s:%d", animeID, episodeKey(selected.Label), selected.Line)
	s.mu.Lock()
	if failure, ok := s.resolveFailures[key]; ok && time.Now().Before(failure.until) {
		s.mu.Unlock()
		return Resolved{}, fmt.Errorf("%s", failure.message)
	}
	if s.resolving == nil {
		s.resolving = map[string]bool{}
	}
	if s.resolving[key] {
		s.mu.Unlock()
		return Resolved{}, fmt.Errorf("该集正在解析，请稍后重试")
	}
	s.resolving[key] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.resolving, key); s.mu.Unlock() }()
	candidates := episodeCandidates(entry, selected)
	var issues []string
	for _, ep := range candidates {
		if len(issues) >= 6 {
			break
		}
		if cached := s.lineFailure(ep.Path); cached != "" {
			issues = append(issues, fmt.Sprintf("线路 %d：%s", ep.Line, cached))
			continue
		}
		media, e := s.resolveEpisode(ctx, ep)
		if e != nil {
			if strings.Contains(e.Error(), "HTTP 403") || strings.Contains(e.Error(), "源站加密播放器") {
				s.rememberLineFailure(ep.Path, e.Error())
			}
			issues = append(issues, fmt.Sprintf("线路 %d：%s", ep.Line, e))
			continue
		}
		lease, e := newLease(media)
		if e != nil {
			issues = append(issues, "视频地址不符合播放范围")
			continue
		}
		kind, e := s.probeMedia(ctx, lease)
		if e != nil {
			if strings.Contains(e.Error(), "HTTP 403") || strings.Contains(e.Error(), "HTTP 404") || strings.Contains(e.Error(), "清单") || strings.Contains(e.Error(), "分片") {
				s.rememberLineFailure(ep.Path, e.Error())
			}
			issues = append(issues, fmt.Sprintf("线路 %d：%s", ep.Line, e))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		token, e := s.register(lease)
		if e != nil {
			return Resolved{}, e
		}
		label := entry.Lines[ep.Line]
		if ep.Source == "" {
			label = "AGE · " + label
		}
		s.mu.Lock()
		if s.preferred == nil {
			s.preferred = map[string]string{}
		}
		s.preferred[fmt.Sprintf("%d:%s", animeID, episodeKey(ep.Label))] = ep.Path
		s.mu.Unlock()
		return Resolved{Src: lease.proxyURL(token, lease.root), Kind: kind, Line: ep.Line, Path: ep.Path, Label: label, Fallback: ep.Line != selected.Line}, nil
	}
	message := "该集暂未找到可播放线路。" + strings.Join(issues, "；")
	if ctx.Err() != nil {
		return Resolved{}, ctx.Err()
	}
	s.mu.Lock()
	if s.resolveFailures == nil {
		s.resolveFailures = map[string]resolveFailure{}
	}
	if len(s.resolveFailures) > 500 {
		s.resolveFailures = map[string]resolveFailure{}
	}
	s.resolveFailures[key] = resolveFailure{message: message, until: time.Now().Add(10 * time.Minute)}
	s.mu.Unlock()
	return Resolved{}, fmt.Errorf("%s", message)
}

func (s *Service) Lookup(ctx context.Context, a catalog.Anime) (Entry, error) {
	s.mu.Lock()
	var cached Entry
	hasCached := false
	for _, v := range s.state.Items {
		if v.AnimeID == a.ID {
			cached = v
			hasCached = true
			if sourceFresh(v.IkunCheckedAt) && sourceFresh(v.SkrCheckedAt) {
				s.mu.Unlock()
				return v, nil
			}
			break
		}
	}
	if s.lookups == nil {
		s.lookups = map[int]bool{}
	}
	if s.lookups[a.ID] {
		s.mu.Unlock()
		return Entry{}, fmt.Errorf("该作品正在查找片源")
	}
	s.lookups[a.ID] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.lookups, a.ID); s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	entry := cached
	var e error
	if !hasCached {
		entry, e = s.find(ctx, a)
		if e != nil {
			entry, e = s.findSkr(ctx, a)
			if e != nil {
				return Entry{}, e
			}
		}
	}
	if !sourceFresh(entry.IkunCheckedAt) {
		if extra, err := s.findIkun(ctx, a); err == nil {
			entry = mergeIkun(entry, extra)
		} else {
			entry.IkunCheckedAt = time.Now().Add(-23 * time.Hour).UTC().Format(time.RFC3339)
		}
	}
	if !sourceFresh(entry.SkrCheckedAt) {
		if extra, err := s.findSkr(ctx, a); err == nil {
			entry = mergeSkr(entry, extra)
		} else {
			entry.SkrCheckedAt = time.Now().Add(-23 * time.Hour).UTC().Format(time.RFC3339)
		}
	}
	s.mu.Lock()
	if hasCached {
		for i, v := range s.state.Items {
			if v.AnimeID == a.ID {
				s.state.Items[i] = entry
				break
			}
		}
	} else {
		s.state.Items = append(s.state.Items, entry)
	}
	for key := range s.resolveFailures {
		if strings.HasPrefix(key, strconv.Itoa(a.ID)+":") {
			delete(s.resolveFailures, key)
		}
	}
	b, e := json.MarshalIndent(s.state, "", "  ")
	if e == nil {
		e = os.WriteFile(s.path+".tmp", b, 0600)
	}
	if e == nil {
		e = os.Rename(s.path+".tmp", s.path)
	}
	s.mu.Unlock()
	if e != nil {
		return Entry{}, e
	}
	return entry, nil
}
