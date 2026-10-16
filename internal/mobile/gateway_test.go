package mobile

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNeedsKeyForLAN(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8787", ":8787", "192.168.1.2:8787"} {
		if !NeedsKey(addr) {
			t.Fatalf("LAN address %q was left unprotected", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:8787", "localhost:8787", "[::1]:8787"} {
		if NeedsKey(addr) {
			t.Fatalf("loopback address %q unexpectedly requires a key", addr)
		}
	}
}

func TestMobileGatewayProtectsAPIAndAcceptsNativeOrigin(t *testing.T) {
	const key = "1234567890abcdef"
	gateway := NewGateway(key).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAuthorized(r) && r.URL.Path != "/api/playback/media/short-lease" {
			t.Fatal("protected request reached the handler without authorization")
		}
		w.WriteHeader(http.StatusOK)
	}))
	request := func(method, path, origin, supplied string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if supplied != "" {
			r.Header.Set("X-AniShelf-Key", supplied)
		}
		w := httptest.NewRecorder()
		gateway.ServeHTTP(w, r)
		return w
	}
	if got := request("GET", "/api/bilibili", "capacitor://localhost", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("missing key: %d", got.Code)
	}
	if got := request("GET", "/api/bilibili", "capacitor://localhost", "bad"); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", got.Code)
	}
	if got := request("OPTIONS", "/api/bilibili", "capacitor://localhost", ""); got.Code != http.StatusNoContent || got.Header().Get("Access-Control-Allow-Origin") != "capacitor://localhost" {
		t.Fatalf("native preflight: %d, %q", got.Code, got.Header().Get("Access-Control-Allow-Origin"))
	}
	if got := request("POST", "/api/bilibili", "capacitor://localhost", key); got.Code != http.StatusOK || got.Header().Get("Access-Control-Allow-Origin") != "capacitor://localhost" {
		t.Fatalf("authorized native call: %d", got.Code)
	}
	if got := request("GET", "/api/bilibili", "https://localhost", key); got.Code != http.StatusOK || got.Header().Get("Access-Control-Allow-Origin") != "https://localhost" {
		t.Fatalf("authorized Android call: %d", got.Code)
	}
	if got := request("GET", "/api/playback/media/short-lease", "capacitor://localhost", ""); got.Code != http.StatusOK {
		t.Fatalf("short-lived media lease: %d", got.Code)
	}
	if got := request("GET", "/api/bilibili", "https://unrelated.example", ""); got.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("untrusted origin received CORS access")
	}
}
