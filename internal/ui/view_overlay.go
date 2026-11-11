package ui

import (
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/protocol"
	"github.com/pixelbender/claudit/internal/theme"
)

// How the overlays move: a popup drops in from above.
const (
	drawPopupDrop  = 180 * time.Millisecond
	drawSuggestMax = 8 // completion rows on screen
)

func drawEaseOut(t float64) float64 { return 1 - math.Pow(1-min(max(t, 0), 1), 3) }

// renderSuggest draws the slash-command completion list: each name with the typed part in bold,
// its argument hint, and its description dimmed after them; the highlighted one lit, the list
// scrolled to keep it in view.
func renderSuggest(cmds []protocol.Command, sel int, typed string, maxWidth int) string {
	top, n := drawSuggestRange(len(cmds), sel)
	shown := cmds[top : top+n]
	nameW, descW := 0, 0
	for _, c := range shown {
		nameW = max(nameW, ansi.StringWidth(drawOneLine("/"+c.Name+" "+c.Hint)))
		descW = max(descW, ansi.StringWidth(drawOneLine(c.Description)))
	}
	nameW = min(nameW, 32)
	inner := nameW
	if descW > 0 {
		inner += 2 + min(descW, 48)
	}
	inner = min(max(inner, 12), max(1, maxWidth-4))
	lines := make([]string, 0, n)
	for i, c := range shown {
		row := lipgloss.NewStyle()
		if top+i == sel {
			row = row.Background(theme.SelBg)
		}
		head, name := drawTypedPrefix(c.Name, typed)
		line := row.Foreground(theme.Dim).Render("/") + row.Foreground(theme.Accent).Render(head) + row.Foreground(theme.Fg).Render(name)
		if c.Hint != "" {
			line += row.Foreground(theme.Dim).Render(" " + drawOneLine(c.Hint))
		}
		line = drawFitTrunc(line, min(nameW, inner))
		if room := inner - nameW - 2; room > 0 && c.Description != "" {
			line += row.Render("  ") + row.Foreground(theme.Dim).Render(drawFitTrunc(drawOneLine(c.Description), room))
		}
		lines = append(lines, line+row.Render(strings.Repeat(" ", max(0, inner-ansi.StringWidth(line)))))
	}
	box := drawBox(lines, inner, theme.Border)
	if more := len(cmds) - n; more > 0 && inner >= 4+len(strconv.Itoa(more)) {
		box[len(box)-1] = drawMoreBottom(inner, theme.Border, more)
	}
	return strings.Join(box, "\n")
}

// drawSuggestRange is the first of count completions on screen with sel highlighted, and how many show.
func drawSuggestRange(count, sel int) (top, n int) {
	n = min(count, drawSuggestMax)
	return min(max(sel-n+1, 0), count-n), n
}

// drawTypedPrefix splits name after the part that matches typed, ignoring case; head is "" when
// name does not start with typed. It walks runes: lowercase forms can differ in byte length.
func drawTypedPrefix(name, typed string) (head, rest string) {
	tr := []rune(typed)
	i := 0
	for j, r := range name {
		if i == len(tr) {
			return name[:j], name[j:]
		}
		if unicode.ToLower(r) != unicode.ToLower(tr[i]) {
			return "", name
		}
		i++
	}
	if i == len(tr) {
		return name, ""
	}
	return "", name
}

// drawMoreBottom is a box's bottom border saying how many rows are off screen.
func drawMoreBottom(inner int, c color.Color, more int) string {
	b := lipgloss.NewStyle().Foreground(c)
	label := " +" + strconv.Itoa(more) + " "
	return b.Render("╰─") + styleDim.Render(label) + b.Render(strings.Repeat("─", max(0, inner+1-ansi.StringWidth(label)))+"╯")
}

