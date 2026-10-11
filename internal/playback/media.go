package playback

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

type mediaLease struct {
	httpClient *http.Client
	root       *url.URL
	prefix     string
	crossHost  string
	expires    time.Time
}

func publicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}
func newLease(raw string) (mediaLease, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return mediaLease{}, fmt.Errorf("invalid media URL")
	}
	l := mediaLease{root: u, prefix: path.Dir(path.Clean(u.Path)) + "/", expires: time.Now().Add(2 * time.Hour)}
	if u.Host == "bfikuncdn.com" {
		l.crossHost = "kkzycdn.com:65"
	}
	l.httpClient = l.buildClient()
	return l, nil
}
func (l mediaLease) permits(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && (u.Host == l.root.Host || l.crossHost != "" && u.Host == l.crossHost) && u.User == nil && (u.Path == l.root.Path || strings.HasPrefix(path.Clean(u.Path), l.prefix))
}
func (l mediaLease) client() *http.Client {
	if l.httpClient != nil {
		return l.httpClient
	}
	return l.buildClient()
}
func (l mediaLease) buildClient() *http.Client {
	return &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || !l.permits(r.URL) {
			return fmt.Errorf("redirect outside media scope")
		}
		return nil
	}, Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(addr)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, fmt.Errorf("non-public media host")
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}}}
}
func (s *Service) register(l mediaLease) (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases == nil {
		s.leases = map[string]mediaLease{}
	}
	for k, v := range s.leases {
		if time.Now().After(v.expires) {
			v.client().CloseIdleConnections()
			delete(s.leases, k)
		}
	}
	if len(s.leases) >= 256 {
		return "", fmt.Errorf("播放会话过多，请稍后重试")
	}
	s.leases[token] = l
	return token, nil
}
func (l mediaLease) proxyURL(token string, u *url.URL) string {
	return "/api/playback/media/" + token + "?url=" + url.QueryEscape(u.String())
}

var uriRE = regexp.MustCompile(`URI="([^"]+)"`)

func (l mediaLease) playlist(body, token string, base *url.URL) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var lines []string
	rewrite := func(raw string) (string, error) {
		u, e := url.Parse(raw)
		if e != nil {
			return "", e
		}
		u = base.ResolveReference(u)
		if !l.permits(u) {
			return "", fmt.Errorf("清单引用了范围外的视频地址")
		}
		return l.proxyURL(token, u), nil
	}
	skippingForeign := false
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if trim == "#EXT-X-DISCONTINUITY" && skippingForeign {
			skippingForeign = false
		}
		if trim != "" && !strings.HasPrefix(trim, "#") {
			r, e := rewrite(trim)
			if e != nil {
				// Some sources splice short segments from a different title
				// between discontinuities. Keep the proxy scoped to this episode.
				u, parseErr := url.Parse(trim)
				if parseErr != nil {
					return "", e
				}
				u = base.ResolveReference(u)
				if (u.Host != l.root.Host && u.Host != l.crossHost) || !strings.HasSuffix(strings.ToLower(u.Path), ".ts") {
					return "", e
				}
				markedBlock := len(lines) >= 2 && strings.HasPrefix(lines[len(lines)-1], "#EXTINF:") && lines[len(lines)-2] == "#EXT-X-DISCONTINUITY"
				markedBlock = markedBlock || len(lines) >= 3 && strings.HasPrefix(lines[len(lines)-1], "#EXTINF:") && lines[len(lines)-2] == "#EXT-X-KEY:METHOD=NONE" && lines[len(lines)-3] == "#EXT-X-DISCONTINUITY"
				if !skippingForeign && !markedBlock {
					return "", e
				}
				skippingForeign = true
				for len(lines) > 0 {
					last := lines[len(lines)-1]
					if !strings.HasPrefix(last, "#EXTINF:") && last != "#EXT-X-DISCONTINUITY" && last != "#EXT-X-KEY:METHOD=NONE" {
						break
					}
					lines = lines[:len(lines)-1]
				}
				continue
			}
			skippingForeign = false
			line = r
		} else if strings.Contains(line, `URI="`) {
			var issue error
			line = uriRE.ReplaceAllStringFunc(line, func(m string) string {
				raw := uriRE.FindStringSubmatch(m)[1]
				r, e := rewrite(raw)
				if e != nil {
					issue = e
				}
				return `URI="` + r + `"`
			})
			if issue != nil {
				return "", issue
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n", scanner.Err()
}
func (s *Service) ServeMedia(w http.ResponseWriter, r *http.Request) {
	if s.mediaSlots != nil {
		select {
		case s.mediaSlots <- struct{}{}:
			defer func() { <-s.mediaSlots }()
		case <-r.Context().Done():
			return
		}
	}
	token := r.PathValue("token")
	s.mu.Lock()
	l, ok := s.leases[token]
	s.mu.Unlock()
	if !ok || time.Now().After(l.expires) {
		http.Error(w, "播放会话已过期，请重新选择该集", 410)
		return
	}
	u, e := url.Parse(r.URL.Query().Get("url"))
	if e != nil || !l.permits(u) {
		http.Error(w, "invalid media scope", 400)
		return
	}
	mediaGate := s.mediaGates.forHost(u.Hostname())
	if e := mediaGate.wait(r.Context()); e != nil {
		http.Error(w, e.Error(), http.StatusTooManyRequests)
		return
	}
	req, e := http.NewRequestWithContext(r.Context(), "GET", u.String(), nil)
	if e != nil {
		http.Error(w, "invalid media URL", 400)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if v := r.Header.Get("Range"); v != "" {
		req.Header.Set("Range", v)
	}
	if v := r.Header.Get("If-Range"); v != "" {
		req.Header.Set("If-Range", v)
	}
	client := l.client()

	resp, e := client.Do(req)
	if e != nil {
		http.Error(w, "片源连接失败", 502)
		return
	}
	defer resp.Body.Close()
	mediaGate.observe(resp.StatusCode, resp.Header)
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		http.Error(w, fmt.Sprintf("片源返回 HTTP %d", resp.StatusCode), 502)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	reader := bufio.NewReader(resp.Body)
	prefix, _ := reader.Peek(7)
	if string(prefix) == "#EXTM3U" {
		body, e := io.ReadAll(io.LimitReader(reader, (2<<20)+1))
		if e != nil || len(body) > 2<<20 {
			http.Error(w, "清单过大或不完整", 502)
			return
		}
		rewritten, e := l.playlist(string(body), token, resp.Request.URL)
		if e != nil {
			http.Error(w, e.Error(), 502)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		io.WriteString(w, rewritten)
		return
	}
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if strings.HasSuffix(u.Path, ".ts") {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, reader)
}
