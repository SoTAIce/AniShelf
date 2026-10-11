package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdaterCoalescesAndPreservesLastGoodData(t *testing.T) {
	s := &Store{Path: filepath.Join(t.TempDir(), "catalog.json"), data: Dataset{FetchedAt: "2026-09-28T00:00:00Z", Items: []Anime{{ID: 1, Title: "已有作品"}}}}
	u := NewUpdater(s, 30*time.Minute)
	entered, release := make(chan struct{}), make(chan struct{})
	u.refresh = func(ctx context.Context, d Dataset, report func(int, int, int)) (Dataset, []string) {
		close(entered)
		<-release
		report(1, 1, 0)
		return d, []string{"上游暂不可用"}
	}
	if !u.Start(context.Background()) {
		t.Fatal("first refresh rejected")
	}
	<-entered
	if u.Start(context.Background()) {
		t.Fatal("overlapping refresh accepted")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for u.Status().Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := u.Status()
	if status.Running || len(status.Issues) != 1 || status.LastSuccess != "2026-09-28T00:00:00Z" {
		t.Fatalf("bad completion status: %#v", status)
	}
	if s.Snapshot().Items[0].Title != "已有作品" {
		t.Fatal("last good catalogue lost")
	}
	if u.Start(context.Background()) {
		t.Fatal("cooldown bypassed")
	}
}
