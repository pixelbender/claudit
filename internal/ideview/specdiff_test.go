package ideview

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/theme"
)

// diffMainFile is the main spec specFile's delta modifies.
const diffMainFile = root + "/openspec/specs/auth/login/spec.md"

const diffMain = "# auth Specification\n\n## Requirements\n\n" +
	"### Requirement: Sessions\nA session SHALL last an hour.\n\nIts token is `opaque`.\n\n" +
	"#### Scenario: Expiry\n- **WHEN** an hour passes\n- **THEN** the session ends\n\n" +
	"#### Scenario: Remember\n- **WHEN** the user ticks remember me\n- **THEN** the session lasts a week\n\n" +
	"#### Scenario: Logout\n- **WHEN** the user logs out\n- **THEN** the session ends\n\n" +
	"### Requirement: Other\nUnchanged.\n"

const diffDelta = "## MODIFIED Requirements\n\n" +
	"### Requirement: Sessions\nA session SHALL last a day.\n\nIts token is `opaque`.\n\n" +
	"#### Scenario: Expiry\n- **WHEN** a day passes\n- **THEN** the session ends\n\n" +
	"#### Scenario: Refresh\n- **WHEN** the user comes back\n- **THEN** the session lasts a day more\n\n" +
	"#### Scenario: Logout\n- **WHEN** the user logs out\n- **THEN** the session ends\n\n" +
	"### Requirement: Unknown\nNot in the main spec.\n"

const sessions = "MODIFIED Requirements\nSessions" // the fold key of the requirement diffed

// diffState is specState with the delta diffDelta, and options that read main for diffMainFile,
// counting the reads in reads.
func diffState(main string, reads *int) (*ide.State, Options) {
	st := specState()
	st.Tabs[2].Buf.SetText(diffDelta)
	o := Options{Now: now, Specs: Specs{}, Modes: SpecModes{}, SpecRows: SpecCache{}, Main: func(path string) []string {
		*reads++
		if path != diffMainFile || main == "" {
			return nil
		}
		return strings.Split(main, "\n")
	}}
	return st, o
}

// joined is the text hs keep and remove, and the text they keep and add.
func joined(hs []hunk) (was, now string) {
	for _, h := range hs {
		was += h.eq + h.del
		now += h.eq + h.ins
	}
	return was, now
}

// A word diff marks whole words, a `code` span whole, a word whose case changed whole; a short
// run between two changes goes into one change; a moved phrase keeps what it can.
func TestWordDiff(t *testing.T) {
	for _, tt := range []struct {
		name, a, b string
		want       []hunk // nil: only check that it rebuilds both texts
	}{
		{"same", "Сообщение SHALL быть", "Сообщение SHALL быть", []hunk{{eq: "Сообщение SHALL быть"}}},
		{"cyrillic", "Сообщение SHALL быть системным.", "Сообщение SHALL быть обычным.",
			[]hunk{{eq: "Сообщение SHALL быть "}, {del: "системным", ins: "обычным"}, {eq: "."}}},
		{"code", "uses `old_name` here", "uses `new_name` here",
			[]hunk{{eq: "uses "}, {del: "`old_name`", ins: "`new_name`"}, {eq: " here"}}},
		{"case", "клиенты узнают", "Клиенты узнают", []hunk{{del: "клиенты", ins: "Клиенты"}, {eq: " узнают"}}},
		{"short run", "красный в синем", "зелёный в жёлтом", []hunk{{del: "красный в синем", ins: "зелёный в жёлтом"}}},
		{"long run", "красный цвет синий", "зелёный цвет жёлтый",
			[]hunk{{del: "красный", ins: "зелёный"}, {eq: " цвет "}, {del: "синий", ins: "жёлтый"}}},
		{"added", "", "новое", []hunk{{ins: "новое"}}},
		{"reorder", "об изменении статуса клиенты узнают из события", "клиенты узнают об изменении статуса из события", nil},
	} {
		got := wordDiff(tt.a, tt.b)
		if tt.want != nil && !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %q", tt.name, got)
		}
		if a, b := joined(got); a != tt.a || b != tt.b {
			t.Errorf("%s: rebuilds %q and %q", tt.name, a, b)
		}
	}
	got := wordDiff("об изменении статуса клиенты узнают из события", "клиенты узнают об изменении статуса из события")
	if kept := got[len(got)-1]; kept.eq != "из события" || len(got) > 4 { // moved: removed there, added here
		t.Errorf("a moved phrase: %q", got)
	}
}

