package ideview

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/theme"
)

// What a MODIFIED requirement of a delta shows: the main spec's text (was), the delta's (now), or
// the delta's with what it changed marked (diff, the default).
const (
	ModeWas  = "was"
	ModeNow  = "now"
	ModeDiff = "diff"
)

// specModes are the words of the switch on the header of a MODIFIED requirement, in order.
var specModes = []string{ModeWas, ModeNow, ModeDiff}

// SpecModes is what each MODIFIED requirement of the specs shows, by the spec's path and the
// requirement's fold key; one missing shows the diff.
type SpecModes map[string]map[string]string

// Set chooses the mode h picks for the requirement it is on; nothing for another hit.
func (m SpecModes) Set(h SpecHit) {
	if h.Mode == "" || len(h.Folds) != 1 {
		return
	}
	if m[h.Path] == nil {
		m[h.Path] = map[string]string{}
	}
	m[h.Path][h.Folds[0]] = h.Mode
}

// modeOf is the mode of the requirement keyed key in modes.
func modeOf(modes map[string]string, key string) string {
	if m := modes[key]; m == ModeWas || m == ModeNow {
		return m
	}
	return ModeDiff
}

// specMain is the main spec a change's delta at path modifies, and whether the change is archived;
// "" for a path that is no delta.
func specMain(path string) (main string, archived bool) {
	capability, delta, ok := specPath(path)
	if !ok || !delta {
		return "", false
	}
	q, _ := strings.CutSuffix("/"+filepath.ToSlash(path), "/spec.md") // as specPath finds the root
	i := strings.LastIndex(q, "/openspec/") + len("/openspec/")
	archived = strings.HasPrefix(q[i:], "changes/archive/")
	return filepath.FromSlash(q[1:i] + "specs/" + capability + "/spec.md"), archived
}

// reqDiff is a MODIFIED requirement of a delta against the requirement of its name in the main
// spec.
type reqDiff struct {
	was     *requirement
	body    []blockPair
	scens   []scenPair // the scenarios of both, the removed ones where the main spec has them
	changes int        // the changed runs of the body and the scenarios added, removed or changed
}

// blockPair is a block of the main spec's text and the block of the delta's in its place; nil for
// one the other has no counterpart of.
type blockPair struct {
	was, now *block
	hunks    []hunk // the word diff from was to now when both are there
}

type scenState int

const (
	scenSame scenState = iota
	scenChanged
	scenNew
	scenRemoved
)

// scenPair is a scenario of the main spec's requirement and the delta's of its name.
type scenPair struct {
	was, now *scenario
	state    scenState
	steps    []blockPair
	key      string // what its fold is kept by: the delta's scenario's, else one of its own
}

// compare matches each MODIFIED requirement of sp to the requirement of its name in main.
func (sp *spec) compare(main *spec) {
	byName := map[string]*requirement{}
	if main != nil {
		for _, s := range main.sections {
			for _, r := range s.reqs {
				if _, ok := byName[r.name]; !ok {
					byName[r.name] = r
				}
			}
		}
	}
	for _, s := range sp.sections {
		for _, r := range s.reqs {
			r.diff = nil
			if was := byName[r.name]; s.op == "MODIFIED" && was != nil {
				r.diff = diffRequirement(was, r)
			}
		}
	}
}

func diffRequirement(was, now *requirement) *reqDiff {
	d := &reqDiff{was: was, body: pairBlocks(was.body, now.body)}
	for _, p := range d.body {
		d.changes += p.changes()
	}
	d.scens = pairScenarios(was.scenarios, now.scenarios, now.key)
	for _, p := range d.scens {
		if p.state != scenSame {
			d.changes++
		}
	}
	return d
}

// whole reports whether p is drawn as the whole old block struck out above the whole new one: the
// two differ in kind or in a step's keyword, or are lines of code that differ.
func (p blockPair) whole() bool {
	if p.was == nil || p.now == nil {
		return false
	}
	return p.was.kind != p.now.kind || p.was.keyword != p.now.keyword ||
		p.was.kind == blockCode && p.was.text != p.now.text
}

// changes is how many changed runs p holds; a pair drawn whole is one change.
func (p blockPair) changes() int {
	if p.was == nil || p.now == nil || p.whole() {
		return 1
	}
	n := 0
	for _, h := range p.hunks {
		if !h.same() {
			n++
		}
	}
	return n
}

