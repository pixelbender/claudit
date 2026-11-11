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
	Mode  string // the mode a click on the switch of a MODIFIED requirement picks for it; its fold stays
}

// Fold opens or folds what h is on.
func (s Specs) Fold(h SpecHit) {
	if h.Mode != "" {
		return
	}
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

// specLayout is a spec laid out, and what from: its text, the main spec's, the width, what was
// open and what its MODIFIED requirements showed.
type specLayout struct {
	lines, main []string
	doc         *spec
	width       int
	folds       map[string]bool
	modes       map[string]string
	rows        []specRow
}

// laidOut is the spec of tab, of capability, a delta or not, laid out w cells wide with folds
// open and its MODIFIED requirements in modes, against the main spec a delta modifies: the last
// frame's, unless the text, the main spec, the width, the folds or the modes changed.
func (o Options) laidOut(st *ide.State, tab *ide.Tab, capability string, delta bool, folds map[string]bool, modes map[string]string, w int) *specLayout {
	path, archived := specMain(tab.Path)
	var main []string
	if path != "" && !archived {
		main = o.mainLines(st, path)
	}
	l := o.SpecRows[tab.Path]
	if l == nil || l.doc.delta != delta || !slices.Equal(l.lines, tab.Buf.Lines) || !slices.Equal(l.main, main) {
		l = &specLayout{doc: parseSpec(tab.Buf.Lines, delta), width: -1}
		l.doc.capability, l.doc.archived = capability, archived
		if main != nil {
			l.doc.compare(parseSpec(main, false))
		}
		if o.SpecRows != nil {
			l.lines, l.main = slices.Clone(tab.Buf.Lines), slices.Clone(main)
			o.SpecRows[tab.Path] = l
		}
	}
	if l.width != w || !maps.Equal(l.folds, folds) || !maps.Equal(l.modes, modes) {
		l.rows, l.width, l.folds, l.modes = l.doc.layout(folds, modes, w), w, maps.Clone(folds), maps.Clone(modes)
	}
	return l
}

// mainLines is the text of the main spec at path: the tab's when it is open, else what o.Main
// reads; nil for none.
func (o Options) mainLines(st *ide.State, path string) []string {
	for _, t := range st.Tabs {
		if t.Path == path && !t.Hex {
			return t.Buf.Lines
		}
	}
	if o.Main != nil {
		return o.Main(path)
	}
	return nil
}

// specView is an OpenSpec spec as an editor shows it structured: its rows, and the window on them.
type specView struct {
	path      string
	doc       *spec
	folds     map[string]bool
	modes     map[string]string
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
	l := o.laidOut(st, tab, capability, delta, o.Specs[tab.Path], o.Modes[tab.Path], textWidth(iw))
	if n, _ := l.doc.counts(); n == 0 {
		return nil
	}
	v := &specView{path: tab.Path, doc: l.doc, folds: o.Specs[tab.Path], modes: o.Modes[tab.Path], width: l.width, rows: l.rows}
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
	sec, req, scen int    // where in the spec it is; -1 for none
	key            string // a scenario's fold key
	open           bool   // the scenario is open
}

// draw is r styled, at most w cells wide.
func (r specRow) draw(w int) string {
	var b strings.Builder
	b.WriteString(r.lead)
	for _, s := range r.spans {
		if s.st.GetStrikethrough() { // lipgloss styles a struck run a character at a time
			b.WriteString(specDelSGR.Styled(s.text))
			continue
		}
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

// tagSpan is the span of a tag after a header or a scenario's name: two blanks, a dot, then text. The
// blanks inside it are no-break spaces, so that a row wraps between tags and not within one.
func tagSpan(text string, st lipgloss.Style) span {
	return span{text: "  " + strings.ReplaceAll("· "+text, " ", "\u00a0"), st: st}
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
		if n := len(row) - 1; n >= 0 && row[n].mark { // a marked run ends at its text
			row[n].text = strings.TrimRight(row[n].text, " ")
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
		b.rows = append(b.rows, specRow{kind: at.kind, sec: at.sec, req: at.req, scen: at.scen, key: at.key, open: at.open})
	}
}

// layout lays sp out w cells wide, what is open in folds open.
func (sp *spec) layout(folds map[string]bool, modes map[string]string, w int) []specRow {
	b := &specLines{w: max(w, 1), at: noRow}
	top := []span{{text: sp.capability, st: specCapStyle}}
	if sp.delta {
		top = append(top, span{text: "  ·  delta", st: dimStyle})
	}
	b.add("", "", top...)
	b.gap(noRow)
	b.blocks(sp.lead, "", "", "")
	for i, s := range sp.sections {
		b.gap(noRow)
		c := opColor(s.op)
		if label := sectionLabel(s); label != "" {
			b.at = specRow{kind: rowSection, sec: i, req: -1, scen: -1}
			spans := []span{{text: " " + label + " ", st: lipgloss.NewStyle().Background(c).Foreground(theme.Bg).Bold(true)}}
			if s.op != "" || len(s.reqs) > 0 { // a main spec's purpose holds none, as it should
				spans = append(spans, span{text: "  " + count(len(s.reqs), "requirement"), st: dimStyle})
			}
			b.add("", "", spans...)
			b.gap(noRow)
		}
		b.at = specRow{sec: i, req: -1, scen: -1}
		b.blocks(s.text, "", "", "")
		bar := lipgloss.NewStyle().Foreground(c).Render("▌") + " "
		for j := range s.reqs {
			b.requirement(sp, s, i, j, folds, modes, bar)
		}
	}
	for len(b.rows) > 0 && b.rows[len(b.rows)-1].blank() {
		b.rows = b.rows[:len(b.rows)-1]
	}
	return b.rows
}

// requirement puts requirement j of section s, number i, after bar: its name, and, open, its text
// and its scenarios. A MODIFIED requirement of a delta found in the main spec shows its text
// there, its own, or what it changed, as modes say, with each scenario's change.
func (b *specLines) requirement(sp *spec, s *section, i, j int, folds map[string]bool, modes map[string]string, bar string) {
	r := s.reqs[j]
	open := reqOpen(folds, s, j)
	mode := modeOf(modes, r.key)
	b.at = specRow{kind: rowReq, sec: i, req: j, scen: -1}
	if open { // the blank row above it is its own: a second click where the first was folds it back
		b.gap(b.at)
	}
	tags := sp.reqTags(s, r, mode)
	scens := plainScenarios(r)
	if d := r.diff; d != nil {
		scens = d.shown(mode)
	}
	if !open { // the scenarios it counts are those it lists open
		b.add(bar+dimStyle.Render("▸ "), bar+"  ", append([]span{{text: r.name, st: specNameStyle},
			tagSpan(count(len(scens), "scenario"), dimStyle)}, tags...)...)
		return
	}
	b.add(bar+fgStyle.Render("▾ "), bar+"  ", append([]span{{text: r.name, st: specNameStyle.Bold(true)}}, tags...)...)
	b.at.kind = rowText
	d := r.diff
	switch {
	case d == nil || mode == ModeNow:
		b.blocks(r.body, bar+"  ", bar, "")
	case mode == ModeWas:
		b.blocks(d.was.body, bar+"  ", bar, "")
	default:
		b.diffBlocks(d.body, bar+"  ", bar, "")
	}
	if len(scens) > 0 {
		b.add(bar, bar)
	}
	for k, p := range scens {
		sc := p.now
		if sc == nil || mode == ModeWas && p.was != nil {
			sc = p.was
		}
		scOpen, set := folds[p.key]
		if !set {
			scOpen = d != nil && mode == ModeDiff && p.state == scenNew // what a diff adds shows
		}
		b.at = specRow{kind: rowScenario, sec: i, req: j, scen: k, key: p.key, open: scOpen}
		lead := bar + "  "
		var tag []span
		if d != nil {
			tag = []span{scenTag(p.state)}
		}
		gone := p.state == scenRemoved && mode == ModeDiff
		if !scOpen {
			name := fgStyle
			if gone {
				name = specDelStyle
			}
			b.add(lead+dimStyle.Render("▸ "), lead+"  ", append([]span{{text: sc.name, st: name},
				tagSpan(count(sc.stepCount(), "step"), dimStyle)}, tag...)...)
			continue
		}
		name := specNameStyle
		if gone {
			name = specDelStyle
		}
		b.add(lead+fgStyle.Render("▾ "), lead+"  ", append([]span{{text: sc.name, st: name}}, tag...)...)
		b.at.kind = rowText
		if d != nil && mode == ModeDiff && p.state != scenNew {
			b.diffBlocks(p.steps, bar+"    ", bar, "       ")
		} else {
			b.blocks(sc.steps, bar+"    ", bar, "       ")
		}
	}
	b.gap(noRow)
}

// plainScenarios are the scenarios of r with nothing to compare them to.
func plainScenarios(r *requirement) []scenPair {
	out := make([]scenPair, len(r.scenarios))
	for k, sc := range r.scenarios {
		out[k] = scenPair{now: sc, key: sc.key}
	}
	return out
}

// shown are the scenarios d shows in mode: the main spec's in was, the delta's in now, both in diff.
func (d *reqDiff) shown(mode string) []scenPair {
	if mode == ModeDiff {
		return d.scens
	}
	var out []scenPair
	for _, p := range d.scens {
		if mode == ModeWas && p.was != nil || mode == ModeNow && p.now != nil {
			out = append(out, p)
		}
	}
	return out
}

// scenTag is what a scenario of a MODIFIED requirement says of its change.
func scenTag(st scenState) span {
	switch st {
	case scenChanged:
		return tagSpan("changed", specChgStyle)
	case scenNew:
		return tagSpan("+ new", specNewStyle)
	case scenRemoved:
		return tagSpan("− removed", lipgloss.NewStyle().Foreground(theme.Red))
	}
	return tagSpan("unchanged", dimStyle)
}

// reqTags is what the header of requirement r of section s says after its name: for a MODIFIED
// one of a delta, the switch between was, now and diff with mode lit, and how many changes it
// makes; else why there is nothing to compare.
func (sp *spec) reqTags(s *section, r *requirement, mode string) []span {
	switch {
	case !sp.delta || s.op != "MODIFIED":
		return nil
	case sp.archived: // the main spec holds the change already
		return []span{tagSpan("archived", dimStyle)}
	case r.diff == nil:
		return []span{tagSpan("not in the main spec", dimStyle)}
	}
	out := []span{{text: "   ", st: dimStyle}}
	for k, m := range specModes {
		if k > 0 {
			out = append(out, span{text: "\u00a0·\u00a0", st: dimStyle}) // the switch wraps whole, as a tag does
		}
		st := dimStyle
		if m == mode {
			st = specModeOn
		}
		out = append(out, span{text: m, st: st, mode: m})
	}
	n := tagSpan(count(r.diff.changes, "change"), specChgStyle)
	if r.diff.changes == 0 {
		n.st = dimStyle
	}
	return append(out, n)
}

// diffBlocks puts the blocks ps pair up as blocks does, each with what it removes struck out and
// what it adds marked. A pair that is whole (a line of code, a step whose keyword changed) is the
// old block as written, struck out, above the new one, marked.
func (b *specLines) diffBlocks(ps []blockPair, lead, sep, pad string) {
	for i, p := range ps {
		bl := p.now
		if bl == nil {
			bl = p.was
		}
		if i > 0 && bl.apart {
			b.add(sep, sep)
		}
		if p.whole() {
			b.block(p.was, lead, pad, struck(asWritten(p.was)))
			b.block(p.now, lead, pad, marked(asWritten(p.now)))
			continue
		}
		b.block(bl, lead, pad, p.diffSpans(blockBase(bl.kind)))
	}
}

// blockBase is the style of the text of a block of kind.
func blockBase(kind blockKind) lipgloss.Style {
	switch kind {
	case blockQuote:
		return dimStyle
	case blockHeading:
		return specNameStyle.Bold(true)
	case blockCode:
		return fgStyle
	}
	return specTextStyle
}

// asWritten is the text of bl in one run per inline style: a line of code as written, the rest as
// inline sets it.
func asWritten(bl *block) []span {
	if bl.kind == blockCode {
		return []span{{text: bl.text, st: blockBase(bl.kind)}}
	}
	return inline(bl.text, blockBase(bl.kind))
}

// block puts the rows of sp, the text of bl, after lead: a step's keyword in its column, a quote's
// gutter, or pad.
func (b *specLines) block(bl *block, lead, pad string, sp []span) {
	switch bl.kind {
	case blockStep:
		kw := specStepStyle.Render(fmt.Sprintf("%5s", bl.keyword)) + "  "
		b.add(lead+kw, lead+strings.Repeat(" ", 7), sp...)
	case blockQuote:
		gutter := lead + pad[:max(len(pad)-2, 0)] + dimStyle.Render("│ ")
		b.add(gutter, gutter, sp...)
	default:
		b.add(lead+pad, lead+pad, sp...)
	}
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
			b.add(hang, hang, span{text: bl.text, st: fgStyle})
		default:
			b.add(hang, hang, inline(bl.text, specTextStyle)...)
		}
	}
}

// span is a run of text in one style.
type span struct {
	text string
	st   lipgloss.Style
	mode string // the mode a click on it picks for its requirement: a word of the switch
	mark bool   // text a diff adds or removes: its background or its strike ends with its text
}

// with is s with text in place of its own.
func (s span) with(text string) span {
	s.text = text
	return s
}

// inline splits the text of a spec into runs in base: `code` violet, the normative SHALL and MUST,
// and their NOT, red and bold; the ** of bold text dropped.
func inline(s string, base lipgloss.Style) []span {
	var out []span
	plain := func(t string) {
		t = strings.ReplaceAll(t, "**", "")
		at := 0
		for _, m := range specNormRe.FindAllStringIndex(t, -1) {
			out = append(out, span{text: t[at:m[0]], st: base}, span{text: t[m[0]:m[1]], st: specNormStyle})
			at = m[1]
		}
		out = append(out, span{text: t[at:], st: base})
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
		out = append(out, span{text: s[i+1 : i+1+j], st: specCodeStyle})
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
				rows[i] = append(rows[i], spans[cur].with(run.String()))
				run.Reset()
			}
			cur = k
			run.WriteString(cs[c].s)
			x += cs[c].w
		}
		if run.Len() > 0 {
			rows[i] = append(rows[i], spans[cur].with(run.String()))
		}
	}
	return rows
}