// Paragraphs pair up with the same ones, a changed one with the one in its place; what has no
// counterpart stands alone.
func TestPairBlocks(t *testing.T) {
	bl := func(ss ...string) []block {
		var out []block
		for _, s := range ss {
			out = append(out, block{text: s})
		}
		return out
	}
	text := func(b *block) string {
		if b == nil {
			return "-"
		}
		return b.text
	}
	for _, tt := range []struct {
		was, now []block
		want     []string
		changes  int
	}{
		{bl("a", "b", "c"), bl("a", "b2", "c", "d"), []string{"a=a", "b=b2", "c=c", "-=d"}, 2},
		{bl("a", "b"), bl("b"), []string{"a=-", "b=b"}, 1},
		{bl("a"), bl("a"), []string{"a=a"}, 0},
	} {
		var got []string
		n := 0
		for _, p := range pairBlocks(tt.was, tt.now) {
			got = append(got, text(p.was)+"="+text(p.now))
			n += p.changes()
		}
		if !reflect.DeepEqual(got, tt.want) || n != tt.changes {
			t.Errorf("%v → %v: %v, %d changes", tt.was, tt.now, got, n)
		}
	}
}

// Scenarios pair up by name: the delta's in order, a removed one where the main spec has it.
func TestPairScenarios(t *testing.T) {
	sc := func(name string, steps ...string) *scenario {
		s := &scenario{name: name, key: "r\n" + name}
		for _, st := range steps {
			s.steps = append(s.steps, block{kind: blockStep, keyword: "WHEN", text: st})
		}
		return s
	}
	show := func(ps []scenPair) []string {
		var out []string
		for _, p := range ps {
			name := ""
			if p.now != nil {
				name = p.now.name
			} else {
				name = p.was.name
			}
			out = append(out, name+":"+[]string{"same", "changed", "new", "removed"}[p.state])
		}
		return out
	}
	for _, tt := range []struct {
		was, now []*scenario
		want     []string
	}{
		{[]*scenario{sc("A", "x"), sc("B", "y"), sc("C", "z")}, []*scenario{sc("A", "x"), sc("N", "n"), sc("C", "z2")},
			[]string{"A:same", "B:removed", "N:new", "C:changed"}},
		{[]*scenario{sc("R", "r"), sc("A", "a")}, []*scenario{sc("A", "a")}, []string{"R:removed", "A:same"}},
		{[]*scenario{sc("A", "a"), sc("A", "b")}, []*scenario{sc("A", "a")}, []string{"A:same", "A:removed"}},
		{nil, []*scenario{sc("N", "n")}, []string{"N:new"}},
	} {
		if got := show(pairScenarios(tt.was, tt.now, "r")); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("got %v, want %v", got, tt.want)
		}
	}
	ps := pairScenarios([]*scenario{sc("A", "a"), sc("A", "b")}, []*scenario{sc("A", "a")}, "r")
	if ps[0].key != "r\nA" || ps[1].key == ps[0].key || len(ps[1].steps) != 1 || ps[1].steps[0].was == nil {
		t.Errorf("keys %q %q, removed steps %v", ps[0].key, ps[1].key, ps[1].steps)
	}
}

// A delta's path names the main spec it modifies; an archived change's says so. A capability
// named openspec does not move the root.
func TestSpecMainPath(t *testing.T) {
	for path, want := range map[string][2]string{
		"/r/openspec/changes/add-x/specs/auth/login/spec.md":          {"/r/openspec/specs/auth/login/spec.md", ""},
		"/r/openspec/changes/archive/2026-01-02-x/specs/auth/spec.md": {"/r/openspec/specs/auth/spec.md", "archived"},
		"openspec/changes/add-x/specs/auth/spec.md":                   {"openspec/specs/auth/spec.md", ""},
		"/r/openspec/changes/x/specs/tools/openspec/spec.md":          {"/r/openspec/specs/tools/openspec/spec.md", ""},
		"/r/openspec/changes/archive/2026-x/specs/openspec/spec.md":   {"/r/openspec/specs/openspec/spec.md", "archived"},
		"/r/openspec/specs/auth/spec.md":                              {"", ""},
		"/r/docs/spec.md":                                             {"", ""},
	} {
		main, archived := specMain(filepath.FromSlash(path))
		if a := map[bool]string{true: "archived"}[archived]; main != filepath.FromSlash(want[0]) || a != want[1] {
			t.Errorf("%s: %q %v", path, main, archived)
		}
	}
}

