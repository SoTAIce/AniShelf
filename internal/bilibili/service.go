package bilibili

import (
	"anime-shelf/internal/catalog"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type State struct {
	UID           string      `json:"uid"`
	IntervalHours int         `json:"intervalHours"`
	Running       bool        `json:"running"`
	LastAttempt   string      `json:"lastAttempt"`
	LastSuccess   string      `json:"lastSuccess"`
	NextSync      string      `json:"nextSync"`
	Error         string      `json:"error"`
	Items         []Entry     `json:"items"`
	Mappings      map[int]int `json:"mappings"`
}
type Taste struct {
	Genre string `json:"genre"`
	Count int    `json:"count"`
}
type View struct {
	State
	Matched int     `json:"matched"`
	Taste   []Taste `json:"taste"`
}
type Service struct {
	mu     sync.Mutex
	state  State
	path   string
	store  *catalog.Store
	client *Client
}

func Open(path string, store *catalog.Store, c *Client) (*Service, error) {
	s := &Service{path: path, store: store, client: c, state: State{IntervalHours: 24, Items: []Entry{}, Mappings: map[int]int{}}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(b, &s.state); err != nil {
			return nil, fmt.Errorf("B站同步配置损坏: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.state.Running = false
	if s.state.Items == nil {
		s.state.Items = []Entry{}
	}
	if s.state.Mappings == nil {
		s.state.Mappings = map[int]int{}
	}
	return s, nil
}
func (s *Service) saveLocked() error {
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(s.path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(s.path+".tmp", s.path)
}
func (s *Service) Configure(uid string, hours int) error {
	if !regexp.MustCompile(`^[1-9][0-9]{0,19}$`).MatchString(uid) {
		return fmt.Errorf("请输入有效的 B站 UID")
	}
	if hours != 0 && hours != 6 && hours != 12 && hours != 24 {
		return fmt.Errorf("同步周期支持 6、12、24 小时或关闭")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Running {
		return fmt.Errorf("同步中，请完成后再修改")
	}
	old := s.state
	if uid != s.state.UID {
		s.state = State{UID: uid, Items: []Entry{}, Mappings: map[int]int{}}
	}
	s.state.IntervalHours = hours
	s.state.NextSync = next(s.state.LastAttempt, hours)
	if err := s.saveLocked(); err != nil {
		s.state = old
		return fmt.Errorf("同步设置保存失败")
	}
	return nil
}
func next(last string, h int) string {
	if h <= 0 {
		return ""
	}
	t, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return t.Add(time.Duration(h) * time.Hour).UTC().Format(time.RFC3339)
}
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.state.UID == "" {
		s.mu.Unlock()
		return fmt.Errorf("请先设置你的 UID")
	}
	if s.state.Running {
		s.mu.Unlock()
		return nil
	}
	last, _ := time.Parse(time.RFC3339, s.state.LastAttempt)
	if time.Since(last) < 5*time.Minute {
		s.mu.Unlock()
		return fmt.Errorf("请至少间隔 5 分钟再同步")
	}
	old := s.state
	s.state.Running = true
	s.state.LastAttempt = time.Now().UTC().Format(time.RFC3339)
	s.state.NextSync = next(s.state.LastAttempt, s.state.IntervalHours)
	s.state.Error = ""
	if err := s.saveLocked(); err != nil {
		s.state = old
		s.mu.Unlock()
		return fmt.Errorf("同步状态保存失败")
	}
	uid := s.state.UID
	s.mu.Unlock()
	go func() {
		job, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		entries, err := s.client.Fetch(job, uid)
		s.mu.Lock()
		defer s.mu.Unlock()
		previous := s.state
		s.state.Running = false
		if err != nil {
			s.state.Error = err.Error()
		} else {
			s.state.Items = entries
			s.state.LastSuccess = time.Now().UTC().Format(time.RFC3339)
			s.state.Error = ""
		}
		if err := s.saveLocked(); err != nil {
			s.state = previous
			s.state.Running = false
			s.state.Error = "追番保存失败，保留上次记录"
		}
	}()
	return nil
}
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	check := func() {
		s.mu.Lock()
		due := s.state.UID != "" && s.state.IntervalHours > 0 && !s.state.Running
		at, _ := time.Parse(time.RFC3339, s.state.NextSync)
		due = due && !time.Now().Before(at)
		s.mu.Unlock()
		if due {
			s.Start(ctx)
		}
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			check()
		}
	}
}
func key(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 0xff01 && r <= 0xff5e {
			r -= 0xfee0
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func Match(items []Entry, d catalog.Dataset, mappings map[int]int) []Entry {
	aliases := map[string]map[int]bool{}
	valid := map[int]bool{}
	for _, a := range d.Items {
		valid[a.ID] = true
		for _, title := range []string{a.Title, a.Native, a.English} {
			k := key(title)
			if k == "" {
				continue
			}
			if aliases[k] == nil {
				aliases[k] = map[int]bool{}
			}
			aliases[k][a.ID] = true
		}
	}
	out := append([]Entry{}, items...)
	for i, e := range out {
		out[i].AnimeID = 0
		if id, ok := mappings[e.SeasonID]; ok {
			if valid[id] {
				out[i].AnimeID = id
			}
			continue
		}
		candidates := aliases[key(e.Title)]
		if len(candidates) == 1 {
			for id := range candidates {
				out[i].AnimeID = id
			}
		}
	}
	return out
}
func (s *Service) View() View {
	d := s.store.Snapshot()
	s.mu.Lock()
	st := s.state
	st.Items = Match(s.state.Items, d, s.state.Mappings)
	st.Mappings = map[int]int{}
	for k, v := range s.state.Mappings {
		st.Mappings[k] = v
	}
	s.mu.Unlock()
	counts := map[string]int{}
	matched := 0
	unique := map[int]bool{}
	for _, e := range st.Items {
		if e.AnimeID > 0 {
			matched++
			unique[e.AnimeID] = true
		}
	}
	for _, a := range d.Items {
		if unique[a.ID] {
			for _, g := range a.Genres {
				counts[g]++
			}
		}
	}
	taste := []Taste{}
	for g, n := range counts {
		taste = append(taste, Taste{g, n})
	}
	sort.Slice(taste, func(i, j int) bool {
		if taste[i].Count == taste[j].Count {
			return taste[i].Genre < taste[j].Genre
		}
		return taste[i].Count > taste[j].Count
	})
	return View{st, matched, taste}
}
func (s *Service) Apply(p catalog.Preferences) catalog.Preferences {
	return s.ApplyTo(p, s.store.Snapshot())
}

// ApplyTo matches imported follows against the recommendation candidate pool,
// which can include anime outside the curated multi-source ranking.
func (s *Service) ApplyTo(p catalog.Preferences, d catalog.Dataset) catalog.Preferences {
	s.mu.Lock()
	items := Match(s.state.Items, d, s.state.Mappings)
	s.mu.Unlock()
	byID := map[int]float64{}
	for _, e := range items {
		if e.AnimeID == 0 {
			continue
		}
		w := .6
		if e.Status == 2 {
			w = 1
		}
		if e.Status == 3 {
			w = 1.2
		}
		if w > byID[e.AnimeID] {
			byID[e.AnimeID] = w
		}
	}
	p.Imported = nil
	for id, w := range byID {
		p.Imported = append(p.Imported, catalog.Seed{ID: id, Weight: w})
	}
	return p
}
func (s *Service) Map(season, id int) error {
	valid := id == 0
	for _, a := range s.store.Snapshot().Items {
		if a.ID == id {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("所选作品不存在")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, e := range s.state.Items {
		if e.SeasonID == season {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("追番条目不存在")
	}
	old, existed := s.state.Mappings[season]
	s.state.Mappings[season] = id
	if err := s.saveLocked(); err != nil {
		if existed {
			s.state.Mappings[season] = old
		} else {
			delete(s.state.Mappings, season)
		}
		return fmt.Errorf("匹配保存失败")
	}
	return nil
}
