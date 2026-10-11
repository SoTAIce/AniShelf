package catalog

import (
	"context"
	"sync"
	"time"
)

type UpdateStatus struct {
	Running         bool     `json:"running"`
	Done            int      `json:"done"`
	Total           int      `json:"total"`
	Updated         int      `json:"updated"`
	StartedAt       string   `json:"startedAt"`
	FinishedAt      string   `json:"finishedAt"`
	LastSuccess     string   `json:"lastSuccess"`
	Issues          []string `json:"issues"`
	IntervalSeconds int      `json:"intervalSeconds"`
}
type Updater struct {
	mu        sync.RWMutex
	status    UpdateStatus
	store     *Store
	interval  time.Duration
	refresh   func(context.Context, Dataset, func(int, int, int)) (Dataset, []string)
	lastStart time.Time
}

func NewUpdater(s *Store, interval time.Duration) *Updater {
	return &Updater{store: s, interval: interval, status: UpdateStatus{LastSuccess: s.Snapshot().FetchedAt, Issues: []string{}, IntervalSeconds: int(interval.Seconds())}, refresh: RefreshProgress}
}
func (u *Updater) Status() UpdateStatus {
	u.mu.RLock()
	defer u.mu.RUnlock()
	s := u.status
	s.Issues = append([]string{}, s.Issues...)
	return s
}

// Start coalesces concurrent requests and enforces a one-minute crawl cooldown.
func (u *Updater) Start(ctx context.Context) bool {
	u.mu.Lock()
	if u.status.Running || time.Since(u.lastStart) < time.Minute {
		u.mu.Unlock()
		return false
	}
	u.lastStart = time.Now()
	u.status.Running = true
	u.status.Done = 0
	u.status.Total = 0
	u.status.Updated = 0
	u.status.StartedAt = time.Now().UTC().Format(time.RFC3339)
	u.status.FinishedAt = ""
	u.status.Issues = []string{}
	u.mu.Unlock()
	go func() {
		job, cancel := context.WithTimeout(ctx, 12*time.Minute)
		defer cancel()
		d, issues := u.refresh(job, u.store.Snapshot(), func(done, total, updated int) {
			u.mu.Lock()
			u.status.Done = done
			u.status.Total = total
			u.status.Updated = updated
			u.mu.Unlock()
		})
		if err := u.store.Replace(d); err != nil {
			issues = append(issues, "新数据保存失败，保留已有片库。")
		}
		u.mu.Lock()
		u.status.Running = false
		u.status.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		u.status.Issues = issues
		u.status.LastSuccess = u.store.Snapshot().FetchedAt
		u.mu.Unlock()
	}()
	return true
}
func (u *Updater) Run(ctx context.Context) {
	if u.interval <= 0 {
		return
	}
	last, _ := time.Parse(time.RFC3339, u.store.Snapshot().FetchedAt)
	if time.Since(last) >= u.interval {
		u.Start(ctx)
	}
	ticker := time.NewTicker(u.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.Start(ctx)
		}
	}
}
