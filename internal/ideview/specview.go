package ideview

import (
	"fmt"
	"image/color"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/theme"
)

// The editor draws an OpenSpec spec structured: a badge per section, a coloured bar beside each
// requirement, the requirements and their scenarios folded or open, the steps' keywords in a
// column. The left column outlines it in place of the files.

var (
	specCapStyle  = lipgloss.NewStyle().Foreground(theme.Accent).Bold(true)
	specNameStyle = lipgloss.NewStyle().Foreground(theme.Bright)
	specTextStyle = lipgloss.NewStyle().Foreground(theme.Bright)
	specNormStyle = lipgloss.NewStyle().Foreground(theme.Red).Bold(true) // SHALL, MUST
	specCodeStyle = lipgloss.NewStyle().Foreground(theme.Violet)
	specStepStyle = lipgloss.NewStyle().Foreground(theme.Yellow)

	specNormRe = regexp.MustCompile(`\b(SHALL NOT|SHALL|MUST NOT|MUST)\b`)
)

// Specs is how the user folded the OpenSpec specs shown structured: by file path, what is open
// (true) or folded, by key. What the user has not turned is as a spec starts: the first
// requirement of each section open, the others and every scenario folded.
type Specs map[string]map[string]bool

// SpecHit is what a click on a spec shown structured does: it opens or folds a requirement, a
// scenario, or every requirement of a section; from the outline, it opens a requirement and
// scrolls the editor so that the requirement is on top.
type SpecHit struct {
	Path  string   // the spec's file
	Folds []string // the keys of what it opens or folds
	Open  bool
	Key   string // the pane it scrolls; "" for none
	Back  int    // the pane's scroll
}

// Fold opens or folds what h is on.
func (s Specs) Fold(h SpecHit) {
	if s[h.Path] == nil {
		s[h.Path] = map[string]bool{}
	}
	for _, k := range h.Folds {
		s[h.Path][k] = h.Open
	}
}

// reqOpen reports whether requirement j of s is open in folds.
func reqOpen(folds map[string]bool, s *section, j int) bool {
	if open, ok := folds[s.reqs[j].key]; ok {
		return open
	}
	return j == 0
}

// specKey is the scroll key of a spec shown structured: a scroll of its own, for it follows the
// top of the spec while the file as written follows the caret.
func specKey(path string) string { return "spec:" + path }

// SpecCache keeps the specs shown structured as the last frame laid them out, by path: a frame
// draws, scrolls and clicks the same spec several times.
type SpecCache map[string]*specLayout

// specLayout is a spec laid out, and what from: its text, the width and what was open.
type specLayout struct {
	lines []string
	doc   *spec
	width int
	folds map[string]bool
	rows  []specRow
}

// laidOut is the spec of tab, of capability, a delta or not, laid out w cells wide with folds
// open: the last frame's, unless the text, the width or the folds changed.
func (o Options) laidOut(tab *ide.Tab, capability string, delta bool, folds map[string]bool, w int) *specLayout {
	l := o.SpecRows[tab.Path]
	if l == nil || l.doc.delta != delta || !slices.Equal(l.lines, tab.Buf.Lines) {
		l = &specLayout{doc: parseSpec(tab.Buf.Lines, delta), width: -1}
		l.doc.capability = capability
		if o.SpecRows != nil {
			l.lines = slices.Clone(tab.Buf.Lines)
			o.SpecRows[tab.Path] = l
		}
	}
	if l.width != w || !maps.Equal(l.folds, folds) {
		l.rows, l.width, l.folds = l.doc.layout(folds, w), w, maps.Clone(folds)
	}
	return l
}

// specView is an OpenSpec spec as an editor shows it structured: its rows, and the window on them.
type specView struct {
	path      string
	doc       *spec
	folds     map[string]bool
	width     int // of the rows
	rows      []specRow
	top, last int // the first row shown, and the last it can be
}

// structured is the file agent a's editor shows, iw×ih cells inside its frame, as an OpenSpec
// spec; nil when the editor shows it as written: another file, a spec with no requirement, specs
// as written (o.Raw), or an agent editing it.
func structured(st *ide.State, a *ide.Agent, o Options, iw, ih int) *specView {
	tab := shownTab(st, a)
	if tab == nil || o.Raw || tab.Hex {
		return nil
	}
	capability, delta, ok := specPath(tab.Path)
	if !ok || editing(st, tab, o) {
		return nil
	}
	l := o.laidOut(tab, capability, delta, o.Specs[tab.Path], textWidth(iw))
	if n, _ := l.doc.counts(); n == 0 {
		return nil
	}
	v := &specView{path: tab.Path, doc: l.doc, folds: o.Specs[tab.Path], width: l.width, rows: l.rows}
	v.last = max(0, len(v.rows)-ih)
	v.top = scrolled(0, o.Scroll[specKey(tab.Path)], v.last)
	return v
}

