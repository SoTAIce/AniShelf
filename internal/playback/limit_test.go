package playback

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestUpstreamGateSpacesRequestsAndHonorsCooldown(t *testing.T) {
	g := newUpstreamGate(25 * time.Millisecond)
	if err := g.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := g.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("upstream calls not spaced: %s", elapsed)
	}
	g.observe(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"120"}})
	if err := g.wait(context.Background()); err == nil {
		t.Fatal("ignored source cooldown")
	}
}

func TestHostCooldownDoesNotBlockOtherPlaybackLines(t *testing.T) {
	hosts := newHostGates(0)
	hosts.forHost("blocked.example").observe(http.StatusForbidden, http.Header{})
	if err := hosts.forHost("blocked.example").wait(context.Background()); err != nil {
		t.Fatalf("a single forbidden line blocked the entire host: %v", err)
	}
	hosts.forHost("blocked.example").observe(http.StatusForbidden, http.Header{})
	if err := hosts.forHost("blocked.example").wait(context.Background()); err == nil {
		t.Fatal("blocked host was retried")
	}
	if err := hosts.forHost("other.example").wait(context.Background()); err != nil {
		t.Fatalf("unrelated line was blocked: %v", err)
	}
}

func TestSourceCooldownSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "age-playback.json")
	first := Open(path, nil)
	first.upstream.observe(http.StatusTooManyRequests, http.Header{})
	restarted := Open(path, nil)
	if restarted.upstream.cooldownRemaining() < 9*time.Minute {
		t.Fatal("source cooldown was lost across restart")
	}
}
