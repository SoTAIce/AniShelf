package playback

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// upstreamGate spaces metadata/player requests across all callers. A source
// rejection pauses the whole gate instead of trying other lines immediately.
type upstreamGate struct {
	mu        sync.Mutex
	gap       time.Duration
	next      time.Time
	blocked   time.Time
	forbidden int
	onBlock   func(time.Time)
}

type hostGates struct {
	mu    sync.Mutex
	gates map[string]*upstreamGate
	gap   time.Duration
}

func newHostGates(gap time.Duration) *hostGates {
	return &hostGates{gates: map[string]*upstreamGate{}, gap: gap}
}
func (h *hostGates) forHost(host string) *upstreamGate {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gates[host] == nil {
		h.gates[host] = newUpstreamGate(h.gap)
	}
	return h.gates[host]
}

func newUpstreamGate(gap time.Duration) *upstreamGate { return &upstreamGate{gap: gap} }

func (g *upstreamGate) wait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	now := time.Now()
	if now.Before(g.blocked) {
		remaining := time.Until(g.blocked).Round(time.Second)
		g.mu.Unlock()
		return fmt.Errorf("片源暂时限制访问，请约 %s 后重试", remaining)
	}
	start := now
	if g.next.After(start) {
		start = g.next
	}
	g.next = start.Add(g.gap)
	g.mu.Unlock()
	if delay := time.Until(start); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Another request may have triggered a cooldown while this one waited.
	if remaining := g.cooldownRemaining(); remaining > 0 {
		return fmt.Errorf("片源暂时限制访问，请约 %s 后重试", remaining.Round(time.Second))
	}
	return nil
}

func (g *upstreamGate) cooldownRemaining() time.Duration {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if remaining := time.Until(g.blocked); remaining > 0 {
		return remaining
	}
	return 0
}

func (g *upstreamGate) observe(status int, header http.Header) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if status >= 200 && status < 300 {
		g.forbidden = 0
		g.mu.Unlock()
		return
	}
	if status == http.StatusForbidden {
		g.forbidden++
		if g.forbidden < 2 && header.Get("Retry-After") == "" {
			g.mu.Unlock()
			return
		}
	}
	if status != http.StatusTooManyRequests && status != http.StatusForbidden && status != http.StatusServiceUnavailable {
		g.mu.Unlock()
		return
	}
	g.mu.Unlock()
	delay := 10 * time.Minute
	if raw := header.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		} else if when, err := http.ParseTime(raw); err == nil {
			delay = time.Until(when)
		}
	}
	if delay < time.Minute {
		delay = time.Minute
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	g.mu.Lock()
	until := time.Now().Add(delay)
	if until.After(g.blocked) {
		g.blocked = until
	}
	callback := g.onBlock
	current := g.blocked
	g.mu.Unlock()
	if callback != nil {
		callback(current)
	}
}
