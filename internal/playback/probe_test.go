package playback

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type probeTransport func(*http.Request) (*http.Response, error)

func (f probeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbeMediaChecksChildPlaylistAndSegment(t *testing.T) {
	root, _ := url.Parse("https://cdn.example/show/index.m3u8")
	var visited []string
	lease := mediaLease{root: root, prefix: "/show/", httpClient: &http.Client{Transport: probeTransport(func(r *http.Request) (*http.Response, error) {
		visited = append(visited, r.URL.Path)
		body := ""
		status := http.StatusOK
		switch r.URL.Path {
		case "/show/index.m3u8":
			body = "#EXTM3U\nsub/index.m3u8\n"
		case "/show/sub/index.m3u8":
			body = "#EXTM3U\n#EXTINF:4,\n000.ts\n"
		case "/show/sub/000.ts":
			body = "video segment"
		default:
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}}
	s := &Service{probeGates: newHostGates(0)}
	kind, err := s.probeMedia(context.Background(), lease)
	if err != nil || kind != "hls" || len(visited) != 3 || visited[2] != "/show/sub/000.ts" {
		t.Fatalf("did not verify the first segment: kind=%q visits=%v err=%v", kind, visited, err)
	}
}

func TestProbeMediaRejectsBrokenChildPlaylist(t *testing.T) {
	root, _ := url.Parse("https://cdn.example/show/index.m3u8")
	lease := mediaLease{root: root, prefix: "/show/", httpClient: &http.Client{Transport: probeTransport(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, "#EXTM3U\nsub/index.m3u8\n"
		if r.URL.Path != root.Path {
			status, body = http.StatusNotFound, ""
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}}
	s := &Service{probeGates: newHostGates(0)}
	if _, err := s.probeMedia(context.Background(), lease); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("broken nested playlist was accepted: %v", err)
	}
}

func TestProbeMediaRejectsBrokenFirstSegment(t *testing.T) {
	root, _ := url.Parse("https://cdn.example/show/index.m3u8")
	lease := mediaLease{root: root, prefix: "/show/", httpClient: &http.Client{Transport: probeTransport(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, "#EXTM3U\n#EXTINF:4,\n000.ts\n"
		if r.URL.Path != root.Path {
			status, body = http.StatusNotFound, ""
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}}
	s := &Service{probeGates: newHostGates(0)}
	if _, err := s.probeMedia(context.Background(), lease); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("broken first segment was accepted: %v", err)
	}
}
