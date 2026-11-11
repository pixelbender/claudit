package ideview

import (
	"image"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/theme"
)

// Scrollbar occupies the right edge of a pane, excluding its corners.
type Scrollbar struct {
	Key          string
	X, Y, Height int
	follow, last int
	top, visible int
}

// Thumb returns the first screen row and height of the draggable part.
func (b Scrollbar) Thumb() (int, int) {
	n := min(max(1, b.Height*b.visible/(b.last+b.visible)), max(1, b.Height-1))
	return b.Y + (b.top*(b.Height-n)+b.last/2)/b.last, n
}

// Scroll returns the scroll offset for a thumb grabbed grab rows below its top at screen row y.
func (b Scrollbar) Scroll(y, grab int) int {
	_, n := b.Thumb()
	travel := b.Height - n
	if travel <= 0 {
		return b.follow - b.top
	}
	pos := min(max(y-b.Y-grab, 0), travel)
	return b.follow - (pos*b.last+travel/2)/travel
}

// Scrollbars lists only overflowing panes, using the same geometry and offsets as Render.
func Scrollbars(st *ide.State, o Options, width, height int) []Scrollbar {
	var bars []Scrollbar
	add := func(key string, r image.Rectangle, follow, last, visible int) {
		if key == "" || r.Dx() < 2 || r.Dy() < 3 || last <= 0 || visible <= 0 {
			return
		}
		bars = append(bars, Scrollbar{Key: key, X: r.Max.X - 1, Y: r.Min.Y + 1, Height: r.Dy() - 2,
			follow: follow, last: last, top: scrolled(follow, o.Scroll[key], last), visible: visible})
	}
	ew := leftWidth(width)
	_, ih := inner(ew, height)
	left := image.Rect(0, 0, ew, height)
	if o.Panel == Agents {
		base := o
		base.Scroll = nil
		tree, follow := agentsView(st, camera(st), base, ih)
		add(agentsPaneKey, left, follow, max(0, len(tree)-ih), ih)
	} else if v := outlined(st, o, width, height); v != nil {
		rows, _, follow, _ := v.outline(o, ih)
		add(outlineKey(v.path), left, follow, max(0, len(rows)-ih), ih)
	} else {
		add(filesPane, left, scrollTop(treeSel(st, camera(st)), len(st.Tree), ih), max(0, len(st.Tree)-ih), ih)
	}
	cols := columns(st, camera(st), o, width-ew, height)
	for i, r := range tiles(len(cols), width-ew, height) {
		c := cols[i]
		iw, ih := inner(r.Dx(), r.Dy())
		r = r.Add(image.Pt(ew, 0))
		switch c.pane {
		case ide.Terminal:
			last := max(0, o.terminalLayout(&c.agent.Term, textWidth(iw)).total-ih)
			add(paneKey(st, c), r, last, last, ih)
		case ide.Browser:
			ph := pageHeight(ih)
			add(paneKey(st, c), r, 0, max(0, len(pageRows(&c.agent.Page, textWidth(iw), -1))-ph), ph)
		default:
			if v := structured(st, c.agent, o, iw, ih); v != nil {
				add(specKey(v.path), r, 0, v.last, ih)
			} else if tab := shownTab(st, c.agent); tab != nil {
				lines, cur, tw := editorText(tab, c.agent, iw)
				follow, last := editorRange(lines, cur, tw, ih, o.Wrap)
				add(paneKey(st, c), r, follow, last, ih)
			}
		}
	}
	return bars
}

func drawScrollbars(lines []string, bars []Scrollbar) {
	track := lipgloss.NewStyle().Foreground(theme.Border).Render("│")
	thumb := lipgloss.NewStyle().Foreground(theme.Fg).Render("│")
	for _, b := range bars {
		top, n := b.Thumb()
		for y := b.Y; y < b.Y+b.Height; y++ {
			cell := track
			if y >= top && y < top+n {
				cell = thumb
			}
			lines[y] = ansi.Cut(lines[y], 0, b.X) + cell + ansi.Cut(lines[y], b.X+1, ansi.StringWidth(lines[y]))
		}
	}
}
