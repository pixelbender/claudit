package ideview

import (
	"maps"
	"slices"

	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ide"
)

// Scroll is how many screen rows the user scrolled each pane back (up) from where it follows its
// agent. It is keyed by pane: files, agents, a file in the editor, an agent's terminal or browser.
type Scroll map[string]int

const (
	filesPane     = "files"
	agentsPaneKey = "agents"
)

func fileKey(path string) string  { return "file:" + path }
func termKey(agent string) string { return "term:" + agent }
func webKey(agent string) string  { return "web:" + agent }

// paneKey is the key of column c's pane; "" for an editor without a file.
func paneKey(st *ide.State, c column) string {
	switch c.pane {
	case ide.Terminal:
		return termKey(c.agent.Key)
	case ide.Browser:
		return webKey(c.agent.Key)
	}
	if tab := shownTab(st, c.agent); tab != nil {
		return fileKey(tab.Path)
	}
	return ""
}

// scrolled is the first row of a window that follows at top, scrolled back d rows, within [0, last].
func scrolled(top, d, last int) int { return min(max(top-d, 0), last) }

// Wheel scrolls the pane at cell (x, y) of Render(st, o, width, height) n rows back (forward when
// n < 0), as far as there is to see. It returns the pane's key, "" for none, and its new scroll.
// Scrolled back to where it follows its agent, a pane stops there: 0, it follows again.
func Wheel(st *ide.State, o Options, width, height, x, y, n int) (string, int) {
	if y < 0 || y >= height || x < 0 || x >= width {
		return "", 0
	}
	_, ih := inner(0, height)
	c, t, ok := columnAt(st, o, width, height, x, y)
	iw := 0
	if ok {
		iw, ih = inner(t.Dx(), t.Dy())
	}
	switch {
	case !ok && o.Panel == Agents:
		base := o
		base.Scroll = nil
		tree, top := agentsView(st, camera(st), base, ih)
		return agentsPaneKey, step(o.Scroll[agentsPaneKey], n, top-max(0, len(tree)-ih), top)
	case !ok && o.Panel == Files:
		if v := outlined(st, o, width, height); v != nil {
			rows, _, follow, _ := v.outline(o, ih)
			key := outlineKey(v.path)
			return key, step(o.Scroll[key], n, follow-max(0, len(rows)-ih), follow)
		}
		top := scrollTop(treeSel(st, camera(st)), len(st.Tree), ih)
		return filesPane, step(o.Scroll[filesPane], n, top-max(0, len(st.Tree)-ih), top)
	case !ok:
		return "", 0
	case c.pane == ide.Terminal:
		key := termKey(c.agent.Key)
		return key, step(o.Scroll[key], n, 0, max(0, o.terminalLayout(&c.agent.Term, textWidth(iw)).total-ih))
	case c.pane == ide.Browser: // it follows the top of the page
		key := webKey(c.agent.Key)
		return key, step(o.Scroll[key], n, -max(0, len(pageRows(&c.agent.Page, textWidth(iw), -1))-pageHeight(ih)), 0)
	}
	tab := shownTab(st, c.agent)
	if tab == nil {
		return "", 0
	}
	if v := structured(st, c.agent, o, iw, ih); v != nil { // it follows the top of the spec
		key := specKey(tab.Path)
		return key, step(o.Scroll[key], n, -v.last, 0)
	}
	key := fileKey(tab.Path)
	lines, cur, tw := editorText(tab, c.agent, iw)
	top, last := editorRange(lines, cur, tw, ih, o.Wrap)
	return key, step(o.Scroll[key], n, min(0, top-last), max(0, top))
}

// step is scroll d moved by n within [lo, hi], the scrolls that show the end and the start. Moving
// across 0, where the pane follows its agent, it stops there.
func step(d, n, lo, hi int) int {
	d = min(max(d, lo), hi)
	next := min(max(d+n, lo), hi)
	if d > 0 && next < 0 || d < 0 && next > 0 {
		return 0
	}
	return next
}

// Prune forgets the scroll of the panes Render(st, o, width, …) does not draw, so that one coming
// back follows its agent. A spec keeps its scroll, and its layout, while its file is on screen, as
// written or not.
func Prune(st *ide.State, o Options, width, height int) {
	v := outlined(st, o, width, height)
	shown := map[string]bool{filesPane: o.Panel == Files && v == nil, agentsPaneKey: o.Panel == Agents}
	if v != nil {
		shown[outlineKey(v.path)] = true
	}
	terms, files := map[*ide.Term]bool{}, map[string]bool{}
	for _, c := range columns(st, camera(st), o, width-leftWidth(width), height) {
		shown[paneKey(st, c)] = true
		if tab := shownTab(st, c.agent); c.pane == ide.Editor && tab != nil {
			shown[specKey(tab.Path)], files[tab.Path] = true, true
		}
		if c.pane == ide.Terminal {
			terms[&c.agent.Term] = true
		}
	}
	maps.DeleteFunc(o.Scroll, func(k string, _ int) bool { return !shown[k] })
	maps.DeleteFunc(o.Terms, func(t *ide.Term, _ termLayout) bool { return !terms[t] })
	maps.DeleteFunc(o.SpecRows, func(path string, _ *specLayout) bool { return !files[path] })
}

// TermScroll is the scroll of agent's terminal in Render(st, o, width, height) that brings the
// first line tool call call typed to the top of the pane: the pane's key and how far back; "" when
// the pane is not on screen or holds no line of the call.
func TermScroll(st *ide.State, o Options, width, height int, agent, call string) (string, int) {
	ew := leftWidth(width)
	cols := columns(st, camera(st), o, width-ew, height)
	for i, r := range tiles(len(cols), width-ew, height) {
		if c := cols[i]; c.pane == ide.Terminal && c.agent.Key == agent {
			t := &c.agent.Term
			at := slices.IndexFunc(t.Lines, func(l ide.TermLine) bool { return l.Call == call })
			if at < 0 {
				return "", 0
			}
			iw, ih := inner(r.Dx(), r.Dy())
			tw := textWidth(iw)
			return termKey(agent), max(0, termRows(t, tw)-ih-lineRows(t.Lines[:at], tw))
		}
	}
	return "", 0
}

// termRows is how many rows terminal t takes iw cells wide, its live line included.
func termRows(t *ide.Term, iw int) int { return lineRows(t.Lines, iw) + 1 }

// lineRows is how many rows the scrollback lines take iw cells wide.
func lineRows(lines []ide.TermLine, iw int) int {
	pw, n := ansi.StringWidth(clip(termPrompt, iw, "")), 0
	for _, l := range lines {
		first := iw
		if l.Kind == ide.Command || l.Kind == ide.More {
			first -= pw
		}
		n += wrappedRows(l.Text, first, iw)
	}
	return n
}