// editing reports whether an agent at work edits tab: types in it, selects what it replaces, or
// waits for the result of what it typed. The editor shows the file as written then, so that the
// edit shows. What an agent types in its terminal or its browser is no edit.
func editing(st *ide.State, tab *ide.Tab, o Options) bool {
	if tab.Buf.Modified {
		return true
	}
	for key, c := range tab.Buf.Carets {
		a := findAgent(st, key)
		if a != nil && (typing(a, o) && a.Focus == ide.Editor && a.File == tab.Path || c.Sel != nil && working(a, o)) {
			return true
		}
	}
	return false
}

// body is the rows of v in the window, ih of them, iw cells wide at most with a cell of air
// before each.
func (v *specView) body(iw, ih int) []string {
	out := make([]string, ih)
	for i := range out {
		if r := v.top + i; r < len(v.rows) {
			out[i] = " " + v.rows[r].draw(textWidth(iw))
		}
	}
	return out
}

// current is the requirement at the top of v: the one its first row is in, else the next one
// below; -1, -1 for none.
func (v *specView) current() (sec, req int) {
	for _, r := range v.rows[min(v.top, len(v.rows)):] {
		if r.req >= 0 {
			return r.sec, r.req
		}
	}
	return -1, -1
}

// rowKind is what a click on a row of a spec opens or folds.
type rowKind int

const (
	rowText     rowKind = iota
	rowSection          // every requirement of the section
	rowReq              // the requirement
	rowScenario         // the scenario
)

// specRow is a row of a spec shown structured. Only the rows on screen are drawn: a spec runs
// longer than the window.
type specRow struct {
	lead           string // styled: the bar, the fold, the indent
	spans          []span
	kind           rowKind
	sec, req, scen int // where in the spec it is; -1 for none
}

// draw is r styled, at most w cells wide.
func (r specRow) draw(w int) string {
	var b strings.Builder
	b.WriteString(r.lead)
	for _, s := range r.spans {
		b.WriteString(s.st.Render(s.text))
	}
	return truncate(b.String(), w)
}

// blank reports whether r is an empty row.
func (r specRow) blank() bool { return r.lead == "" && len(r.spans) == 0 }

// opColor is the colour of a section of a delta, by what it does to its requirements; dim for a
// section of a main spec.
func opColor(op string) color.Color {
	switch op {
	case "ADDED":
		return theme.Green
	case "MODIFIED":
		return theme.Yellow
	case "REMOVED":
		return theme.Red
	case "RENAMED":
		return theme.Blue
	}
	return theme.Dim
}

// sectionLabel is what the badge of s says: what the delta does, else the title.
func sectionLabel(s *section) string {
	if s.op != "" {
		return s.op
	}
	return strings.ToUpper(s.title)
}

// count is n of a thing, in English: "1 step", "2 steps".
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return strconv.Itoa(n) + " " + thing + "s"
}

// specLines lays a spec out in rows w cells wide.
type specLines struct {
	rows []specRow
	w    int
	at   specRow // what the next rows are
}

// add puts the rows of spans, wrapped to what is left of the width after first; the rows after
// the first start with hang, as wide as first.
func (b *specLines) add(first, hang string, spans ...span) {
	for i, row := range flow(spans, b.w-ansi.StringWidth(first)) {
		lead := first
		if i > 0 {
			lead = hang
		}
		r := b.at
		r.lead, r.spans = lead, row
		b.rows = append(b.rows, r)
	}
}

// noRow is a row that is nothing of the spec's.
var noRow = specRow{sec: -1, req: -1, scen: -1}

// gap puts a blank row that is what at is, unless there is no row above or the row above is blank.
func (b *specLines) gap(at specRow) {
	if n := len(b.rows); n > 0 && !b.rows[n-1].blank() {
		b.rows = append(b.rows, specRow{kind: at.kind, sec: at.sec, req: at.req, scen: at.scen})
	}
}

// layout lays sp out w cells wide, what is open in folds open.
func (sp *spec) layout(folds map[string]bool, w int) []specRow {
	b := &specLines{w: max(w, 1), at: noRow}
	top := []span{{sp.capability, specCapStyle}}
	if sp.delta {
		top = append(top, span{"  ·  delta", dimStyle})
	}
	b.add("", "", top...)
	b.gap(noRow)
	b.blocks(sp.lead, "", "", "")
	for i, s := range sp.sections {
		b.gap(noRow)
		c := opColor(s.op)
		if label := sectionLabel(s); label != "" {
			b.at = specRow{kind: rowSection, sec: i, req: -1, scen: -1}
			spans := []span{{" " + label + " ", lipgloss.NewStyle().Background(c).Foreground(theme.Bg).Bold(true)}}
			if s.op != "" || len(s.reqs) > 0 { // a main spec's purpose holds none, as it should
				spans = append(spans, span{"  " + count(len(s.reqs), "requirement"), dimStyle})
			}
			b.add("", "", spans...)
			b.gap(noRow)
		}
		b.at = specRow{sec: i, req: -1, scen: -1}
		b.blocks(s.text, "", "", "")
		bar := lipgloss.NewStyle().Foreground(c).Render("▌") + " "
		for j := range s.reqs {
			b.requirement(s, i, j, folds, bar)
		}
	}
	for len(b.rows) > 0 && b.rows[len(b.rows)-1].blank() {
		b.rows = b.rows[:len(b.rows)-1]
	}
	return b.rows
}

