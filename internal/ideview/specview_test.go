package ideview

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/theme"
)

const specFile = root + "/openspec/changes/add-login/specs/auth/login/spec.md"

// specState is sampleState with main reading specText, the explorer on its file.
func specState() *ide.State {
	st := sampleState()
	st.Tabs = append(st.Tabs, &ide.Tab{Path: specFile, Title: "spec.md", Buf: ide.Buffer{
		Lines:  strings.Split(specText, "\n"),
		Carets: map[string]*ide.Caret{"main": {Pos: ide.Pos{Line: 3}}},
	}})
	st.Agents[0].File, st.Agents[0].TreeSel = specFile, ""
	st.Agents[0].Term = ide.Term{} // the editor alone
	return st
}

// plainText is s with its styles stripped and its no-break spaces, which keep a tag whole when a row
// wraps, as blanks.
func plainText(s string) string { return strings.ReplaceAll(ansi.Strip(s), "\u00a0", " ") }

// specRender is the plain rows of the view of st with o, 120×40.
func specRender(st *ide.State, o Options) []string {
	return strings.Split(plainText(Render(st, o, 120, 40)), "\n")
}

// rowWith is the index of the first row of rows that contains s; -1 for none.
func rowWith(rows []string, s string) int {
	return slices.IndexFunc(rows, func(r string) bool { return strings.Contains(r, s) })
}

