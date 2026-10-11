package playback

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// probeMedia follows the same scoped manifest rewriting as the player and
// checks the first segment. A reachable master playlist alone is not enough
// to call a line playable.
func (s *Service) probeMedia(ctx context.Context, lease mediaLease) (string, error) {
	u := lease.root
	kind := "mp4"
	for depth := 0; depth < 4; depth++ {
		if !lease.permits(u) {
			return "", fmt.Errorf("视频地址超出当前选集范围")
		}
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		if !strings.HasSuffix(strings.ToLower(u.Path), ".m3u8") {
			req.Header.Set("Range", "bytes=0-2047")
		}
		gate := s.probeGates.forHost(u.Hostname())
		if err := gate.wait(ctx); err != nil {
			return "", err
		}
		resp, err := lease.client().Do(req)
		if err != nil {
			return "", fmt.Errorf("视频连接失败")
		}
		gate.observe(resp.StatusCode, resp.Header)
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			return "", fmt.Errorf("视频返回 HTTP %d", resp.StatusCode)
		}
		reader := bufio.NewReader(resp.Body)
		prefix, _ := reader.Peek(7)
		if string(prefix) != "#EXTM3U" {
			first, readErr := io.ReadAll(io.LimitReader(reader, 2048))
			resp.Body.Close()
			if readErr != nil || len(first) == 0 || strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(first))), "<html") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(first))), "<!doctype") {
				return "", fmt.Errorf("视频分片未返回有效内容")
			}
			if depth == 0 && !strings.HasPrefix(resp.Header.Get("Content-Type"), "video/") {
				return "", fmt.Errorf("返回内容不是视频")
			}
			return kind, nil
		}
		kind = "hls"
		body, readErr := io.ReadAll(io.LimitReader(reader, (2<<20)+1))
		resp.Body.Close()
		if readErr != nil || len(body) > 2<<20 {
			return "", fmt.Errorf("清单过大或不完整")
		}
		rewritten, err := lease.playlist(string(body), "probe", resp.Request.URL)
		if err != nil {
			return "", err
		}
		next, err := firstMediaReference(rewritten)
		if err != nil {
			return "", err
		}
		u = next
	}
	return "", fmt.Errorf("播放清单层级过深")
}

func firstMediaReference(playlist string) (*url.URL, error) {
	for _, line := range strings.Split(playlist, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		proxy, err := url.Parse(line)
		if err != nil {
			break
		}
		next, err := url.Parse(proxy.Query().Get("url"))
		if err == nil && next.IsAbs() {
			return next, nil
		}
		break
	}
	return nil, fmt.Errorf("播放清单没有可用的视频分片")
}