// drawHelpRows are the lines of the key reference: key, what it does.
var drawHelpRows = [][2]string{
	{"", "Prompt"},
	{"enter", "send the prompt; while claude works, it waits its turn"},
	{"ctrl+j", "new line (or alt+enter, or \\ then enter)"},
	{"ctrl+a e", "edit as in a shell: alt+b/f by words, ctrl+w/u/k cut, ctrl+y puts it back"},
	{"ctrl+v", "paste the picture on the clipboard; drop picture files to attach them"},
	{"↑ ↓", "previous prompts; queued prompts first, edited in their place"},
	{"!", "a shell command, run in the project; claude gets it and its output and goes on"},
	{"/", "slash commands: ↑/↓ pick, tab complete, enter run; a typo is held back, enter again sends it"},
	{"/rename", "name the conversation: the window's title and /resume show the name"},
	{"esc", "show every agent again, else stop a ! command, else interrupt claude, else clear the prompt, else quit (twice; confirms unfinished tasks)"},
	{"shift+tab", "permission mode: manual → accept edits → plan → auto; over a request, also answers what the new mode allows"},
	{"pgup pgdn", "scroll the conversation (or the wheel)"},
	{"drag", "select text in the conversation or a pane; letting go copies it"},
	{"click", "what a Bash call does, in the conversation: where it ran, in the terminal"},
	{"", "Questions and permissions"},
	{"y  n", "allow, deny (esc denies too); ↑/↓ pgup/pgdn scroll; or click"},
	{"", "Camera"},
	{"alt+1-9", "show an agent alone (or click it; click again or esc for all)"},
	{"alt+0", "auto camera: follow the action"},
	{"tab", "agents panel while agents work: ↑/↓ select, enter pin"},
	{"alt+z", "word wrap in the editor on/off"},
	{"", "Views"},
	{"F1  /help", "this reference"},
	{"ctrl+c", "clear the prompt, interrupt, or quit (twice)"},
}

// renderHelp draws the key reference box, at most maxWidth×maxHeight.
func renderHelp(maxWidth, maxHeight int) string {
	inner := max(1, min(maxWidth-4, 96))
	keyW := 15
	title := drawGradient("claudit", theme.Accent, theme.Violet, theme.Sky) + styleDim.Render("  ·  keys")
	lines := []string{title, ""}
	for _, r := range drawHelpRows {
		if r[0] == "" {
			if len(lines) > 2 {
				lines = append(lines, "")
			}
			lines = append(lines, styleStrong.Render(r[1]))
			continue
		}
		key := styleAccent.Render(drawFit(r[0], keyW))
		lines = append(lines, drawFlow([]drawSpan{{r[1], styleFg}}, inner, "  "+key, strings.Repeat(" ", 2+keyW))...)
	}
	lines = append(lines, "", styleDim.Render("esc close"))
	if len(lines) > maxHeight-2 { // a short screen: no blank lines
		lines = slices.DeleteFunc(lines, func(l string) bool { return l == "" })
	}
	if n := max(1, maxHeight-2); len(lines) > n { // cut short, it still says how to close it
		lines = append(lines[:n-1], lines[len(lines)-1])
	}
	return strings.Join(drawBox(lines, inner, theme.Border), "\n")
}

// drawGradient paints text with colours running through stops, left to right.
func drawGradient(text string, stops ...color.Color) string {
	rs := []rune(text)
	var b strings.Builder
	for i, r := range rs {
		c := theme.Gradient(float64(i)/float64(max(len(rs)-1, 1)), stops...)
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
	}
	return b.String()
}

// drawShimmer paints text in base with a bright band sweeping across it, one rune per frame.
func drawShimmer(text string, frame int, base color.Color) string {
	rs := []rune(text)
	period := len(rs) + 8
	pos := frame%period - 4
	var b strings.Builder
	for i, r := range rs {
		c := base
		if d := math.Abs(float64(i - pos)); d < 3 {
			c = theme.Mix(base, theme.Bright, 0.7*(1-d/3))
		}
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
	}
	return b.String()
}

// drawPopupY is the row of a popup of height ph centred in h rows, dropping in from above while new.
func drawPopupY(h, ph int, age time.Duration) int {
	y := (h - ph) / 2
	if age >= 0 && age < drawPopupDrop {
		y -= int((1 - drawEaseOut(float64(age)/float64(drawPopupDrop))) * 4)
	}
	return max(y, 0)
}