// pairBlocks lines the blocks of was and now up: the same blocks with each other, the others in
// between in their order, a changed block with the one in its place.
func pairBlocks(was, now []block) []blockPair {
	same := func(a, b block) bool { return a.kind == b.kind && a.keyword == b.keyword && a.text == b.text }
	ops := lcs(len(was), len(now), func(i, j int) bool { return same(was[i], now[j]) })
	var out []blockPair
	var gone, came []*block
	flush := func() {
		for k := range max(len(gone), len(came)) {
			var p blockPair
			if k < len(gone) {
				p.was = gone[k]
			}
			if k < len(came) {
				p.now = came[k]
			}
			switch {
			case p.whole() && p.was.text != p.now.text: // no words to tell apart
				p.hunks = []hunk{{del: p.was.text, ins: p.now.text}}
			case p.whole():
				p.hunks = []hunk{{eq: p.now.text}}
			case p.was != nil && p.now != nil:
				p.hunks = wordDiff(p.was.text, p.now.text)
			}
			out = append(out, p)
		}
		gone, came = nil, nil
	}
	i, j := 0, 0
	for _, op := range ops {
		switch op {
		case '=':
			flush()
			out = append(out, blockPair{was: &was[i], now: &now[j], hunks: []hunk{{eq: now[j].text}}})
			i, j = i+1, j+1
		case '-':
			gone = append(gone, &was[i])
			i++
		default:
			came = append(came, &now[j])
			j++
		}
	}
	flush()
	return out
}

// pairScenarios lines the scenarios of was and now up by name: the delta's in its order, each
// removed one after the scenario the main spec has before it.
func pairScenarios(was, now []*scenario, reqKey string) []scenPair {
	at := map[string][]int{} // the main spec's scenarios of a name, in order
	for k, s := range was {
		at[s.name] = append(at[s.name], k)
	}
	match := make([]int, len(now)) // the main spec's scenario of each of the delta's; -1 for none
	taken := make([]bool, len(was))
	for k, s := range now {
		match[k] = -1
		if ks := at[s.name]; len(ks) > 0 {
			match[k], at[s.name] = ks[0], ks[1:]
			taken[ks[0]] = true
		}
	}
	var out []scenPair
	gone := func(from int) { // the removed scenarios from from on, up to the next kept one
		for k := from; k < len(was) && !taken[k]; k++ {
			p := scenPair{was: was[k], state: scenRemoved, key: reqKey + "\n" + was[k].name + "\x00removed"}
			for i := range was[k].steps {
				p.steps = append(p.steps, blockPair{was: &was[k].steps[i]})
			}
			out = append(out, p)
		}
	}
	gone(0)
	for k, s := range now {
		p := scenPair{now: s, state: scenNew, key: s.key}
		if m := match[k]; m >= 0 {
			p.was, p.state = was[m], scenSame
			p.steps = pairBlocks(was[m].steps, s.steps)
			for _, q := range p.steps {
				if q.changes() > 0 {
					p.state = scenChanged
				}
			}
		}
		out = append(out, p)
		if m := match[k]; m >= 0 {
			gone(m + 1)
		}
	}
	return out
}

// lcsCells is the most cells lcs fills in: a pair of sequences that needs more is told apart
// whole.
const lcsCells = 1_000_000

// lcs is the edit from a sequence of n to one of m with eq telling equal elements apart: '='
// for an element both keep, '-' for one of the first only, '+' for one of the second only; what
// goes before what comes in each change. Sequences of more than lcsCells cells together are not
// compared: all of the first goes, then all of the second comes.
func lcs(n, m int, eq func(i, j int) bool) []byte {
	if n*m > lcsCells {
		ops := make([]byte, 0, n+m)
		for range n {
			ops = append(ops, '-')
		}
		for range m {
			ops = append(ops, '+')
		}
		return ops
	}
	l := make([][]int32, n+1)
	for i := range l {
		l[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if eq(i, j) {
				l[i][j] = l[i+1][j+1] + 1
			} else {
				l[i][j] = max(l[i+1][j], l[i][j+1])
			}
		}
	}
	ops := make([]byte, 0, n+m)
	for i, j := 0, 0; i < n || j < m; {
		switch {
		case i < n && j < m && eq(i, j):
			ops = append(ops, '=')
			i, j = i+1, j+1
		case i < n && (j == m || l[i+1][j] >= l[i][j+1]):
			ops = append(ops, '-')
			i++
		default:
			ops = append(ops, '+')
			j++
		}
	}
	return ops
}

// hunk is a run of a word diff: text both keep, or what it removes and adds in its place.
type hunk struct{ eq, del, ins string }

