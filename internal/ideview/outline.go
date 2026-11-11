package ideview

import (
	"fmt"
	"maps"
	"strconv"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/theme"
)

// outlineKey is the scroll key of the outline of the spec at path in the left column.
func outlineKey(path string) string { return "outline:" + path }

// outlined is the spec the left column outlines in place of the files: beside's, unless the user
// chose the files.
func outlined(st *ide.State, o Options, width, height int) *specView {
	if o.Explorer {
		return nil
	}
	return beside(st, o, width, height)
}

// beside is the spec the left column can outline: the one the first editor on the right shows
// structured; nil for none, or while the left column shows the agents.
func beside(st *ide.State, o Options, width, height int) *specView {
	if o.Panel != Files {
		return nil
	}
	ew := leftWidth(width)
	cols := columns(st, camera(st), o, width-ew, height)
	for i, r := range tiles(len(cols), width-ew, height) {
		if cols[i].pane != ide.Editor {
			continue
		}
		iw, ih := inner(r.Dx(), r.Dy())
		if v := structured(st, cols[i].agent, o, iw, ih); v != nil {
			return v
		}
	}
	return nil
}

// outlineRow is a row of the outline: the label of a section, a requirement of it, or the blank
// between two sections.
type outlineRow struct{ sec, req int } // -1 for none

// outline is the rows of v's outline: per section with requirements its label, then a row per
// requirement; the row of the requirement at the top of the editor; and the outline's first row
// in ih rows that keeps that one in sight, and the first row it shows, scrolled as o says.
func (v *specView) outline(o Options, ih int) (rows []outlineRow, sel, follow, top int) {
	sec, req := v.current()
	sel = -1
	for i, s := range v.doc.sections {
		if len(s.reqs) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, outlineRow{-1, -1})
		}
		if sectionLabel(s) != "" {
			rows = append(rows, outlineRow{i, -1})
		}
		for j := range s.reqs {
			if i == sec && j == req {
				sel = len(rows)
			}
			rows = append(rows, outlineRow{i, j})
		}
	}
	follow = scrollTop(sel, len(rows), ih)
	return rows, sel, follow, scrolled(follow, o.Scroll[outlineKey(v.path)], max(0, len(rows)-ih))
}

// outlinePane draws the outline of v in the left column, w×h cells: the sections' labels in their
// colours; a row per requirement, its name after the bar of its section, the count of its
// scenarios at the right; the requirement at the top of the editor lit as the viewed file is in
// the explorer.
func outlinePane(v *specView, o Options, w, h int) []string {
	iw, ih := inner(w, h)
	rows, sel, _, top := v.outline(o, ih)
	out := make([]string, ih)
	for i := range out {
		r := top + i
		if r >= len(rows) || rows[r].sec < 0 {
			continue
		}
		s := v.doc.sections[rows[r].sec]
		c := opColor(s.op)
		if rows[r].req < 0 {
			out[i] = lipgloss.NewStyle().Foreground(c).Bold(true).Render(clip(sectionLabel(s), iw, "…"))
			continue
		}
		req := s.reqs[rows[r].req]
		base := lipgloss.NewStyle()
		if r == sel {
			base = base.Background(theme.SelBg)
		}
		n := strconv.Itoa(len(req.scenarios))
		room := max(0, iw-3-len(n)) // the bar and a space before the name, a space before the count
		name := clip(req.name, room, "…")
		pad := fmt.Sprintf("%*s", room-ansi.StringWidth(name)+1, "")
		out[i] = base.Foreground(c).Render("▌") + base.Render(" ") + base.Foreground(theme.Bright).Render(name) +
			base.Render(pad) + base.Foreground(theme.Dim).Render(n)
	}
	reqs, _ := v.doc.counts()
	return frame(w, h, theme.Border, switchTitle(iw-1, "files", count(reqs, "requirement"), "outline"), out)
}

// switchTitle heads the left column beside a spec in room cells: the first of shown that fits,
// then, dim, what a click on the title switches it to; that alone when none fits, so that the
// switch stays in sight.
func switchTitle(room int, other string, shown ...string) string {
	for _, s := range shown {
		if ansi.StringWidth(" "+s+" · "+other+" ") <= room {
			return " " + fgStyle.Render(s) + dimStyle.Render(" · "+other) + " "
		}
	}
	return " " + dimStyle.Render("· "+other) + " "
}

