package bilibili

import (
	"anime-shelf/internal/catalog"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestClientPaginatesAndRejectsPartialData(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("vmid") != "123" || r.URL.Query().Get("type") != "1" {
			t.Error("wrong account or media type")
		}
		if r.Header.Get("Cookie") != "SESSDATA=test-only" {
			t.Error("missing local credential")
		}
		if r.URL.Query().Get("pn") == "1" {
			fmt.Fprint(w, `{"code":0,"data":{"total":2,"list":[{"season_id":10,"title":"第一季","follow_status":2}]}}`)
		} else {
			fmt.Fprint(w, `{"code":0,"data":{"total":2,"list":[{"season_id":11,"title":"第二季","follow_status":3}]}}`)
		}
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), BaseURL: server.URL, Cookie: "test-only"}
	items, err := c.Fetch(context.Background(), "123")
	if err != nil || len(items) != 2 || calls != 2 {
		t.Fatalf("pagination: %v %v", items, err)
	}
	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":53013,"message":"隐私设置未公开"}`)
	}))
	defer deny.Close()
	c.BaseURL = deny.URL
	if _, err = c.Fetch(context.Background(), "123"); err == nil {
		t.Fatal("private list treated as empty success")
	}
}
func TestMatchDoesNotMergeSeasonsOrAmbiguousTitles(t *testing.T) {
	d := catalog.Dataset{Items: []catalog.Anime{{ID: 1, Title: "动漫 第一季"}, {ID: 2, Title: "动漫 第二季"}, {ID: 3, Title: "重复"}, {ID: 4, Title: "重复"}}}
	items := []Entry{{SeasonID: 10, Title: "动漫 第二季"}, {SeasonID: 11, Title: "动漫"}, {SeasonID: 12, Title: "重复"}}
	got := Match(items, d, nil)
	if got[0].AnimeID != 2 || got[1].AnimeID != 0 || got[2].AnimeID != 0 {
		t.Fatalf("unsafe match: %#v", got)
	}
	got = Match(items, d, map[int]int{10: 0, 12: 3})
	if got[0].AnimeID != 0 || got[2].AnimeID != 3 {
		t.Fatal("explicit corrections ignored")
	}
}
func TestFailurePreservesImportedListAndSchedule(t *testing.T) {
	store, err := catalog.Load(filepath.Join("..", "..", "data", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"code":53013}`) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "bilibili.json")
	c := &Client{HTTP: server.Client(), BaseURL: server.URL}
	s, err := Open(path, store, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Configure("123", 24); err != nil {
		t.Fatal(err)
	}
	s.state.Items = []Entry{{SeasonID: 1, Title: "已有追番"}}
	s.state.LastSuccess = "2026-09-28T00:00:00Z"
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for s.View().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	v := s.View()
	if v.Running || v.Error == "" || len(v.Items) != 1 || v.LastSuccess != "2026-09-28T00:00:00Z" || v.NextSync == "" {
		t.Fatalf("lost prior data: %#v", v)
	}
	restored, err := Open(path, store, c)
	if err != nil {
		t.Fatal(err)
	}
	if restored.View().NextSync != v.NextSync {
		t.Fatal("schedule not persisted")
	}
	if s.Start(context.Background()) == nil {
		t.Fatal("cooldown bypassed")
	}
}

func TestImportedProfileReachesRecommender(t *testing.T) {
	store, err := catalog.Load(filepath.Join("..", "..", "data", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := store.Snapshot()
	seed := d.Items[0]
	s, err := Open(filepath.Join(t.TempDir(), "bilibili.json"), store, NewClient(""))
	if err != nil {
		t.Fatal(err)
	}
	s.state.Items = []Entry{{SeasonID: 42, Title: seed.Title, Status: 2}}
	view := s.View()
	if view.Matched != 1 || len(view.Taste) == 0 {
		t.Fatal("imported taste not analyzed")
	}
	prefs := s.Apply(catalog.Preferences{})
	if len(prefs.Imported) != 1 || prefs.Imported[0].Weight != 1 {
		t.Fatal("following misrepresented as explicit liking")
	}
	recs := catalog.Recommend(d, prefs)
	if len(recs) == 0 {
		t.Fatal("no recommendations from imported profile")
	}
	for _, r := range recs {
		if r.Anime.ID == seed.ID {
			t.Fatal("already followed show recommended again")
		}
	}
	if err = s.Map(42, 0); err != nil {
		t.Fatal(err)
	}
	if s.View().Matched != 0 {
		t.Fatal("manual exclusion ignored")
	}
}

func TestImportedFollowMatchesExpandedCandidatePool(t *testing.T) {
	store, err := catalog.Load(filepath.Join("..", "..", "data", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(t.TempDir(), "bilibili.json"), store, NewClient(""))
	if err != nil {
		t.Fatal(err)
	}
	s.state.Items = []Entry{{SeasonID: 77, Title: "全库之外的奇幻番", Status: 2}}
	d := store.Snapshot()
	d.Items = append(d.Items, catalog.Anime{ID: 999777, Title: "全库之外的奇幻番", Genres: []string{"奇幻"}})
	p := s.ApplyTo(catalog.Preferences{}, d)
	if len(p.Imported) != 1 || p.Imported[0].ID != 999777 {
		t.Fatalf("expanded Bilibili match: %+v", p.Imported)
	}
}
