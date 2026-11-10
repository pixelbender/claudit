package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/protocol"
)

// modelCacheAt syncs the model as of modelT0+d and returns what the status says of the cache.
func modelCacheAt(m *Model, d time.Duration) (left, ttl time.Duration) {
	m.Now = modelT0.Add(d)
	m.sync()
	return m.Status.CacheLeft, m.Status.CacheTTL
}

// The prompt cache lives its TTL from when main's last call reached the API; a /clear forgets it.
func TestModelCache(t *testing.T) {
	m, _ := modelNew(t, false)
	if _, ttl := modelCacheAt(m, 0); ttl != 0 {
		t.Fatalf("shown before any call: %s", ttl)
	}
	modelEvents(m, protocol.Context{Model: "m", Tokens: 10}) // no cache read or written: none to time
	if _, ttl := modelCacheAt(m, 0); ttl != 0 {
		t.Fatalf("shown without a cache: %s", ttl)
	}
	modelEvents(m, protocol.Context{Model: "m", Tokens: 10, TTFT: 2 * time.Second, Cached: true}) // no cache writes: the default
	if left, ttl := modelCacheAt(m, time.Minute); ttl != 5*time.Minute || left != 5*time.Minute-62*time.Second {
		t.Fatalf("default: %s of %s", left, ttl)
	}
	modelEvents(m, protocol.Context{Model: "m", Tokens: 10, CacheTTL: time.Hour, Cached: true})
	modelEvents(m, protocol.Context{Model: "m", Tokens: 10, Cached: true}) // a call that wrote nothing keeps the TTL told
	if left, ttl := modelCacheAt(m, 10*time.Minute); ttl != time.Hour || left != 50*time.Minute {
		t.Fatalf("an hour: %s of %s", left, ttl)
	}
	if left, _ := modelCacheAt(m, 2*time.Hour); left != 0 {
		t.Fatalf("expired: %s", left)
	}
	modelEvents(m, protocol.Reset{})
	if _, ttl := modelCacheAt(m, 2*time.Hour); ttl != 0 {
		t.Fatalf("after /clear: %s", ttl)
	}
}

// Another model, or another conversation resumed, has no cache of what main says: no timer until its first call.
func TestModelCacheOtherModelOrConversation(t *testing.T) {
	m, _ := modelResumeNew(t)
	modelEvents(m, protocol.Context{Model: "m", Tokens: 10, Cached: true})
	modelEvents(m, protocol.ModelChanged{Model: "m"}) // the same model: its cache lives on
	if _, ttl := modelCacheAt(m, time.Minute); ttl == 0 {
		t.Fatalf("the same model forgets the cache")
	}
	modelEvents(m, protocol.ModelChanged{Model: "n"})
	if _, ttl := modelCacheAt(m, time.Minute); ttl != 0 {
		t.Fatalf("after /model: %s", ttl)
	}
	modelEvents(m, protocol.Context{Model: "n", Tokens: 10, Cached: true})
	if _, ttl := modelCacheAt(m, time.Minute); ttl == 0 {
		t.Fatalf("the new model's call starts no timer")
	}
	modelResumeSubmit(m, "/resume")
	_, cmd := m.Update(inputKey("enter"))
	modelResumeRun(m, cmd)
	if !modelResumeSaid(m, "Resumed: ") {
		t.Fatalf("not resumed: %+v", m.Chat)
	}
	if _, ttl := modelCacheAt(m, time.Minute); ttl != 0 {
		t.Fatalf("after /resume: %s", ttl)
	}
}

func TestDrawCache(t *testing.T) {
	for _, c := range []struct {
		left, ttl time.Duration
		want      string
	}{
		{4*time.Minute + 59*time.Second, 5 * time.Minute, "cache 4m"},
		{2 * time.Minute, 5 * time.Minute, "cache 2m"},
		{105 * time.Second, 5 * time.Minute, "cache 1:45"},
		{5 * time.Second, 5 * time.Minute, "cache 0:05"},
		{0, 5 * time.Minute, "cache cold"},
		{50 * time.Minute, time.Hour, "cache 50m"},
	} {
		if got := ansi.Strip(drawCache(c.left, c.ttl)); got != c.want {
			t.Errorf("%s of %s: %q, want %q", c.left, c.ttl, got, c.want)
		}
	}
	for _, c := range []struct {
		left, ttl time.Duration
		want      int
	}{{5 * time.Minute, 5 * time.Minute, 0}, {time.Minute, 5 * time.Minute, 80}, {15 * time.Second, 5 * time.Minute, 95}, {6 * time.Minute, time.Hour, 90}} {
		if got := cacheUsed(c.left, c.ttl); got != c.want {
			t.Errorf("used %s of %s: %d%%, want %d%%", c.left, c.ttl, got, c.want)
		}
	}
	line := ansi.Strip(renderStatus(Status{Model: "m", State: "idle", CacheLeft: 3 * time.Minute, CacheTTL: 5 * time.Minute}, nil, 120))
	if !strings.Contains(line, "cache 3m") {
		t.Errorf("status: %q", line)
	}
	if line := ansi.Strip(renderStatus(Status{Model: "m", State: "idle"}, nil, 120)); strings.Contains(line, "cache") {
		t.Errorf("shown before any call: %q", line)
	}
}

// Squeezed, the status line gives up the model, then the limits; the context and the cache last.
func TestStatusKeepsContextAndCache(t *testing.T) {
	st := Status{Model: "claude-opus-5-5", Effort: "high", Mode: "auto", State: "idle", Usage: "5h 8% 4h34m · 7d 20% 5d17h",
		Context: 0.05, CacheLeft: 3 * time.Minute, CacheTTL: 5 * time.Minute}
	for w := 70; w <= 200; w++ {
		line := ansi.Strip(renderStatus(st, nil, w))
		if strings.Contains(line, "5h 8%") && !strings.Contains(line, "cache 3m") {
			t.Fatalf("%d: the limits stay, the cache goes: %q", w, line)
		}
		if strings.Contains(line, "claude-opus") && !strings.Contains(line, "5h 8%") {
			t.Fatalf("%d: the model stays, the limits go: %q", w, line)
		}
	}
	if line := ansi.Strip(renderStatus(st, nil, 90)); !strings.Contains(line, "ctx 5% · cache 3m") || strings.Contains(line, "5h") {
		t.Errorf("90 columns: %q", line)
	}
}
