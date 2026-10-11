package main

import (
	"anime-shelf/internal/bilibili"
	"anime-shelf/internal/catalog"
	"anime-shelf/internal/playback"
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address")
	data := flag.String("data", "data/catalog.json", "catalogue snapshot")
	web := flag.String("web", "frontend/dist", "built frontend")
	refresh := flag.Bool("refresh", false, "refresh source ratings then exit")
	interval := flag.Duration("refresh-interval", 30*time.Minute, "background rating refresh interval; 0 disables")
	flag.Parse()
	serviceCtx, stopService := context.WithCancel(context.Background())
	defer stopService()
	store, e := catalog.Load(*data)
	if e != nil {
		log.Fatal(e)
	}
	if *refresh {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		d, issues := catalog.Refresh(ctx, store.Snapshot())
		for _, issue := range issues {
			log.Println(issue)
		}
		if e := store.Replace(d); e != nil {
			log.Fatal(e)
		}
		log.Printf("Refresh finished; %d upstream issues, last good values retained", len(issues))
		return
	}
	explorer := catalog.NewExplorer(store)
	updater := catalog.NewUpdater(store, *interval)
	go updater.Run(serviceCtx)
	bili, err := bilibili.Open(filepath.Join(filepath.Dir(*data), "bilibili.json"), store, bilibili.NewClient(os.Getenv("BILIBILI_SESSDATA")))
	if err != nil {
		log.Fatal(err)
	}
	go bili.Run(serviceCtx)
	videos := playback.Open(filepath.Join(filepath.Dir(*data), "age-playback.json"), store)
	go videos.Run(serviceCtx)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/explore", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		year, _ := strconv.Atoi(q.Get("year"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		result, e := explorer.Search(r.Context(), q.Get("q"), year, offset, limit)
		if e != nil {
			w.WriteHeader(502)
			respond(w, map[string]string{"error": "动漫库暂时不可用，请稍后重试"})
			return
		}
		respond(w, result)
	})
	mux.HandleFunc("GET /api/explore/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil {
			http.Error(w, "invalid id", 400)
			return
		}
		item, e := explorer.Subject(r.Context(), id)
		if e != nil {
			http.Error(w, "not found", 404)
			return
		}
		respond(w, item)
	})
	mux.HandleFunc("POST /api/playback/lookup/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil {
			http.Error(w, "invalid id", 400)
			return
		}
		item, e := explorer.Subject(r.Context(), id)
		if e != nil {
			w.WriteHeader(404)
			respond(w, map[string]string{"error": "作品资料暂时不可用"})
			return
		}
		entry, e := videos.Lookup(r.Context(), item)
		if e != nil {
			w.WriteHeader(404)
			respond(w, map[string]string{"error": "暂未在已接入片源找到同季作品"})
			return
		}
		respond(w, entry)
	})

	mux.HandleFunc("GET /api/playback/media/{token}", videos.ServeMedia)
	mux.HandleFunc("GET /api/playback", func(w http.ResponseWriter, r *http.Request) { respond(w, videos.View()) })
	mux.HandleFunc("GET /api/playback/resolve", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		id, _ := strconv.Atoi(r.URL.Query().Get("animeId"))
		var src playback.Resolved
		var e error
		if episode := r.URL.Query().Get("episode"); episode != "" {
			src, e = videos.ResolveAuto(r.Context(), id, episode)
		} else {
			src, e = videos.Resolve(r.Context(), id, r.URL.Query().Get("path"))
		}
		if e != nil {
			w.WriteHeader(502)
			respond(w, map[string]string{"error": e.Error()})
			return
		}
		respond(w, src)
	})
	mux.HandleFunc("POST /api/playback/failure", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		var report struct {
			AnimeID int    `json:"animeId"`
			Path    string `json:"path"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&report) != nil || !videos.ReportFailure(report.AnimeID, report.Path) {
			w.WriteHeader(http.StatusBadRequest)
			respond(w, map[string]string{"error": "无效的选集"})
			return
		}
		respond(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/playback/sync", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		if !videos.Start(serviceCtx) {
			w.WriteHeader(429)
		} else {
			w.WriteHeader(202)
		}
		respond(w, videos.View())
	})
	mux.HandleFunc("GET /api/bilibili", func(w http.ResponseWriter, r *http.Request) { respond(w, bili.View()) })
	mux.HandleFunc("POST /api/bilibili/config", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		var p struct {
			UID           string `json:"uid"`
			IntervalHours int    `json:"intervalHours"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&p) != nil {
			http.Error(w, "invalid settings", 400)
			return
		}
		if err := bili.Configure(p.UID, p.IntervalHours); err != nil {
			w.WriteHeader(400)
			respond(w, map[string]string{"error": err.Error()})
			return
		}
		respond(w, bili.View())
	})
	mux.HandleFunc("POST /api/bilibili/sync", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		if err := bili.Start(serviceCtx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			respond(w, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		respond(w, bili.View())
	})
	mux.HandleFunc("POST /api/bilibili/match", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		var p struct {
			SeasonID int `json:"seasonId"`
			AnimeID  int `json:"animeId"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&p) != nil {
			http.Error(w, "invalid match", 400)
			return
		}
		if err := bili.Map(p.SeasonID, p.AnimeID); err != nil {
			w.WriteHeader(400)
			respond(w, map[string]string{"error": err.Error()})
			return
		}
		respond(w, bili.View())
	})
	mux.HandleFunc("GET /api/sync", func(w http.ResponseWriter, r *http.Request) { respond(w, updater.Status()) })
	mux.HandleFunc("POST /api/sync", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		started := updater.Start(serviceCtx)
		status := updater.Status()
		if !started && !status.Running {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(429)
			respond(w, map[string]string{"error": "请稍后再更新，抓取间隔至少一分钟。"})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(202)
		respond(w, status)
	})
	mux.HandleFunc("POST /api/recommendations", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "cross-origin requests not allowed", 403)
			return
		}
		var p catalog.Preferences
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&p); err != nil {
			http.Error(w, "invalid preferences", 400)
			return
		}
		if len(p.Genres) > 20 || len(p.Liked) > 500 || len(p.Saved) > 500 || len(p.Seen) > 500 || len(p.Disliked) > 500 {
			http.Error(w, "too many preferences", 400)
			return
		}
		base := store.Snapshot()
		ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
		defer cancel()
		byID := map[int]bool{}
		for _, a := range base.Items {
			byID[a.ID] = true
		}
		// A liked or saved search result can act as a taste seed even if it is
		// outside the sampled candidate pages. Bound extra upstream requests.
		added := 0
		for _, id := range append(append(append([]int{}, p.Liked...), p.Saved...), p.Disliked...) {
			if id <= 0 || byID[id] || added >= 6 || ctx.Err() != nil {
				continue
			}
			added++
			if a, err := explorer.Subject(ctx, id); err == nil {
				base.Items = append(base.Items, a)
				byID[id] = true
			}
		}
		initial := bili.Apply(p)
		tags := catalog.RecommendationTags(base, initial)
		pool, poolErr := explorer.RecommendationCandidates(ctx, tags)
		for _, a := range pool {
			if !byID[a.ID] {
				base.Items = append(base.Items, a)
				byID[a.ID] = true
			}
		}
		personalized := bili.ApplyTo(p, base)
		response := map[string]any{"items": catalog.Recommend(base, personalized), "personalized": len(p.Genres)+len(p.Liked)+len(p.Saved)+len(personalized.Imported) > 0, "candidateCount": len(base.Items), "wideCatalog": len(pool) > 0}
		if poolErr != nil {
			response["catalogIssue"] = "部分动漫库候选暂时无法更新，已使用可用作品推荐"
		}
		respond(w, response)
	})
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { respond(w, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/anime", func(w http.ResponseWriter, r *http.Request) {
		d := store.Snapshot()
		q := r.URL.Query()
		items := []catalog.Anime{}
		min, _ := strconv.ParseFloat(q.Get("minScore"), 64)
		year, _ := strconv.Atoi(q.Get("year"))
		minSources, _ := strconv.Atoi(q.Get("minSources"))
		for _, a := range d.Items {
			if !catalog.Match(a, q.Get("q")) {
				continue
			}
			if year > 0 && a.Year != year {
				continue
			}
			if q.Get("format") != "" && a.Format != q.Get("format") {
				continue
			}
			if min > 0 && (a.Composite == nil || *a.Composite < min) {
				continue
			}
			if minSources > 0 && len(a.Sources) < minSources {
				continue
			}
			if q.Get("genre") != "" {
				ok := false
				for _, g := range a.Genres {
					if g == q.Get("genre") {
						ok = true
					}
				}
				if !ok {
					continue
				}
			}
			if q.Get("scope") == "recent" && a.Year < 2020 {
				continue
			}
			items = append(items, a)
		}
		switch q.Get("sort") {
		case "newest":
			sort.SliceStable(items, func(i, j int) bool { return items[i].Date > items[j].Date })
		case "votes":
			sort.SliceStable(items, func(i, j int) bool { return items[i].Votes > items[j].Votes })
		}
		respond(w, map[string]any{"items": items, "total": len(items), "catalogTotal": len(d.Items), "fetchedAt": d.FetchedAt, "scope": d.Scope})
	})
	mux.HandleFunc("GET /api/anime/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil {
			http.Error(w, "invalid id", 400)
			return
		}
		for _, a := range store.Snapshot().Items {
			if a.ID == id {
				respond(w, a)
				return
			}
		}
		http.Error(w, "not found", 404)
	})
	fs := http.FileServer(http.Dir(*web))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean(r.URL.Path)
		if strings.HasPrefix(clean, "/api/") {
			http.NotFound(w, r)
			return
		}
		if clean == "/" {
			http.ServeFile(w, r, filepath.Join(*web, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; media-src 'self' blob:; font-src 'self'; frame-ancestors 'none'; base-uri 'self'")
		mux.ServeHTTP(w, r)
	})
	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-done
		stopService()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
	log.Printf("番选 http://%s", *addr)
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, e := url.Parse(origin)
	return e == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
}
