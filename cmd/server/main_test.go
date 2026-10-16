package main

import (
	"anime-shelf/internal/mobile"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeRequestPassesOriginCheckOnlyWithConnectionKey(t *testing.T) {
	const key = "1234567890abcdef"
	handler := mobile.NewGateway(key).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func(supplied string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/playback/lookup/1", nil)
		r.Header.Set("Origin", "capacitor://localhost")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		if supplied != "" {
			r.Header.Set("X-AniShelf-Key", supplied)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if got := request(""); got != http.StatusUnauthorized {
		t.Fatalf("missing key returned %d", got)
	}
	if got := request(key); got != http.StatusNoContent {
		t.Fatalf("authorized native request returned %d", got)
	}
}