// requirement puts requirement j of section s, number i, after bar: its name, and, open, its text
// and its scenarios.
func (b *specLines) requirement(s *section, i, j int, folds map[string]bool, bar string) {
	r := s.reqs[j]
	open := reqOpen(folds, s, j)
	b.at = specRow{kind: rowReq, sec: i, req: j, scen: -1}
	if open { // the blank row above it is its own: a second click where the first was folds it back
		b.gap(b.at)
	}
	if !open {
		b.add(bar+dimStyle.Render("▸ "), bar+"  ", span{r.name, specNameStyle},
			span{"  · " + count(len(r.scenarios), "scenario"), dimStyle})
		return
	}
	b.add(bar+fgStyle.Render("▾ "), bar+"  ", span{r.name, specNameStyle.Bold(true)})
	b.at.kind = rowText
	b.blocks(r.body, bar+"  ", bar, "")
	if len(r.scenarios) > 0 {
		b.add(bar, bar)
	}
	for k, sc := range r.scenarios {
		b.at = specRow{kind: rowScenario, sec: i, req: j, scen: k}
		lead := bar + "  "
		if !folds[sc.key] {
			b.add(lead+dimStyle.Render("▸ "), lead+"  ", span{sc.name, fgStyle},
				span{"  · " + count(sc.stepCount(), "step"), dimStyle})
			continue
		}
		b.add(lead+fgStyle.Render("▾ "), lead+"  ", span{sc.name, specNameStyle})
		b.at.kind = rowText
		b.blocks(sc.steps, bar+"    ", bar, "       ")
	}
	b.gap(noRow)
}

// blocks puts bs after lead: a step's keyword right-aligned in 5 cells, its text after it; the
// text of other blocks after pad, a quote's after a bar. A row of sep stands for a blank line
// between two blocks.
func (b *specLines) blocks(bs []block, lead, sep, pad string) {
	for i, bl := range bs {
		if i > 0 && bl.apart {
			b.add(sep, sep)
		}
		hang := lead + pad
		switch bl.kind {
		case blockStep:
			kw := specStepStyle.Render(fmt.Sprintf("%5s", bl.keyword)) + "  "
			b.add(lead+kw, lead+strings.Repeat(" ", 7), inline(bl.text, specTextStyle)...)
		case blockQuote:
			gutter := lead + pad[:max(len(pad)-2, 0)] + dimStyle.Render("│ ")
			b.add(gutter, gutter, inline(bl.text, dimStyle)...)
		case blockHeading:
			b.add(hang, hang, inline(bl.text, specNameStyle.Bold(true))...)
		case blockCode:
			b.add(hang, hang, span{bl.text, fgStyle})
		default:
			b.add(hang, hang, inline(bl.text, specTextStyle)...)
		}
	}
}

// span is a run of text in one style.
type span struct {
	text string
	st   lipgloss.Style
}

// inline splits the text of a spec into runs in base: `code` violet, the normative SHALL and MUST,
// and their NOT, red and bold; the ** of bold text dropped.
func inline(s string, base lipgloss.Style) []span {
	var out []span
	plain := func(t string) {
		t = strings.ReplaceAll(t, "**", "")
		at := 0
		for _, m := range specNormRe.FindAllStringIndex(t, -1) {
			out = append(out, span{t[at:m[0]], base}, span{t[m[0]:m[1]], specNormStyle})
			at = m[1]
		}
		out = append(out, span{t[at:], base})
	}
	for {
		i := strings.IndexByte(s, '`')
		j := -1
		if i >= 0 {
			j = strings.IndexByte(s[i+1:], '`')
		}
		if j < 0 {
			plain(s)
			return out
		}
		plain(s[:i])
		out = append(out, span{s[i+1 : i+1+j], specCodeStyle})
		s = s[i+2+j:]
	}
}

// flow wraps spans into rows at most w cells wide, where segments breaks a line; a cell wider
// than w takes a row of its own.
func flow(spans []span, w int) [][]span {
	var line strings.Builder
	ends := make([]int, len(spans)) // the rune after each span
	n := 0
	for i, s := range spans {
		line.WriteString(s.text)
		n += utf8.RuneCountInString(s.text)
		ends[i] = n
	}
	text := line.String()
	cs, _ := layout(text, math.MaxInt, 0)
	segs := segments(text, max(w, 1))
	rows := make([][]span, len(segs))
	c, x, k := 0, 0, 0
	for i, sg := range segs {
		var run strings.Builder
		cur := -1
		for ; c < len(cs) && (x < sg[1] || i == len(segs)-1); c++ {
			for k < len(spans)-1 && cs[c].r0 >= ends[k] {
				k++
			}
			if k != cur && run.Len() > 0 {
				rows[i] = append(rows[i], span{run.String(), spans[cur].st})
				run.Reset()
			}
			cur = k
			run.WriteString(cs[c].s)
			x += cs[c].w
		}
		if run.Len() > 0 {
			rows[i] = append(rows[i], span{run.String(), spans[cur].st})
		}
	}
	return rows
}
