package mobile

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

type authorizedKey struct{}

type Gateway struct {
	key string
}

func NewGateway(key string) Gateway { return Gateway{key: key} }

func NeedsKey(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return true
	}
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func IsAuthorized(r *http.Request) bool {
	return r.Context().Value(authorizedKey{}) == true
}

func nativeOrigin(origin string) bool {
	return origin == "capacitor://localhost" || origin == "http://localhost" || origin == "https://localhost"
}

func (g Gateway) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if g.key != "" && nativeOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Range, X-AniShelf-Key")
			w.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range")
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && g.key != "" && !strings.HasPrefix(r.URL.Path, "/api/playback/media/") {
			provided := r.Header.Get("X-AniShelf-Key")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(g.key)) != 1 {
				w.Header().Set("Cache-Control", "no-store")
				http.Error(w, "invalid AniShelf connection key", http.StatusUnauthorized)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), authorizedKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
}
