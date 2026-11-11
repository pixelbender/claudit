package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/protocol"
)

// A whole percent from 1 to 99 sets the warning; anything else leaves the 80% of before.
func TestModelContextWarnParse(t *testing.T) {
	for s, want := range map[string]int{"30": 30, " 45\n": 45, "1": 1, "99": 99, "": 0, "0": 0, "100": 0, "-5": 0, "30%": 0, "0.3": 0, "abc": 0} {
		if got := ContextWarn(s); got != want {
			t.Errorf("ContextWarn(%q) = %d, want %d", s, got, want)
		}
	}
}

// modelContextWarned counts the chat's lines that say the context got to the warning.
func modelContextWarned(m *Model) int {
	n := 0
	for _, e := range m.Chat {
		if e.Kind == CommandEntry && strings.HasPrefix(e.Text, "Context at ") {
			n++
		}
	}
	return n
}

// The chat says once that main's context got to CLAUDIT_CONTEXT_WARN, and again only after it went
// below: after /clear or a compaction.
func TestModelContextWarn(t *testing.T) {
	m, _ := modelNew(t, false)
	m.ContextWarn = 30
	modelEvents(m, protocol.Result{ContextWindows: map[string]int{"haiku": 200000}}, protocol.Context{Model: "haiku", Tokens: 50000})
	if n := modelContextWarned(m); n != 0 || m.Status.ContextWarn != 30 {
		t.Fatalf("at 25%%: %d lines, warn %d", n, m.Status.ContextWarn)
	}
	modelEvents(m, protocol.Context{Model: "haiku", Tokens: 62000})
	if n := modelContextWarned(m); n != 1 {
		t.Fatalf("at 31%%: %d lines", n)
	}
	if got := m.Chat[len(m.Chat)-1].Text; got != "Context at 31% · past CLAUDIT_CONTEXT_WARN=30" {
		t.Errorf("line %q", got)
	}
	modelEvents(m, protocol.Context{Model: "haiku", Tokens: 90000}, protocol.Context{Model: "fallback", Tokens: 95000}) // a window not known yet
	modelEvents(m, protocol.Result{ContextWindows: map[string]int{"fallback": 200000}})
	if n := modelContextWarned(m); n != 1 {
		t.Fatalf("said again above the warning: %d lines", n)
	}
	modelEvents(m, protocol.Context{Model: "haiku", Tokens: 20000}) // compacted
	modelEvents(m, protocol.Context{Model: "haiku", Tokens: 70000})
	if n := modelContextWarned(m); n != 2 {
		t.Fatalf("after a compaction: %d lines", n)
	}
	modelEvents(m, protocol.Reset{}) // the chat starts anew
	modelEvents(m, protocol.Context{Model: "haiku", Tokens: 64000})
	if n := modelContextWarned(m); n != 1 {
		t.Fatalf("after /clear: %d lines", n)
	}
}

// Without CLAUDIT_CONTEXT_WARN, or in a replay, the chat says nothing of the context.
func TestModelContextWarnSilent(t *testing.T) {
	for _, replay := range []bool{false, true} {
		m, _ := modelNew(t, replay)
		if replay {
			m.ContextWarn = 30
		}
		modelEvents(m, protocol.Result{ContextWindows: map[string]int{"haiku": 200000}}, protocol.Context{Model: "haiku", Tokens: 190000})
		if n := modelContextWarned(m); n != 0 {
			t.Errorf("replay %v: %d lines", replay, n)
		}
		if want := m.ContextWarn; m.Status.ContextWarn != want {
			t.Errorf("replay %v: status warns from %d, want %d", replay, m.Status.ContextWarn, want)
		}
	}
}

// While the terminal is away, the warning rings it too, after what the turn that took the context
// there says.
func TestModelContextWarnNotice(t *testing.T) {
	m, _ := modelNew(t, false)
	m.ContextWarn = 30
	modelEvents(m, protocol.Context{Model: "haiku", Tokens: 70000})
	m.Update(tea.BlurMsg{})
	m.Status.Busy, m.Status.Elapsed = true, "12s"
	_, cmd := m.Update(AgentMsg{Events: []protocol.Event{protocol.Result{ContextWindows: map[string]int{"haiku": 200000}}}})
	if s := modelRaw(cmd); s != "\x1b]9;claude is done · 12s · context at 35%\x07" {
		t.Errorf("notice %q", s)
	}
	modelEvents(m, protocol.Context{Model: "haiku", Tokens: 20000})
	_, cmd = m.Update(AgentMsg{Events: []protocol.Event{protocol.Context{Model: "haiku", Tokens: 80000}}})
	if s := modelRaw(cmd); s != "\x1b]9;context at 40%\x07" {
		t.Errorf("notice mid-turn %q", s)
	}
}

// Resuming a conversation starts the context anew, as /clear does: the warning is said again once
// the resumed conversation gets past it.
func TestModelContextWarnResume(t *testing.T) {
	m, _ := modelResumeNew(t)
	m.ContextWarn = 30
	modelEvents(m, protocol.Result{ContextWindows: map[string]int{"haiku": 200000}}, protocol.Context{Model: "haiku", Tokens: 70000})
	if n := modelContextWarned(m); n != 1 {
		t.Fatalf("at 35%%: %d lines", n)
	}
	modelResumeSubmit(m, "/resume")
	_, cmd := m.Update(inputKey("enter"))
	modelResumeRun(m, cmd)
	if n := modelContextWarned(m); n != 0 || m.Status.Context != 0 {
		t.Fatalf("after resuming: %d lines, context %v", n, m.Status.Context)
	}
	modelEvents(m, protocol.Context{Model: "haiku", Tokens: 70000})
	if n := modelContextWarned(m); n != 1 {
		t.Fatalf("after resuming and getting past the warning: %d lines", n)
	}
}