// A MODIFIED requirement shows by default what it changed against the main spec: words struck
// out and marked, every scenario tagged, a new one open, a removed one where it was.
func TestSpecDiffView(t *testing.T) {
	reads := 0
	st, o := diffState(diffMain, &reads)
	out := Render(st, o, 120, 40)
	rows := strings.Split(plainText(out), "\n")
	for _, want := range []string{
		"▾ Sessions   was · now · diff  · 4 changes",
		"A session SHALL last an houra day.",
		"Its token is opaque.",
		"▸ Expiry  · 2 steps  · changed",
		"▸ Remember  · 2 steps  · − removed",
		"▾ Refresh  · + new",
		"WHEN  the user comes back",
		"▸ Logout  · 2 steps  · unchanged",
	} {
		if rowWith(rows, want) < 0 {
			t.Errorf("no row with %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	if i := rowWith(rows, "Expiry"); i < 0 || i > rowWith(rows, "Remember") || rowWith(rows, "Remember") > rowWith(rows, "Refresh") {
		t.Error("the removed scenario is not where the main spec has it")
	}
	strike := "9"
	if !styled(out, "an hour", sgr("38", theme.Red), strike) || !styled(out, "a day", sgr("48", theme.AddedBg)) ||
		!styled(out, "Remember", sgr("38", theme.Red), strike) || !styled(out, "diff", sgr("48", theme.SelBg)) {
		t.Errorf("the diff is not marked:\n%q", out)
	}
	if styled(out, "opaque", sgr("48", theme.AddedBg)) || !styled(out, "opaque", sgr("38", theme.Violet)) {
		t.Error("kept code marked, or not violet")
	}
	if reads == 0 {
		t.Error("the main spec was not asked for")
	}
	// the rest of a section: a requirement the main spec lacks says so, with no switch
	o.Specs[specFile] = map[string]bool{"MODIFIED Requirements\nUnknown": true}
	rows = specRender(st, o)
	if i := rowWith(rows, "▾ Unknown"); i < 0 || !strings.Contains(rows[i], "· not in the main spec") || strings.Contains(rows[i], "was ·") {
		t.Errorf("a requirement not in the main spec:\n%s", strings.Join(rows, "\n"))
	}
}

// Added text keeps its colours on the marked background: code violet, SHALL red.
func TestSpecDiffKeepsColours(t *testing.T) {
	reads := 0
	st, o := diffState(strings.Replace(diffMain, "A session SHALL last an hour.", "A session lasts.", 1), &reads)
	out := Render(st, o, 120, 40)
	if !styled(out, "SHALL", sgr("38", theme.Red), sgr("48", theme.AddedBg)) {
		t.Errorf("an added SHALL lost its colour:\n%q", out)
	}
	st.Tabs[2].Buf.SetText(strings.Replace(diffDelta, "Its token is `opaque`.", "Its token is `signed`.", 1))
	if out := Render(st, o, 120, 40); !styled(out, "`signed`"[1:7], sgr("38", theme.Violet), sgr("48", theme.AddedBg)) {
		t.Errorf("added code lost its colour:\n%q", out)
	}
}

// The switch shows the main spec's text, the delta's, or the diff; each mode tags the scenarios
// it shows, and every mode counts the changes.
func TestSpecDiffModes(t *testing.T) {
	reads := 0
	st, o := diffState(diffMain, &reads)
	o.Modes[specFile] = map[string]string{sessions: ModeWas}
	out := Render(st, o, 120, 40)
	rows := strings.Split(plainText(out), "\n")
	for _, want := range []string{"A session SHALL last an hour.", "▸ Expiry  · 2 steps  · changed", "▸ Remember  · 2 steps  · − removed",
		"▸ Logout  · 2 steps  · unchanged", "· 4 changes"} {
		if rowWith(rows, want) < 0 {
			t.Errorf("was: no row with %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	if rowWith(rows, "Refresh") >= 0 || rowWith(rows, "a day") >= 0 || styled(out, "Remember", "9") || !styled(out, "was", sgr("48", theme.SelBg)) {
		t.Errorf("was shows the delta:\n%s", strings.Join(rows, "\n"))
	}
	o.Modes[specFile][sessions] = ModeNow
	out = Render(st, o, 120, 40)
	rows = strings.Split(plainText(out), "\n")
	for _, want := range []string{"A session SHALL last a day.", "▸ Refresh  · 2 steps  · + new", "▸ Expiry  · 2 steps  · changed", "· 4 changes"} {
		if rowWith(rows, want) < 0 {
			t.Errorf("now: no row with %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	if rowWith(rows, "Remember") >= 0 || rowWith(rows, "an hour") >= 0 || styled(out, "a day", sgr("48", theme.AddedBg)) {
		t.Errorf("now shows the main spec or marks:\n%s", strings.Join(rows, "\n"))
	}
	// diff: a changed scenario opened shows its steps diffed; a removed one, its old steps struck
	o.Modes[specFile][sessions] = ModeDiff
	o.Specs[specFile] = map[string]bool{sessions + "\nExpiry": true, sessions + "\nRemember\x00removed": true}
	out = Render(st, o, 120, 40)
	if !styled(out, "an hour", sgr("38", theme.Red), "9") || !styled(out, "a day", sgr("48", theme.AddedBg)) ||
		!styled(out, "the user ticks remember me", sgr("38", theme.Red), "9") {
		t.Errorf("opened scenarios are not diffed:\n%s", plainText(out))
	}
	// a new scenario folds as any other
	o.Specs[specFile][sessions+"\nRefresh"] = false
	if rows := specRender(st, o); rowWith(rows, "▸ Refresh  · 2 steps  · + new") < 0 {
		t.Errorf("a new scenario does not fold:\n%s", strings.Join(rows, "\n"))
	}
}

// A click on a word of the switch picks the mode; the rest of the header folds; a removed
// scenario folds as the others.
func TestSpecDiffClick(t *testing.T) {
	reads := 0
	st, o := diffState(diffMain, &reads)
	x, y := specRowAt(t, st, o, "was ·")
	for dx, mode := range map[int]string{0: ModeWas, 2: ModeWas, 6: ModeNow, 12: ModeDiff} {
		h, ok := SpecAt(st, o, 120, 40, x+dx, y)
		if !ok || h.Mode != mode || !reflect.DeepEqual(h.Folds, []string{sessions}) {
			t.Errorf("+%d: %+v", dx, h)
		}
	}
	if h, ok := SpecAt(st, o, 120, 40, x+3, y); !ok || h.Mode != "" { // the dot between: a fold
		t.Errorf("between the words: %+v", h)
	}
	h, _ := SpecAt(st, o, 120, 40, x, y)
	o.Modes.Set(h)
	o.Specs.Fold(h)
	if o.Modes[specFile][sessions] != ModeWas || len(o.Specs[specFile]) != 0 {
		t.Errorf("modes %v, folds %v", o.Modes, o.Specs)
	}
	nx, ny := specRowAt(t, st, o, "▾ Sessions")
	if h, ok := SpecAt(st, o, 120, 40, nx+4, ny); !ok || h.Mode != "" || h.Open || h.Folds[0] != sessions {
		t.Errorf("the name: %+v", h)
	}
	o.Modes[specFile][sessions] = ModeDiff
	rx, ry := specRowAt(t, st, o, "Remember")
	if h, ok := SpecAt(st, o, 120, 40, rx, ry); !ok || !h.Open || h.Folds[0] != sessions+"\nRemember\x00removed" {
		t.Errorf("the removed scenario: %+v", h)
	}
	if h, ok := SpecAt(st, o, 120, 40, 3, 2); ok && h.Mode != "" { // the outline picks no mode
		t.Errorf("the outline: %+v", h)
	}
}

// No main spec, or none of the name: drawn as before, said so, no switch.
func TestSpecDiffNoMain(t *testing.T) {
	for name, main := range map[string]string{"no file": "", "no requirement": strings.Replace(diffMain, "Sessions", "Sittings", 1)} {
		reads := 0
		st, o := diffState(main, &reads)
		rows := specRender(st, o)
		if i := rowWith(rows, "▾ Sessions"); i < 0 || !strings.HasSuffix(strings.TrimRight(strings.TrimSuffix(strings.TrimRight(rows[i], " "), "│"), " "), "· not in the main spec") ||
			rowWith(rows, "was ·") >= 0 || rowWith(rows, "unchanged") >= 0 || rowWith(rows, "A session SHALL last a day.") < 0 {
			t.Errorf("%s:\n%s", name, strings.Join(rows, "\n"))
		}
	}
}

// An archived change is in the main spec already: no diff, no read, said so.
func TestSpecDiffArchive(t *testing.T) {
	reads := 0
	st, o := diffState(diffMain, &reads)
	st.Tabs[2].Path = root + "/openspec/changes/archive/2026-01-02-add-login/specs/auth/login/spec.md"
	st.Agents[0].File = st.Tabs[2].Path
	rows := specRender(st, o)
	if i := rowWith(rows, "▾ Sessions"); i < 0 || !strings.Contains(rows[i], "· archived") || rowWith(rows, "was ·") >= 0 || reads != 0 {
		t.Errorf("reads %d:\n%s", reads, strings.Join(rows, "\n"))
	}
}

// A main spec open in a tab is compared as the tab holds it, not as read.
func TestSpecDiffTab(t *testing.T) {
	reads := 0
	st, o := diffState(diffMain, &reads)
	st.Tabs = append(st.Tabs, &ide.Tab{Path: diffMainFile, Title: "spec.md", Buf: ide.Buffer{
		Lines: strings.Split(strings.Replace(diffMain, "last an hour", "last a day", 1), "\n")}})
	rows := specRender(st, o)
	if rowWith(rows, "· 3 changes") < 0 || reads != 0 {
		t.Errorf("reads %d:\n%s", reads, strings.Join(rows, "\n"))
	}
}

// The layout is kept while nothing it is drawn from changes, and laid out again when the main
// spec, a mode or the folds change.
func TestSpecDiffCache(t *testing.T) {
	reads := 0
	main := diffMain
	st, o := diffState(main, &reads)
	o.Main = func(string) []string { reads++; return strings.Split(main, "\n") }
	specRender(st, o)
	l := o.SpecRows[specFile]
	if specRender(st, o); o.SpecRows[specFile] != l || len(l.rows) == 0 {
		t.Fatal("the layout was not kept")
	}
	main = strings.Replace(diffMain, "last an hour", "last a day", 1) // the main spec edited
	if out := specRender(st, o); rowWith(out, "· 3 changes") < 0 || o.SpecRows[specFile] == l {
		t.Errorf("a main spec's edit kept the old diff:\n%s", strings.Join(out, "\n"))
	}
	l = o.SpecRows[specFile]
	o.Modes[specFile] = map[string]string{sessions: ModeWas}
	if out := specRender(st, o); rowWith(out, "A session SHALL last a day.") < 0 || rowWith(out, "Refresh") >= 0 || o.SpecRows[specFile].doc != l.doc {
		t.Errorf("a mode change kept the old rows, or parsed again:\n%s", strings.Join(out, "\n"))
	}
}

// A marked run ends with its text: the background does not run on into the row's end.
func TestSpecDiffMarkEnds(t *testing.T) {
	b := &specLines{w: 12, at: noRow}
	b.add("", "", marked([]span{{text: "aaaa bbbb cccc dddd", st: specTextStyle}})...)
	b.add("", "", struck([]span{{text: "eeee ffff gggg", st: specTextStyle}})...)
	for _, r := range b.rows {
		if last := r.spans[len(r.spans)-1].text; strings.HasSuffix(last, " ") {
			t.Errorf("row ends with %q", last)
		}
	}
	if len(b.rows) < 4 {
		t.Errorf("%d rows", len(b.rows))
	}
}

// Every frame of a diffed spec is exactly the window, in every mode, whatever its size and text.
func TestSpecDiffRenderSize(t *testing.T) {
	cjk := strings.NewReplacer("session", "会话", "hour", "小時🎉", "day", "日👨‍👩‍👧", "user", "用户").Replace
	for name, mode := range map[string]string{"was": ModeWas, "now": ModeNow, "diff": ModeDiff} {
		for _, text := range []func(string) string{func(s string) string { return s }, cjk} {
			reads := 0
			st, o := diffState(text(diffMain), &reads)
			st.Tabs[2].Buf.SetText(text(diffDelta))
			o.Modes[specFile] = map[string]string{sessions: mode}
			o.Specs[specFile] = map[string]bool{sessions + "\nExpiry": true, sessions + "\nRemember\x00removed": true, "MODIFIED Requirements\nUnknown": true}
			for w := 0; w <= 70; w++ {
				for _, h := range []int{0, 1, 2, 4, 9, 16} {
					checkSize(t, name, Render(st, o, w, h), w, h)
				}
			}
			for _, s := range [][2]int{{80, 24}, {120, 40}, {170, 52}} {
				checkSize(t, name, Render(st, o, s[0], s[1]), s[0], s[1])
			}
		}
	}
}

// A step whose keyword changed, or a block whose kind did, is a change though its text is the same:
// it is one change, and the scenario is changed.
func TestKeywordChange(t *testing.T) {
	step := func(kw, text string) block { return block{kind: blockStep, keyword: kw, text: text} }
	for name, tt := range map[string]struct{ was, now block }{
		"keyword": {step("THEN", "it ends"), step("AND", "it ends")},
		"kind":    {block{kind: blockText, text: "it ends"}, block{kind: blockQuote, text: "it ends"}},
		"both":    {step("THEN", "it ends"), step("AND", "it ends soon")},
	} {
		ps := pairBlocks([]block{tt.was}, []block{tt.now})
		if len(ps) != 1 || ps[0].was == nil || ps[0].now == nil || ps[0].changes() != 1 || !ps[0].whole() {
			t.Errorf("%s: %+v", name, ps)
		}
	}
	sc := func(kw string) *scenario {
		return &scenario{name: "S", key: "r\nS", steps: []block{step("WHEN", "a"), step(kw, "it ends")}}
	}
	if ps := pairScenarios([]*scenario{sc("THEN")}, []*scenario{sc("AND")}, "r"); ps[0].state != scenChanged {
		t.Errorf("state %d", ps[0].state)
	}
	if ps := pairScenarios([]*scenario{sc("THEN")}, []*scenario{sc("THEN")}, "r"); ps[0].state != scenSame {
		t.Errorf("state %d", ps[0].state)
	}
}

// A changed keyword shows in the view: the header counts it, the scenario says changed, and the
// old step is struck out above the new one, marked.
func TestSpecDiffKeywordView(t *testing.T) {
	reads := 0
	st, o := diffState(diffMain, &reads)
	st.Tabs[2].Buf.SetText(strings.Replace(diffDelta, "- **THEN** the session ends\n\n#### Scenario: Refresh", "- **AND** the session ends\n\n#### Scenario: Refresh", 1))
	o.Specs[specFile] = map[string]bool{sessions + "\nExpiry": true}
	out := Render(st, o, 120, 40)
	rows := strings.Split(plainText(out), "\n")
	i, j := rowWith(rows, "THEN  the session ends"), rowWith(rows, "AND  the session ends")
	if i < 0 || j != i+1 || rowWith(rows, "· 4 changes") < 0 {
		t.Errorf("the old step is not above the new, or the count misses it:\n%s", strings.Join(rows, "\n"))
	}
	if !styled(out, "the session ends", sgr("38", theme.Red), "9") || !styled(out, "the session ends", sgr("48", theme.AddedBg)) {
		t.Errorf("the steps are not struck and marked:\n%q", out)
	}
}

// A line of code is told apart whole; a pair of sequences too large to compare is too.
func TestDiffLargeAndCode(t *testing.T) {
	code := pairBlocks([]block{{kind: blockCode, text: "a b c"}}, []block{{kind: blockCode, text: "a x c"}})
	if len(code) != 1 || !reflect.DeepEqual(code[0].hunks, []hunk{{del: "a b c", ins: "a x c"}}) || code[0].changes() != 1 {
		t.Errorf("code: %+v", code)
	}
	a, b := strings.Repeat("a ", 10000), strings.Repeat("b ", 10000) // 20000 tokens each
	if got := wordDiff(a, b); !reflect.DeepEqual(got, []hunk{{del: a, ins: b}}) {
		t.Errorf("a large pair: %d hunks", len(got))
	}
	ops := lcs(2000, 2000, func(i, j int) bool { return i == j })
	if len(ops) != 4000 || ops[0] != '-' || ops[2000] != '+' {
		t.Errorf("a large lcs: %d ops", len(ops))
	}
}

// A change of ** markers alone is no change and leaves nothing marked.
func TestMarkupIsNoChange(t *testing.T) {
	for _, tt := range []struct{ a, b string }{
		{"banner", "**banner**"}, {"**banner** here", "banner here"}, {"a  b", "a b"},
	} {
		ps := pairBlocks([]block{{text: tt.a}}, []block{{text: tt.b}})
		if len(ps) != 1 || ps[0].changes() != 0 {
			t.Errorf("%q → %q: %+v", tt.a, tt.b, ps)
			continue
		}
		drawn := ""
		for _, sp := range ps[0].diffSpans(specTextStyle) {
			if sp.mark {
				t.Errorf("%q → %q: marked %q", tt.a, tt.b, sp.text)
			}
			drawn += sp.text
		}
		if want := strings.ReplaceAll(tt.b, "**", ""); drawn != want {
			t.Errorf("%q → %q: draws %q", tt.a, tt.b, drawn)
		}
	}
	if got := wordDiff("**old** word", "**new** word"); !reflect.DeepEqual(got, []hunk{{eq: "**"}, {del: "old", ins: "new"}, {eq: "** word"}}) {
		t.Errorf("a real change: %q", got)
	}
}

// The header of a folded requirement counts the scenarios its mode lists: the main spec's in was,
// the delta's in now, all in diff.
func TestFoldedScenarioCount(t *testing.T) {
	reads := 0
	st, o := diffState(diffMain, &reads)
	st.Tabs[2].Buf.SetText(strings.Replace(diffDelta, "### Requirement: Unknown", "#### Scenario: Extra\n- **WHEN** x\n- **THEN** y\n\n### Requirement: Unknown", 1))
	o.Specs[specFile] = map[string]bool{sessions: false}
	for mode, want := range map[string]string{ModeWas: "▸ Sessions  · 3 scenarios", ModeNow: "▸ Sessions  · 4 scenarios", ModeDiff: "▸ Sessions  · 5 scenarios"} {
		o.Modes[specFile] = map[string]string{sessions: mode}
		if rows := specRender(st, o); rowWith(rows, want) < 0 {
			t.Errorf("%s: no row with %q:\n%s", mode, want, strings.Join(rows, "\n"))
		}
	}
}

// A tag of a header does not break across rows when the header wraps: it moves to the next row whole.
func TestTagsStayWhole(t *testing.T) {
	for n := range 40 {
		name := "Sessions" + strings.Repeat("x", n)
		reads := 0
		st, o := diffState(strings.Replace(diffMain, "Sessions", name, 1), &reads)
		st.Tabs[2].Buf.SetText(strings.Replace(diffDelta, "Sessions", name, 1))
		rows := strings.Split(plainText(Render(st, o, 80, 40)), "\n")
		if rowWith(rows, "Sessions") < 0 {
			t.Fatalf("no header:\n%s", strings.Join(rows, "\n"))
		}
		if rowWith(rows, "4 changes") < 0 {
			t.Errorf("%d: the tag broke:\n%s", n, strings.Join(rows, "\n"))
		}
		if rowWith(rows, "was · now · diff") < 0 {
			t.Errorf("%d: the switch broke:\n%s", n, strings.Join(rows, "\n"))
		}
	}
}