// A spec shows structured: the capability, a badge and a count per section, the requirements
// behind the bar of their section, the first of each open, the scenarios folded.
func TestSpecView(t *testing.T) {
	st := specState()
	out := Render(st, Options{Now: now}, 120, 40)
	rows := strings.Split(plainText(out), "\n")
	for _, want := range []string{
		"│ auth/login  ·  delta",
		" ADDED   2 requirements",
		"▌ ▾ Login",
		"▌   The system SHALL let a user log in with email and password. It MUST NOT",
		"▌   ▸ Good password  · 2 steps",
		"▌   ▸ Bad password  · 2 steps",
		"▌ ▸ Logout  · 1 scenario",
		" MODIFIED   1 requirement",
		"▌ ▾ Sessions",
		" REMOVED   1 requirement",
		"▌   Reason: nobody used it",
		"▌   Migration: none",
		" RENAMED   0 requirements",
		"- FROM: ### Requirement: Sign in",
	} {
		if rowWith(rows, want) < 0 {
			t.Errorf("no row with %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	if strings.Contains(ansi.Strip(out), "**") || strings.Contains(ansi.Strip(out), "Requirement: Login") && rowWith(rows, "TO: ### Requirement: Login") < 0 {
		t.Error("markup shows")
	}
	if !styled(out, "SHALL", sgr("38", theme.Red), "1") || !styled(out, "MUST NOT", sgr("38", theme.Red), "1") {
		t.Error("SHALL or MUST NOT is not red and bold")
	}
	if !styled(out, "email", sgr("38", theme.Violet)) || !styled(out, "auth/login", sgr("38", theme.Accent), "1") {
		t.Error("code is not violet, or the capability not in the accent")
	}
	if !styled(out, " ADDED ", sgr("48", theme.Green), sgr("38", theme.Bg), "1") || !styled(out, " MODIFIED ", sgr("48", theme.Yellow)) ||
		!styled(out, " REMOVED ", sgr("48", theme.Red)) || !styled(out, " RENAMED ", sgr("48", theme.Blue)) {
		t.Error("a badge is not in its section's colour")
	}
	if l := lineWith(out, "Logout  ·"); !styled(l, "▌", sgr("38", theme.Green)) {
		t.Errorf("no green bar: %q", l)
	}
	if l := lineWith(out, "▾ Sessions"); !styled(l, "▌", sgr("38", theme.Yellow)) {
		t.Errorf("no yellow bar: %q", l)
	}
	if rowWith(rows, " 1 ## ADDED") >= 0 || rowWith(rows, "WHEN") >= 0 {
		t.Error("the file shows as written, or a folded scenario's steps")
	}
}

// A main spec has its sections' titles for badges, in grey, and is no delta.
func TestSpecMain(t *testing.T) {
	st := specState()
	st.Tabs[2].Path = root + "/openspec/specs/auth/login/spec.md"
	st.Agents[0].File = st.Tabs[2].Path
	st.Tabs[2].Buf.SetText("# auth Specification\n\n## Purpose\nLogs users in.\n\n## Requirements\n\n### Requirement: Login\nIt SHALL.")
	out := Render(st, Options{Now: now}, 120, 40)
	rows := strings.Split(ansi.Strip(out), "\n")
	for _, want := range []string{"│ auth/login ", "auth Specification", " PURPOSE  ", "Logs users in.", " REQUIREMENTS   1 requirement", "▌ ▾ Login"} {
		if rowWith(rows, want) < 0 {
			t.Errorf("no row with %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	if rowWith(rows, "delta") >= 0 || rowWith(rows, "PURPOSE   0") >= 0 || !styled(out, " PURPOSE ", sgr("48", theme.Dim)) {
		t.Error("a main spec is a delta, its purpose counts requirements, or its badge is not grey")
	}
}

// An open scenario shows its steps: the keyword right-aligned in a column, the text after it with
// a hanging indent; a note after a bar.
func TestSpecSteps(t *testing.T) {
	st := specState()
	open := "ADDED Requirements\nLogin\nGood password"
	o := Options{Now: now, Specs: Specs{specFile: {open: true}}}
	rows := specRender(st, o)
	for _, want := range []string{
		"▌   ▾ Good password",
		"▌      WHEN  a user logs in with the right password",
		"▌      THEN  the user is in",
		"▌          │ NOTE: checked by TestLogin.",
	} {
		if rowWith(rows, want) < 0 {
			t.Errorf("no row with %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	out := Render(st, o, 120, 40)
	if !styled(lineWith(out, "THEN"), " THEN", sgr("38", theme.Yellow)) {
		t.Errorf("keyword not yellow: %q", lineWith(out, "THEN"))
	}
	// Narrow, a step's text wraps under itself, after the keyword's column.
	narrow := strings.Split(ansi.Strip(Render(st, o, 60, 40)), "\n")
	i := rowWith(narrow, "WHEN  a user logs")
	if i < 0 || !strings.HasPrefix(strings.TrimLeft(narrow[i+1], "│ "), "▌            ") || strings.Contains(narrow[i+1], "WHEN") {
		t.Errorf("no hanging indent:\n%s", strings.Join(narrow, "\n"))
	}
}

// What is not a spec, or holds no requirement, or is shown as written, stays as written.
func TestSpecFallsBack(t *testing.T) {
	raw := func(st *ide.State, o Options) bool {
		return rowWith(specRender(st, o), " 1 ## ADDED Requirements") >= 0
	}
	if st := specState(); raw(st, Options{Now: now}) || !raw(st, Options{Now: now, Raw: true}) {
		t.Error("structured and as written do not switch with Raw")
	}
	st := specState()
	st.Tabs[2].Path = root + "/docs/spec.md"
	st.Agents[0].File = st.Tabs[2].Path
	if !raw(st, Options{Now: now}) {
		t.Error("a spec.md outside openspec/ is structured")
	}
	st = specState()
	st.Tabs[2].Hex = true
	if !raw(st, Options{Now: now}) {
		t.Error("a hex dump is structured")
	}
	for _, text := range []string{"", "## ADDED Requirements\n\nNothing yet.", "## RENAMED Requirements\n- FROM: `### Requirement: A`\n- TO: `### Requirement: B`"} {
		st := specState()
		st.Tabs[2].Buf.SetText(text)
		if rows := specRender(st, Options{Now: now}); rowWith(rows, " 1 ") < 0 {
			t.Errorf("%q: not as written:\n%s", text, strings.Join(rows, "\n"))
		}
	}
}

// While an agent edits the spec, it shows as written, so that the edit shows; as soon as the
// agent is done, structured again.
func TestSpecWhileEditing(t *testing.T) {
	raw := func(st *ide.State) bool {
		return rowWith(specRender(st, Options{Now: now, Busy: true}), " 1 ## ADDED Requirements") >= 0
	}
	st := specState()
	st.Tabs[2].Buf.Modified = true
	if !raw(st) {
		t.Error("an edit waiting for its result shows structured")
	}
	st = specState()
	st.Agents[0].TypedAt = now.Add(-100 * time.Millisecond)
	if !raw(st) {
		t.Error("an agent typing in it: structured")
	}
	st.Agents[0].TypedAt = now.Add(-time.Second)
	if raw(st) {
		t.Error("an agent that typed a while ago: as written")
	}
	st.Agents[0].TypedAt, st.Agents[0].Focus, st.Agents[0].Term.Input = now.Add(-100*time.Millisecond), ide.Terminal, "go te"
	if raw(st) || !strings.Contains(strings.Join(leftRows(Render(st, Options{Now: now, Busy: true}, 120, 40), 120), "\n"), "Logout") {
		t.Error("an agent typing a command in its terminal: as written, or no outline")
	}
	st.Agents[0].Focus = ide.Browser
	if raw(st) {
		t.Error("an agent typing an address in its browser: as written")
	}
	st.Agents[0].Focus, st.Agents[0].File = ide.Editor, root+"/main.go"
	if raw(st) {
		t.Error("an agent typing in another file: as written")
	}
	st.Agents[0].File = specFile
	st.Agents[0].Focus, st.Agents[0].TypedAt = ide.Editor, now.Add(-time.Second)
	st.Tabs[2].Buf.Carets["main"].Sel = &ide.Selection{To: ide.Pos{Line: 1}}
	if !raw(st) {
		t.Error("an agent selecting what it replaces: structured")
	}
	st.Agents = append(st.Agents, &ide.Agent{Key: "sub", Kind: ide.Subagent, Status: ide.Done})
	st.Tabs[2].Buf.Carets = map[string]*ide.Caret{"main": {}, "sub": {Sel: &ide.Selection{To: ide.Pos{Line: 1}}}}
	if raw(st) {
		t.Error("a finished agent's selection keeps the spec as written")
	}
}

// specRowAt is the cell of the first row of the view of st with o, 120×40, that contains s.
func specRowAt(t *testing.T, st *ide.State, o Options, s string) (x, y int) {
	t.Helper()
	rows := specRender(st, o)
	y = rowWith(rows, s)
	if y < 0 {
		t.Fatalf("no row with %q:\n%s", s, strings.Join(rows, "\n"))
	}
	return ansi.StringWidth(rows[y][:strings.Index(rows[y], s)]), y
}

// A click on a requirement or a scenario opens or folds it; on a section's badge, every
// requirement of the section. Folds are kept by name: an edit above them keeps them.
func TestSpecFold(t *testing.T) {
	st := specState()
	o := Options{Now: now, Specs: Specs{}}
	click := func(s string) {
		t.Helper()
		x, y := specRowAt(t, st, o, s)
		h, ok := SpecAt(st, o, 120, 40, x+2, y)
		if !ok {
			t.Fatalf("a click on %q does nothing", s)
		}
		o.Specs.Fold(h)
	}
	// Opened after a folded one, a requirement takes a blank row above it; a second click where the
	// first was folds it back.
	click("▾ Login")
	x, y := specRowAt(t, st, o, "▸ Logout")
	for i, want := range []string{"▾ Logout", "▸ Logout"} {
		h, ok := SpecAt(st, o, 120, 40, x+2, y)
		if !ok {
			t.Fatalf("click %d at the same place does nothing", i+1)
		}
		o.Specs.Fold(h)
		if rows := specRender(st, o); rowWith(rows, want) < 0 {
			t.Fatalf("click %d: no %q:\n%s", i+1, want, strings.Join(rows, "\n"))
		}
	}
	click("▸ Login")
	click("▸ Logout")
	if rows := specRender(st, o); rowWith(rows, "▾ Logout") < 0 || rowWith(rows, "▸ Logout  · 2 steps") < 0 {
		t.Fatalf("Logout did not open:\n%s", strings.Join(rows, "\n"))
	}
	click("▸ Logout  · 2 steps")
	if rows := specRender(st, o); rowWith(rows, "WHEN  the user logs out") < 0 {
		t.Fatalf("the scenario did not open:\n%s", strings.Join(rows, "\n"))
	}
	click("▾ Login")
	if rows := specRender(st, o); rowWith(rows, "▸ Login  · 2 scenarios") < 0 {
		t.Fatal("Login did not fold")
	}
	// Some folded: the badge opens them all; all open: it folds them all.
	click(" ADDED ")
	if rows := specRender(st, o); rowWith(rows, "▾ Login") < 0 || rowWith(rows, "▾ Logout") < 0 {
		t.Fatal("the section did not open")
	}
	click(" ADDED ")
	if rows := specRender(st, o); rowWith(rows, "▸ Login") < 0 || rowWith(rows, "▸ Logout") < 0 || rowWith(rows, "▾ Sessions") < 0 {
		t.Fatal("the section did not fold, or folded another")
	}
	click(" ADDED ")
	// An edit above keeps the folds.
	st.Tabs[2].Buf.SetText("## ADDED Requirements\n\n### Requirement: First\nNew.\n" + strings.TrimPrefix(specText, "## ADDED Requirements\n"))
	if rows := specRender(st, o); rowWith(rows, "▾ Logout") < 0 || rowWith(rows, "WHEN  the user logs out") < 0 || rowWith(rows, "▾ First") < 0 { // new and first: open
		t.Fatalf("the folds moved with the edit:\n%s", strings.Join(rows, "\n"))
	}
	// Text, a blank row, the frame and the gutter of air take no click.
	for _, s := range []string{"The system SHALL let a user log out", "auth/login"} {
		x, y := specRowAt(t, st, o, s)
		if _, ok := SpecAt(st, o, 120, 40, x, y); ok {
			t.Errorf("a click on %q folds", s)
		}
	}
	x, y = specRowAt(t, st, o, "▾ Logout")
	for _, at := range [][2]int{{119, y}, {x, 0}, {x, 39}, {x, -1}, {-1, y}} {
		if _, ok := SpecAt(st, o, 120, 40, at[0], at[1]); ok {
			t.Errorf("a click at %v folds", at)
		}
	}
	if _, ok := SpecAt(specState(), Options{Now: now, Raw: true}, 120, 40, x, y); ok {
		t.Error("a click on the spec as written folds")
	}
}

// The wheel scrolls the spec from its top, as far as it goes; the scrollbar is of the same rows.
// The spec keeps its scroll while an agent edits it, and the file as written its own.
func TestSpecScroll(t *testing.T) {
	st := specState()
	all := Specs{specFile: {}}
	for _, s := range specOf(specText).sections {
		for _, r := range s.reqs {
			all[specFile][r.key] = true
			for _, sc := range r.scenarios {
				all[specFile][sc.key] = true
			}
		}
	}
	o := Options{Now: now, Specs: all, Scroll: Scroll{}}
	key := specKey(specFile)
	_, last := Wheel(st, o, 120, 20, 60, 10, -1000)
	if last >= 0 {
		t.Fatalf("scrolled to %d", last)
	}
	if k, n := Wheel(st, o, 120, 20, 60, 10, 5); k != key || n != 0 {
		t.Errorf("up from the top: %q %d", k, n)
	}
	o.Scroll[key] = -4
	if rows := strings.Split(ansi.Strip(Render(st, o, 120, 20)), "\n"); !strings.Contains(rows[1], "▾ Login") {
		t.Errorf("scrolled 4: %q", rows[1])
	}
	o.Scroll[key] = last
	rows := strings.Split(ansi.Strip(Render(st, o, 120, 20)), "\n")
	if !strings.Contains(rows[18], "TO: ### Requirement: Login") {
		t.Errorf("at the end: %q", rows[18])
	}
	i := slices.IndexFunc(Scrollbars(st, o, 120, 20), func(b Scrollbar) bool { return b.Key == key })
	if i < 0 {
		t.Fatal("no scrollbar")
	}
	if b := Scrollbars(st, o, 120, 20)[i]; b.Scroll(b.Y+b.Height, 0) != last || b.Scroll(b.Y, 0) != 0 {
		t.Errorf("the scrollbar scrolls to %d and %d", b.Scroll(b.Y+b.Height, 0), b.Scroll(b.Y, 0))
	}
	o.Scroll[fileKey(specFile)] = 2
	st.Tabs[2].Buf.Modified = true
	Prune(st, o, 120, 20)
	if o.Scroll[key] != last || o.Scroll[fileKey(specFile)] != 2 {
		t.Errorf("scrolls lost while editing: %v", o.Scroll)
	}
	st.Agents[0].File = root + "/src/main.go"
	Prune(st, o, 120, 20)
	if _, ok := o.Scroll[key]; ok {
		t.Error("the scroll of a spec off screen stays")
	}
}

// The structured rows select as the text of a terminal does: inside the frame, after a cell of air.
func TestSpecTextAt(t *testing.T) {
	st := specState()
	o := Options{Now: now}
	rows := specRender(st, o)
	r := TextAt(st, o, 120, 40, 60, 5)
	if got := ansi.Cut(rows[1], r.Min.X, r.Max.X); !strings.HasPrefix(got, "auth/login") || r.Max.X != 118 {
		t.Errorf("text %v: %q", r, got)
	}
}

// Instead of the files, the left column outlines the spec: the sections, a row per requirement
// with the count of its scenarios, the requirement at the top of the editor lit; a click opens
// the requirement and brings it to the top of the editor.
func TestSpecOutline(t *testing.T) {
	st := specState()
	o := Options{Now: now, Specs: Specs{}, Scroll: Scroll{}}
	out := Render(st, o, 120, 40)
	l := leftRows(out, 120)
	for i, want := range []string{"ADDED", "▌ Login", "▌ Logout", "", "MODIFIED", "▌ Sessions", "", "REMOVED", "▌ Remember me", ""} {
		if !strings.HasPrefix(l[i], want) {
			t.Errorf("outline row %d: %q, want %q", i, l[i], want)
		}
	}
	if !strings.HasSuffix(l[1], " 2") || !strings.HasSuffix(l[8], " 0") {
		t.Errorf("counts: %q %q", l[1], l[8])
	}
	if top := strings.Split(ansi.Strip(left(out, 120)), "\n")[0]; !strings.Contains(top, " outline · files ") {
		t.Errorf("title: %q", top)
	}
	if top := strings.Split(ansi.Strip(left(Render(st, o, 170, 40), 170)), "\n")[0]; !strings.Contains(top, " 4 requirements · files ") {
		t.Errorf("wide title: %q", top)
	}
	if !strings.Contains(lineWith(left(out, 120), "Login"), sgr("48", theme.SelBg)) || strings.Contains(lineWith(left(out, 120), "Logout"), sgr("48", theme.SelBg)) {
		t.Error("Login is not the one lit")
	}
	if path, _ := TreeAt(st, o, 120, 40, 3, 2); path != "" {
		t.Errorf("a click in the outline opens %q", path)
	}
	// A click on Logout opens it and brings it to the top.
	h, ok := SpecAt(st, o, 120, 16, 3, 3)
	if !ok || h.Key != specKey(specFile) || !h.Open {
		t.Fatalf("outline click: %+v %v", h, ok)
	}
	o.Specs.Fold(h)
	o.Scroll[h.Key] = h.Back
	out = Render(st, o, 120, 16)
	if rows := strings.Split(ansi.Strip(out), "\n"); !strings.Contains(rows[1], "▾ Logout") || !strings.Contains(lineWith(left(out, 120), "Logout"), sgr("48", theme.SelBg)) {
		t.Errorf("Logout is not on top, or not lit:\n%s", strings.Join(rows, "\n"))
	}
	for _, at := range [][2]int{{3, 1}, {3, 4}, {0, 2}, {3, 0}} { // a section, a blank, the frame, the top
		if _, ok := SpecAt(st, o, 120, 40, at[0], at[1]); ok {
			t.Errorf("a click at %v in the outline does something", at)
		}
	}
	// The agents panel, and a spec shown as written, leave the outline out.
	if l := leftRows(Render(st, Options{Now: now, Panel: Agents}, 120, 40), 120); strings.Contains(strings.Join(l, "\n"), "Logout") {
		t.Error("the agents panel outlines")
	}
	if l := leftRows(Render(st, Options{Now: now, Raw: true}, 120, 40), 120); !strings.Contains(strings.Join(l, "\n"), "src") {
		t.Error("no files beside the spec as written")
	}
}

// A long outline scrolls with the wheel, and follows the requirement on top of the editor.
func TestSpecOutlineScroll(t *testing.T) {
	st := specState()
	var b strings.Builder
	b.WriteString("## ADDED Requirements\n")
	for i := range 40 {
		b.WriteString("\n### Requirement: R" + string(rune('A'+i%26)) + string(rune('a'+i/26)) + "\nIt SHALL.\n")
	}
	st.Tabs[2].Buf.SetText(b.String())
	o := Options{Now: now, Scroll: Scroll{}}
	if k, n := Wheel(st, o, 120, 20, 5, 5, -3); k != outlineKey(specFile) || n != -3 {
		t.Errorf("wheel: %q %d", k, n)
	}
	if i := slices.IndexFunc(Scrollbars(st, o, 120, 20), func(b Scrollbar) bool { return b.Key == outlineKey(specFile) }); i < 0 {
		t.Error("no scrollbar on the outline")
	}
	o.Scroll[specKey(specFile)] = -60 // further down than the outline shows from its top
	if l := leftRows(Render(st, o, 120, 20), 120); !slices.ContainsFunc(l, func(r string) bool { return strings.Contains(r, "▌ R") }) ||
		strings.Contains(l[0], "ADDED") {
		t.Errorf("the outline did not follow:\n%s", strings.Join(l, "\n"))
	}
	delete(o.Scroll, specKey(specFile))
	o.Scroll[outlineKey(specFile)] = -3
	if l := leftRows(Render(st, o, 120, 20), 120); strings.Contains(l[0], "ADDED") {
		t.Errorf("the outline did not scroll:\n%s", strings.Join(l, "\n"))
	}
	Prune(st, o, 120, 20)
	if _, ok := o.Scroll[outlineKey(specFile)]; !ok {
		t.Error("pruned the outline's scroll")
	}
	other := root + "/openspec/specs/auth/login/spec.md"
	st.Tabs = append(st.Tabs, &ide.Tab{Path: other, Title: "spec.md", Buf: ide.Buffer{Lines: st.Tabs[2].Buf.Lines}})
	st.Agents[0].File = other
	if l := leftRows(Render(st, o, 120, 20), 120); !strings.Contains(l[0], "ADDED") {
		t.Errorf("another spec's outline kept the scroll:\n%s", strings.Join(l, "\n"))
	}
	st.Agents[0].File = specFile
	o.Raw = true
	Prune(st, o, 120, 20)
	if _, ok := o.Scroll[outlineKey(specFile)]; ok {
		t.Error("kept the scroll of an outline off screen")
	}
}

// Every row keeps its pane's width, whatever the size and the text.
func TestSpecRenderSize(t *testing.T) {
	cjk := strings.NewReplacer("user", "用户", "password", "密码", "Login", "登录🎉", "the", "👨‍👩‍👧").Replace(specText)
	open := Specs{specFile: {}}
	for _, s := range specOf(cjk).sections {
		for _, r := range s.reqs {
			open[specFile][r.key] = true
			for _, sc := range r.scenarios {
				open[specFile][sc.key] = true
			}
		}
	}
	long := "## ADDED Requirements\n### Requirement: " + strings.Repeat("長い名前", 30) + "\n" + strings.Repeat("Ｗ", 300) + "\n#### Scenario: x\n- **WHEN** " + strings.Repeat("y", 200)
	for name, text := range map[string]string{"sample": specText, "cjk": cjk, "long": long} {
		st := specState()
		st.Tabs[2].Buf.SetText(text)
		for oname, o := range map[string]Options{"folded": {Now: now}, "open": {Now: now, Specs: open}} {
			for w := 0; w <= 70; w++ {
				for _, h := range []int{0, 1, 2, 3, 4, 6, 9, 16} {
					checkSize(t, name+"/"+oname, Render(st, o, w, h), w, h)
				}
			}
			for _, s := range [][2]int{{80, 24}, {120, 40}, {170, 52}, {200, 60}} {
				checkSize(t, name+"/"+oname, Render(st, o, s[0], s[1]), s[0], s[1])
			}
		}
	}
}

// flow wraps by cells: wide characters count two, and one wider than the row gets a row of its own.
func TestSpecFlow(t *testing.T) {
	for _, tt := range []struct {
		text string
		w    int
		want []string
	}{
		{"", 10, []string{""}},
		{"alpha beta gamma", 10, []string{"alpha ", "beta gamma"}},
		{"世界世界世界", 5, []string{"世界", "世界", "世界"}},
		{"世界", 1, []string{"世", "界"}},
		{"averyveryverylongword", 8, []string{"averyver", "yverylon", "gword"}},
	} {
		var got []string
		for _, row := range flow([]span{{text: tt.text, st: fgStyle}}, tt.w) {
			got = append(got, ansi.Strip(specRow{spans: row}.draw(100)))
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%q at %d: %q, want %q", tt.text, tt.w, got, tt.want)
		}
	}
	// Styles carry over a row's break.
	rows := flow([]span{{text: "plain ", st: fgStyle}, {text: "code code", st: specCodeStyle}}, 10)
	if len(rows) != 2 || !styled(specRow{spans: rows[1]}.draw(10), "code", sgr("38", theme.Violet)) {
		t.Errorf("rows %v", rows)
	}
}

// Normative words are red outside code, as whole words; ** goes.
func TestSpecInline(t *testing.T) {
	plainOf := func(spans []span) (s string, red []string) {
		for _, sp := range spans {
			s += sp.text
			if sp.st.GetForeground() == specNormStyle.GetForeground() {
				red = append(red, sp.text)
			}
		}
		return s, red
	}
	for _, tt := range []struct {
		in, plain string
		red       []string
	}{
		{"It SHALL NOT, it MUST.", "It SHALL NOT, it MUST.", []string{"SHALL NOT", "MUST"}},
		{"SHALLOW MUSTARD SHALLNOT", "SHALLOW MUSTARD SHALLNOT", nil},
		{"a `SHALL` b **MUST NOT**", "a SHALL b MUST NOT", []string{"MUST NOT"}},
		{"open `code", "open `code", nil},
		{"", "", nil},
	} {
		if s, red := plainOf(inline(tt.in, fgStyle)); s != tt.plain || !slices.Equal(red, tt.red) {
			t.Errorf("%q: %q red %q, want %q %q", tt.in, s, red, tt.plain, tt.red)
		}
	}
}

// From the outline, a requirement after a folded one lands on the top row exactly, the blank row
// that opening it puts above it out of sight.
func TestSpecOutlineTop(t *testing.T) {
	st := specState()
	o := Options{Now: now, Specs: Specs{specFile: {"ADDED Requirements\nLogin": false}}, Scroll: Scroll{}}
	h, ok := SpecAt(st, o, 120, 16, 3, 3)
	if !ok || h.Folds[0] != "ADDED Requirements\nLogout" {
		t.Fatalf("outline click: %+v %v", h, ok)
	}
	o.Specs.Fold(h)
	o.Scroll[h.Key] = h.Back
	if rows := strings.Split(ansi.Strip(Render(st, o, 120, 16)), "\n"); !strings.Contains(rows[1], "▾ Logout") {
		t.Errorf("Logout is not the top row:\n%s", strings.Join(rows, "\n"))
	}
}

// Tables and rules show as written, row by row.
func TestSpecTable(t *testing.T) {
	st := specState()
	st.Tabs[2].Buf.SetText("## Requirements\n### Requirement: T\nIt SHALL.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\nAfter.")
	st.Tabs[2].Path = root + "/openspec/specs/auth/spec.md"
	st.Agents[0].File = st.Tabs[2].Path
	rows := specRender(st, Options{Now: now})
	for i, want := range []string{"▌   | a | b |", "▌   |---|---|", "▌   | 1 | 2 |"} {
		if j := rowWith(rows, want); j < 0 || i > 0 && rowWith(rows, "▌   | a | b |")+i != j {
			t.Errorf("no row %q under the one before:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	if rowWith(rows, "▌   ---") < 0 {
		t.Errorf("no rule:\n%s", strings.Join(rows, "\n"))
	}
}

// The layout of a frame is the next frame's, until the text, the folds or the width change.
func TestSpecCache(t *testing.T) {
	st := specState()
	o := Options{Now: now, Specs: Specs{}, SpecRows: SpecCache{}, Scroll: Scroll{}}
	rows := specRender(st, o)
	cached := o.SpecRows[specFile]
	if cached == nil || rowWith(rows, "▸ Logout") < 0 {
		t.Fatal("nothing cached")
	}
	if specRender(st, o); o.SpecRows[specFile] != cached || &cached.rows[0] != &o.SpecRows[specFile].rows[0] {
		t.Error("laid out again with nothing changed")
	}
	o.Specs.Fold(SpecHit{Path: specFile, Folds: []string{"ADDED Requirements\nLogout"}, Open: true})
	if rows := specRender(st, o); rowWith(rows, "▾ Logout") < 0 {
		t.Error("a fold kept the old layout")
	}
	// in place, as an agent's typing changes it; another name starts folded
	st.Tabs[2].Buf.Lines[18] = "### Requirement: Sign out"
	if rows := specRender(st, o); rowWith(rows, "▸ Sign out") < 0 {
		t.Errorf("an edit kept the old layout:\n%s", strings.Join(rows, "\n"))
	}
	if rows := strings.Split(ansi.Strip(Render(st, o, 60, 40)), "\n"); rowWith(rows, "The system SHALL let a user log in with email and password. It") >= 0 {
		t.Error("a narrower editor kept the old width")
	}
	st.Agents[0].File = root + "/src/main.go"
	Prune(st, o, 120, 40)
	if len(o.SpecRows) != 0 {
		t.Error("kept the layout of a spec off screen")
	}
}

// A click on the left column's title switches it between the outline and the files, both ways;
// with the files, the explorer is as without a spec.
func TestSpecSwitch(t *testing.T) {
	st := specState()
	o := Options{Now: now, Scroll: Scroll{filesPane: 1}}
	if !SwitchAt(st, o, 120, 40, 5, 0) || SwitchAt(st, o, 120, 40, 5, 1) || SwitchAt(st, o, 120, 40, 60, 0) {
		t.Error("the title switches nothing, or something else does")
	}
	if SwitchAt(st, Options{Now: now, Panel: Agents}, 120, 40, 5, 0) || SwitchAt(st, Options{Now: now, Raw: true}, 120, 40, 5, 0) {
		t.Error("the title switches beside the agents or a spec as written")
	}
	o.Scroll[outlineKey(specFile)] = -1
	Prune(st, o, 120, 40)
	if _, ok := o.Scroll[filesPane]; ok {
		t.Error("kept the scroll of the files under the outline")
	}
	if _, ok := o.Scroll[outlineKey(specFile)]; !ok {
		t.Error("pruned the outline's scroll")
	}
	if path, _ := TreeAt(st, o, 120, 40, 3, 2); path != "" {
		t.Errorf("the outline opens %q", path)
	}
	o.Explorer = true
	out := Render(st, o, 120, 40)
	if top := strings.Split(ansi.Strip(left(out, 120)), "\n")[0]; !strings.Contains(top, " files · outline ") || !styled(out, " · outline", sgr("38", theme.Dim)) {
		t.Errorf("title: %q", top)
	}
	if l := leftRows(out, 120); !strings.Contains(strings.Join(l, "\n"), "▾ src") || strings.Contains(strings.Join(l, "\n"), "Logout") {
		t.Errorf("not the files:\n%s", strings.Join(l, "\n"))
	}
	if path, dir := TreeAt(st, o, 120, 40, 3, 2); path != root+"/src" || !dir {
		t.Errorf("the files open %q", path)
	}
	if _, ok := SpecAt(st, o, 120, 40, 3, 2); ok || !SwitchAt(st, o, 120, 40, 5, 0) {
		t.Error("the files fold the spec, or do not switch back")
	}
	if rows := specRender(st, o); rowWith(rows, "▾ Login") < 0 {
		t.Error("the spec is no longer structured")
	}
	for _, explorer := range []bool{false, true} { // 80 columns: the left column is 18 wide
		o := Options{Now: now, Explorer: explorer}
		top := strings.Split(ansi.Strip(left(Render(st, o, 80, 40), 80)), "\n")[0]
		if want := map[bool]string{false: " · files ", true: " · outline "}[explorer]; !strings.Contains(top, want) {
			t.Errorf("80 columns, the files %v: title %q, no %q", explorer, top, want)
		}
	}
	o.Scroll[filesPane] = 1
	Prune(st, o, 120, 40)
	if _, ok := o.Scroll[outlineKey(specFile)]; ok {
		t.Error("kept the scroll of the outline under the files")
	}
	if _, ok := o.Scroll[filesPane]; !ok {
		t.Error("pruned the scroll of the files")
	}
}