// modeAt is the mode the word of a switch at cell x of r picks; "" elsewhere.
func (r specRow) modeAt(x int) string {
	at := ansi.StringWidth(r.lead)
	for _, s := range r.spans {
		w := ansi.StringWidth(s.text)
		if x >= at && x < at+w {
			return s.mode
		}
		at += w
	}
	return ""
}

// SwitchAt reports whether a click at cell (x, y) of Render(st, o, width, height) falls on the
// title of the left column while it can show a spec's outline or the files: the click switches.
func SwitchAt(st *ide.State, o Options, width, height, x, y int) bool {
	return y == 0 && x >= 0 && x < leftWidth(width) && height > 1 && beside(st, o, width, height) != nil
}

// SpecAt is what a click at cell (x, y) of Render(st, o, width, height) does to a spec shown
// structured: on a row of the editor that heads a section, a requirement or a scenario, or on a
// requirement of the outline. It is false elsewhere.
func SpecAt(st *ide.State, o Options, width, height, x, y int) (SpecHit, bool) {
	ew := leftWidth(width)
	if y < 1 || y >= height-1 || x < 0 || x >= width {
		return SpecHit{}, false
	}
	if x < ew {
		v := outlined(st, o, width, height)
		if v == nil || x < 1 || x >= ew-1 {
			return SpecHit{}, false
		}
		_, ih := inner(ew, height)
		rows, _, _, top := v.outline(o, ih)
		if i := top + y - 1; i < len(rows) && rows[i].req >= 0 {
			return v.hit(specRow{kind: rowReq, sec: rows[i].sec, req: rows[i].req, scen: -1}, -1, true)
		}
		return SpecHit{}, false
	}
	c, t, ok := columnAt(st, o, width, height, x, y)
	if !ok || c.pane != ide.Editor || x < t.Min.X+1 || x >= t.Max.X-1 || y < t.Min.Y+1 || y >= t.Max.Y-1 {
		return SpecHit{}, false
	}
	iw, ih := inner(t.Dx(), t.Dy())
	v := structured(st, c.agent, o, iw, ih)
	if v == nil || v.top+y-t.Min.Y-1 >= len(v.rows) {
		return SpecHit{}, false
	}
	return v.hit(v.rows[v.top+y-t.Min.Y-1], x-t.Min.X-2, false) // the row starts after a cell of air
}

// hit is what a click on row r at cell x of it does: on a word of a requirement's switch, it
// picks that mode; elsewhere it turns the fold the row heads; from the outline, it opens the
// requirement and brings it to the top of the editor.
func (v *specView) hit(r specRow, x int, outline bool) (SpecHit, bool) {
	h := SpecHit{Path: v.path}
	kind, sec, req := r.kind, r.sec, r.req
	if m := r.modeAt(x); m != "" && kind == rowReq && !outline {
		h.Folds, h.Mode = []string{v.doc.sections[sec].reqs[req].key}, m
		return h, true
	}
	switch s := v.doc.sections[max(sec, 0)]; {
	case kind == rowText:
		return SpecHit{}, false
	case kind == rowSection: // they all open, unless they are all open: then they all fold
		h.Open = false
		for j, r := range s.reqs {
			h.Folds = append(h.Folds, r.key)
			h.Open = h.Open || !reqOpen(v.folds, s, j)
		}
	case kind == rowScenario:
		h.Folds, h.Open = []string{r.key}, !r.open
	case outline: // where it is once open: opening it may put a blank row above it
		key := s.reqs[req].key
		h.Folds, h.Open, h.Key = []string{key}, true, specKey(v.path)
		folds := maps.Clone(v.folds)
		if folds == nil {
			folds = map[string]bool{}
		}
		folds[key] = true
		for i, r := range v.doc.layout(folds, v.modes, v.width) {
			if r.kind == rowReq && r.sec == sec && r.req == req && !r.blank() {
				h.Back = -i
				break
			}
		}
	default:
		h.Folds, h.Open = []string{s.reqs[req].key}, !reqOpen(v.folds, s, req)
	}
	return h, true
}
