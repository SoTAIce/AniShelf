package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Entry struct {
	SeasonID int    `json:"season_id"`
	Title    string `json:"title"`
	Status   int    `json:"follow_status"`
	Cover    string `json:"cover"`
	AnimeID  int    `json:"animeId"`
}
type Client struct {
	HTTP    *http.Client
	BaseURL string
	Cookie  string
}

func NewClient(cookie string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}, BaseURL: "https://api.bilibili.com", Cookie: cookie}
}
func (c *Client) Fetch(ctx context.Context, uid string) ([]Entry, error) {
	entries := []Entry{}
	seen := map[int]bool{}
	for page := 1; page <= 100; page++ {
		address := fmt.Sprintf("%s/x/space/bangumi/follow/list?vmid=%s&type=1&follow_status=0&pn=%d&ps=30", c.BaseURL, url.QueryEscape(uid), page)
		req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 AnimeShelf/0.3")
		req.Header.Set("Referer", "https://space.bilibili.com/"+uid+"/bangumi")
		if c.Cookie != "" {
			req.Header.Set("Cookie", "SESSDATA="+c.Cookie)
		}
		res, err := c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("B站连接失败，请稍后重试")
		}
		var body struct {
			Code int `json:"code"`
			Data *struct {
				List  []Entry `json:"list"`
				Total *int    `json:"total"`
			} `json:"data"`
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&body)
		res.Body.Close()
		if res.StatusCode != 200 || err != nil {
			return nil, fmt.Errorf("B站响应异常，保留上次追番记录")
		}
		if body.Code != 0 || body.Data == nil {
			return nil, fmt.Errorf("B站拒绝读取（代码 %d）：追番可能未公开、登录失效或触发访问限制；保留原记录", body.Code)
		}
		added := 0
		for _, e := range body.Data.List {
			if e.SeasonID <= 0 || strings.TrimSpace(e.Title) == "" {
				return nil, fmt.Errorf("追番条目不完整，保留原记录")
			}
			if !seen[e.SeasonID] {
				seen[e.SeasonID] = true
				e.AnimeID = 0
				entries = append(entries, e)
				added++
			}
		}
		if body.Data.Total == nil || *body.Data.Total < 0 {
			return nil, fmt.Errorf("追番分页信息不完整，保留原记录")
		}
		if len(entries) >= *body.Data.Total {
			return entries, nil
		}
		if len(body.Data.List) == 0 || added == 0 {
			return nil, fmt.Errorf("追番分页不完整，保留原记录")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("追番超过 3000 条，未保存不完整列表")
}
