package ui

import (
	"fmt"
	"strconv"
	"strings"
)

// ContextWarnEnv names the percent of main's context from which claudit warns, for those who
// hand off or compact at a point of their own. It moves the colour of the status line from 80%
// and the chat line; the orange from 90% stays, as the mark of a context almost full.
const ContextWarnEnv = "CLAUDIT_CONTEXT_WARN"

// ContextWarn reads a value of ContextWarnEnv: a whole percent from 1 to 99, else 0 for none.
func ContextWarn(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 99 {
		return 0
	}
	return n
}

// warnContext says once in the chat, and to the terminal if it is away, that main's context got
// to Model.ContextWarn; it says so again only after the context went below it, after /clear or a
// compaction, or after resuming another conversation. A window not known yet changes nothing, so
// a resumed conversation already past the warning says so when its first turn ends, with the
// Result that tells the window. A recording's context is its own business:
// its replay only colours the status line.
func (m *Model) warnContext() {
	m.Status.ContextWarn = m.ContextWarn
	if m.ContextWarn == 0 || m.Status.Context == 0 && m.context.Tokens > 0 {
		return
	}
	pct := contextPercent(m.Status.Context)
	switch {
	case pct < m.ContextWarn:
		m.contextWarned = false
	case !m.contextWarned:
		m.contextWarned = true
		if m.Status.Replay {
			break
		}
		m.local(fmt.Sprintf("Context at %d%% · past %s=%d", pct, ContextWarnEnv, m.ContextWarn))
		notice := fmt.Sprintf("context at %d%%", pct)
		if m.notice != "" { // with the turn that took it there
			notice = m.notice + " · " + notice
		}
		m.notice = notice
	}
}