func (h hunk) same() bool { return h.del == "" && h.ins == "" }

// specTokenRe splits text into what a word diff compares: a `code` span and a ** marker whole,
// words, runs of blanks, any other character.
var specTokenRe = regexp.MustCompile("`[^`]*`|\\*\\*|[\\p{L}\\p{N}_]+|\\s+|.")

// wordDiff is the change from a to b by words. A short run kept between two changes ("в", ", ")
// goes into the change, so that it reads as one. A change of ** markers and blanks alone is no
// change: the run is kept as b has it.
func wordDiff(a, b string) []hunk {
	if a == b {
		return []hunk{{eq: a}}
	}
	x, y := specTokenRe.FindAllString(a, -1), specTokenRe.FindAllString(b, -1)
	var hs []hunk
	i, j := 0, 0
	for _, op := range lcs(len(x), len(y), func(i, j int) bool { return x[i] == y[j] }) {
		if op == '=' {
			if n := len(hs) - 1; n >= 0 && hs[n].same() {
				hs[n].eq += x[i]
			} else {
				hs = append(hs, hunk{eq: x[i]})
			}
			i, j = i+1, j+1
			continue
		}
		if n := len(hs) - 1; n < 0 || hs[n].same() {
			hs = append(hs, hunk{})
		}
		h := &hs[len(hs)-1]
		if op == '-' {
			h.del += x[i]
			i++
		} else {
			h.ins += y[j]
			j++
		}
	}
	var kept []hunk
	for _, h := range hs {
		if !h.same() && markup(h.del) && markup(h.ins) {
			h = hunk{eq: h.ins}
		}
		if n := len(kept) - 1; n >= 0 && kept[n].same() && h.same() {
			kept[n].eq += h.eq
		} else {
			kept = append(kept, h)
		}
	}
	hs = kept
	for k := 1; k+1 < len(hs); {
		if hs[k].same() && !hs[k-1].same() && !hs[k+1].same() && slight(hs[k].eq) {
			hs[k-1].del += hs[k].eq + hs[k+1].del
			hs[k-1].ins += hs[k].eq + hs[k+1].ins
			hs = append(hs[:k], hs[k+2:]...)
			continue
		}
		k++
	}
	return hs
}

// markup reports whether s is ** markers and blanks alone, which the view does not draw.
func markup(s string) bool { return strings.TrimSpace(strings.ReplaceAll(s, "**", "")) == "" }

// slight reports whether a kept run is too short to read as kept between two changes: blanks,
// punctuation, and one word of two letters at most.
func slight(s string) bool {
	words := 0
	for _, t := range specTokenRe.FindAllString(s, -1) {
		r, _ := utf8.DecodeRuneInString(t)
		switch {
		case strings.TrimSpace(t) == "" || utf8.RuneCountInString(t) == 1 && !isWord(r):
		case utf8.RuneCountInString(t) > 2:
			return false
		default:
			words++
		}
	}
	return words <= 1
}

var (
	specDelStyle = lipgloss.NewStyle().Foreground(theme.Red).Strikethrough(true)
	specDelSGR   = ansi.Style{}.ForegroundColor(theme.Red).Strikethrough(true) // specDelStyle in one sequence
	specNewStyle = lipgloss.NewStyle().Foreground(theme.Green)
	specChgStyle = lipgloss.NewStyle().Foreground(theme.Yellow)
	specModeOn   = lipgloss.NewStyle().Foreground(theme.Bright).Background(theme.SelBg)
)

// marked is sp on the background of a diff's added text, in its own colours.
func marked(sp []span) []span {
	for i := range sp {
		sp[i].st, sp[i].mark = sp[i].st.Background(theme.AddedBg), true
	}
	return sp
}

// struck is sp struck out in red.
func struck(sp []span) []span {
	for i := range sp {
		sp[i].st, sp[i].mark = specDelStyle, true
	}
	return sp
}

// diffSpans is the text of p in base, what it removes struck out and what it adds marked.
func (p blockPair) diffSpans(base lipgloss.Style) []span {
	switch {
	case p.was == nil:
		return marked(inline(p.now.text, base))
	case p.now == nil:
		return struck(inline(p.was.text, base))
	}
	var out []span
	for _, h := range p.hunks {
		if h.same() {
			out = append(out, inline(h.eq, base)...)
			continue
		}
		if h.del != "" {
			out = append(out, struck(inline(h.del, base))...)
		}
		if h.ins != "" {
			out = append(out, marked(inline(h.ins, base))...)
		}
	}
	return out
}
