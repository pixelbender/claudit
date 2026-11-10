package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/theme"
)

func TestChatGoalConfirmation(t *testing.T) {
	entries := []ChatEntry{
		{Kind: AssistantEntry, Text: "Ready"},
		{Kind: CommandEntry, Text: "\x1b[32mGoal set: build **the app**\x1b[0m"},
	}
	out, _, _ := renderChat(new(chatCache), entries, 60, 3, 0)
	rows := strings.Split(ansi.Strip(out), "\n")
	if strings.TrimSpace(rows[1]) != "" || strings.TrimSpace(rows[2]) != "› Build **the app**" {
		t.Fatalf("goal is not displayed as a plain user prompt:\n%s", ansi.Strip(out))
	}
	want := lipgloss.NewStyle().Foreground(theme.Orange).Render("› Build **the app**")
	if !strings.Contains(out, want) {
		t.Fatal("the goal confirmation is not orange")
	}
	if entries[1].Kind != CommandEntry || !strings.Contains(entries[1].Text, "Goal set:") {
		t.Fatal("displaying a goal mutated the conversation history")
	}
	for _, kind := range []EntryKind{UserEntry, AssistantEntry} {
		if out := strings.Join(drawEntry(ChatEntry{Kind: kind, Text: "Goal set: example"}, 60), "\n"); !strings.Contains(ansi.Strip(out), "Goal set:") {
			t.Fatal("a regular message lost its literal prefix")
		}
	}
}

// drawCheckSize fails unless s is exactly h lines of exactly w cells.
func drawCheckSize(t *testing.T, s string, w, h int) {
	t.Helper()
	lines := strings.Split(s, "\n")
	if len(lines) != h {
		t.Fatalf("%dx%d: got %d lines", w, h, len(lines))
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Fatalf("%dx%d: line %d is %d cells: %q", w, h, i, got, ansi.Strip(l))
		}
		// The renderer counts by wcwidth where the terminal does not report its own (iTerm2).
		if got := ansi.StringWidthWc(l); got != w {
			t.Fatalf("%dx%d: line %d is %d cells by wcwidth: %q", w, h, i, got, ansi.Strip(l))
		}
	}
}

// drawPlainLines strips styles and trailing spaces.
func drawPlainLines(s string) []string {
	lines := strings.Split(ansi.Strip(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

var drawTestEntries = []ChatEntry{
	{Kind: UserEntry, Text: "Hello! Please fix the division function in calc.py"},
	{Kind: AssistantEntry, Text: "这是一个很长的句子没有空格需要被折行处理 and some words 😀😀😀 emoji ❤️ 👨‍👩‍👧"},
	{Kind: AssistantEntry, Text: ""},
	{Kind: AssistantEntry, Text: "## Summary\n\n- **calc.py:** `div(a, b)` returns *a / b*\n- see [docs](https://example.com)\n\n" +
		"| Category | Tokens | Percentage |\n|---|---|---|\n| System prompt | 3.3k | 0.3% |\n| Free space | 949.7k | 95.0% |\n\n" +
		"```python\ndef div(a, b):\n    return a / b  # 😀 very long comment that goes on and on\n```\n\n> quoted\n\n---\n" +
		"claude exited: \x1b[31mexit status 1\x1b[0m\twith\ttabs\r\nand a second line"},
	{Kind: AssistantEntry, Text: "supercalifragilisticexpialidocious-and-a-very-long-unbreakable-token\n\n    indented code"},
}

func TestChatSize(t *testing.T) {
	for _, w := range []int{1, 2, 3, 4, 5, 7, 10, 13, 20, 37, 40, 80, 133} {
		for _, h := range []int{1, 2, 5, 17, 60} {
			for _, scroll := range []int{-3, 0, 1, 7, 1000} {
				out, _, _ := renderChat(new(chatCache), drawTestEntries, w, h, scroll)
				drawCheckSize(t, out, w, h)
			}
		}
	}
	out, maxScroll, _ := renderChat(new(chatCache), nil, 30, 4, 0)
	drawCheckSize(t, out, 30, 4)
	if maxScroll != 0 {
		t.Errorf("empty chat: maxScroll = %d", maxScroll)
	}
}

func TestChatWrapKeepsText(t *testing.T) {
	tests := []struct{ name, text string }{
		{"ascii", "Hello world, this is a long line in plain words with no surprises"},
		{"wide", "这是一个很长的句子没有空格需要被折行处理"},
		{"emoji", "ab😀😀😀😀😀😀 ❤️❤️❤️ 👨‍👩‍👧👨‍👩‍👧 done"},
		{"long word", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bbb"},
	}
	for _, tt := range tests {
		for _, w := range []int{8, 9, 11, 14, 27} { // the reply's "› " takes 2 cells of each
			out, _, _ := renderChat(new(chatCache), []ChatEntry{{Kind: AssistantEntry, Text: tt.text}}, w, 40, 0)
			drawCheckSize(t, out, w, 40)
			var got strings.Builder
			for _, l := range drawPlainLines(out) {
				got.WriteString(strings.ReplaceAll(l, " ", ""))
			}
			text := strings.Join(strings.FieldsFunc(tt.text, ideview.Undrawn), "") // ❤️ draws as ❤
			if want := "›" + strings.ReplaceAll(text, " ", ""); got.String() != want {
				t.Errorf("%s at width %d: text changed:\n got %q\nwant %q", tt.name, w, got.String(), want)
			}
		}
	}
}

func TestChatScroll(t *testing.T) {
	var entries []ChatEntry
	for i := range 10 {
		entries = append(entries, ChatEntry{Kind: AssistantEntry, Text: fmt.Sprintf("line %d", i)})
	}
	// 10 entries, no blank lines between them: 10 lines.
	const h = 5
	out, maxScroll, _ := renderChat(new(chatCache), entries, 30, h, 0)
	if maxScroll != 10-h {
		t.Fatalf("maxScroll = %d, want %d", maxScroll, 10-h)
	}
	if lines := drawPlainLines(out); lines[h-1] != "  › line 9" {
		t.Errorf("tail: bottom line = %q", lines[h-1])
	}
	top, _, _ := renderChat(new(chatCache), entries, 30, h, maxScroll)
	if lines := drawPlainLines(top); lines[0] != "  › line 0" {
		t.Errorf("scrolled to the top: first line = %q", lines[0])
	}
	if over, _, _ := renderChat(new(chatCache), entries, 30, h, maxScroll+50); over != top {
		t.Error("scroll beyond maxScroll is not clamped")
	}
	if under, _, _ := renderChat(new(chatCache), entries, 30, h, -5); under != out {
		t.Error("negative scroll is not clamped")
	}
	one, _, _ := renderChat(new(chatCache), entries, 30, h, 2)
	if lines := drawPlainLines(one); lines[h-1] != "  › line 7" {
		t.Errorf("scroll 2: bottom line = %q", lines[h-1])
	}

	// A short log is tail-aligned and cannot scroll.
	short, maxScroll, _ := renderChat(new(chatCache), entries[:1], 30, h, 3)
	if maxScroll != 0 {
		t.Errorf("short log: maxScroll = %d", maxScroll)
	}
	if lines := drawPlainLines(short); lines[0] != "" || lines[h-1] != "  › line 0" {
		t.Errorf("short log is not tail-aligned: %q", lines)
	}
}

// No line of the chat is blank: not between entries, not inside them.
func TestChatDense(t *testing.T) {
	entries := []ChatEntry{
		{Kind: UserEntry, Text: "fix it\n\nplease"},
		{Kind: AssistantEntry, Text: "Done.\n\n- calc.py: div"},
		{Kind: CommandEntry, Text: "one\n\ntwo"},
	}
	out, _, _ := renderChat(new(chatCache), entries, 40, 7, 0)
	want := []string{"  › fix it", "    please", "  › Done.", "    • calc.py: div", "  › one", "    two"}
	if got := drawPlainLines(out)[1:]; !slices.Equal(got, want) {
		t.Errorf("chat %q, want %q", got, want)
	}
}

// A blank line sets the user's prompts apart from what claude said before them.
func TestChatGap(t *testing.T) {
	entries := []ChatEntry{
		{Kind: UserEntry, Text: "one"},
		{Kind: UserEntry, Text: "two"},
		{Kind: AssistantEntry, Text: "reply"},
		{Kind: AssistantEntry}, // nothing streamed yet
		{Kind: UserEntry, Text: "three"},
		{Kind: CommandEntry, Text: "out"},
		{Kind: UserEntry, Text: "four", Queued: true},
	}
	out, _, _ := renderChat(new(chatCache), entries, 40, 9, 0)
	want := []string{"  › one", "  › two", "  › reply", "", "  › three", "  › out", "", "  › four"}
	if got := drawPlainLines(out)[1:]; !slices.Equal(got, want) {
		t.Errorf("chat %q, want %q", got, want)
	}
	if n := drawChatCount(new(chatCache), entries, 40); n != len(want) {
		t.Errorf("count %d, want %d", n, len(want))
	}

	// An answer to a popup's question sticks to the question; the blank line comes after it.
	entries = []ChatEntry{
		{Kind: AssistantEntry, Text: "said"},
		{Kind: AssistantEntry, Text: "Which?"},
		{Kind: UserEntry, Text: "this", Answer: true},
		{Kind: AssistantEntry, Text: "And?"},
		{Kind: UserEntry, Text: "that", Answer: true},
		{Kind: UserEntry, Text: "next"},
		{Kind: CaretEntry, Text: "⠋"},
	}
	want = []string{"  › said", "  › Which?", "  › this", "", "  › And?", "  › that", "", "  › next", "    ⠋"}
	out, _, _ = renderChat(new(chatCache), entries, 40, len(want), 0)
	if got := drawPlainLines(out); !slices.Equal(got, want) {
		t.Errorf("answers %q, want %q", got, want)
	}
}

func TestChatEntries(t *testing.T) {
	out, _, _ := renderChat(new(chatCache), drawTestEntries, 60, 40, 0)
	plain := strings.Join(drawPlainLines(out), "\n")
	for _, want := range []string{
		"› Hello!",
		"Summary",
		"• calc.py: div(a, b) returns a / b",
		"• see docs",
		"Category      │ Tokens │ Percentage",
		"──────────────┼────────┼───────────",
		"System prompt │ 3.3k   │ 0.3%",
		"│ def div(a, b):",
		"▏ quoted",
		"exit status 1    with    tabs",
		"    indented code",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in\n%s", want, plain)
		}
	}
	for _, leak := range []string{"\x1b", "\r", "**", "`", "](", "|---"} {
		if strings.Contains(plain, leak) {
			t.Errorf("%q leaks into the chat:\n%s", leak, plain)
		}
	}
}

// Markdown keeps every line within the width, whatever it holds.
// What an agent writes is white: its gutter, its paragraphs, its list items; headings and code
// keep their own colours.
func TestChatReplyWhite(t *testing.T) {
	lines := drawEntry(ChatEntry{Kind: AssistantEntry, Text: "# Title\nplain words\n- an item `code`"}, 60)
	if len(lines) != 3 || !strings.HasPrefix(lines[0], styleReply.Render("› ")+styleHeading.Render("Title")) ||
		!strings.Contains(lines[1], styleReply.Render("plain")) || !strings.Contains(lines[2], styleReply.Render("an")) ||
		!strings.Contains(lines[2], styleCode.Render("code")) {
		t.Errorf("reply %q", lines)
	}
}

// What a Bash call says it does is light lime, as typed: no markdown, its gutter as a reply's.
func TestChatBashLime(t *testing.T) {
	lines := drawEntry(ChatEntry{Kind: AssistantEntry, Text: "Run `go test` in **sub**", Bash: true}, 60)
	if want := styleReply.Render("› ") + styleBash.Render("Run `go test` in **sub**"); len(lines) != 1 || lines[0] != want {
		t.Errorf("bash entry %q, want %q", lines, want)
	}
}

func TestChatMarkdownWidth(t *testing.T) {
	src := drawTestEntries[3].Text + "\n\n1. first item with a long tail of words that has to wrap\n   - nested **bold item** too\n" +
		"# 👨‍👩‍👧 heading with emoji and 1️⃣ keycaps\n```\nunterminated fence"
	for w := 1; w <= 90; w++ {
		for i, l := range drawMarkdown(src, w) {
			if got := ansi.StringWidth(l); got > w {
				t.Fatalf("width %d: line %d is %d cells: %q", w, i, got, ansi.Strip(l))
			}
		}
	}
	if lines := drawMarkdown("```go\nfunc main() {}", 40); len(lines) != 1 || !strings.Contains(ansi.Strip(lines[0]), "func main() {}") {
		t.Errorf("a streaming fence is not code: %q", lines)
	}
	if got := drawMarkdown("**bold** and `code`", 40)[0]; !strings.Contains(got, styleCode.Render("code")) || !strings.Contains(got, styleFg.Foreground(theme.Strong).Render("bold")) {
		t.Errorf("inline styles: %q", got)
	}
}

func TestChatInput(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		cursor  int
		width   int
		wantX   int
		wantHas string
	}{
		{"empty shows placeholder", "", 0, 30, 2, "› Ask claude…"},
		{"ascii", "hello", 5, 30, 7, "› hello"},
		{"caret mid-line", "hello", 2, 30, 4, "› hello"},
		{"wide runes", "你好世界", 2, 30, 6, "› 你好世界"},
		{"emoji", "😀a", 1, 30, 4, "› 😀a"},
		{"newline", "a\nb", 3, 30, 5, "› a↵b"},
		{"emoji sequences", "a❤️👍🏽b", 3, 30, 4, "› a❤👍b"}, // without what terminals measure apart
		{"long line, caret at start", strings.Repeat("x", 100), 0, 20, 2, "› xxxxxxxxxxxxxxxxxx"},
		{"long line, caret at end", strings.Repeat("x", 100), 100, 20, 19, "› xxxxxxxxxxxxxxxxx"},
		{"long wide line, caret at end", strings.Repeat("界", 50), 50, 20, 18, "› 界界界界界界界界"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := &Input{Text: []rune(tt.text), Cursor: tt.cursor}
			out, x := renderInput(in, tt.width, "Ask claude…", false)
			drawCheckSize(t, out, tt.width, 1)
			if x != tt.wantX {
				t.Errorf("cursorX = %d, want %d", x, tt.wantX)
			}
			if plain := ansi.Strip(out); !strings.HasPrefix(plain, tt.wantHas) {
				t.Errorf("got %q, want prefix %q", plain, tt.wantHas)
			}
		})
	}
}

// The caret stays on screen, right after the rune before it and on the rune under it, for every position and width.
func TestChatInputCaret(t *testing.T) {
	text := []rune("Hello, 世界! 😀 abc 你好你好你好 the last word")
	for _, width := range []int{6, 7, 9, 12, 20, 33, 80} {
		for cur := 0; cur <= len(text); cur++ {
			out, x := renderInput(&Input{Text: text, Cursor: cur}, width, "", false)
			drawCheckSize(t, out, width, 1)
			if x < 2 || x >= width {
				t.Fatalf("width %d cursor %d: cursorX %d off screen", width, cur, x)
			}
			plain := ansi.Strip(out)
			if cur > 0 && text[cur-1] != ' ' && !strings.HasSuffix(ansi.Cut(plain, 0, x), string(text[cur-1])) {
				t.Errorf("width %d cursor %d: %q does not end with %q", width, cur, ansi.Cut(plain, 0, x), text[cur-1])
			}
			if cur < len(text) && text[cur] != ' ' && !strings.HasPrefix(ansi.Cut(plain, x, width), string(text[cur])) {
				t.Errorf("width %d cursor %d: %q does not start with %q", width, cur, ansi.Cut(plain, x, width), text[cur])
			}
		}
	}
}

func TestChatStatus(t *testing.T) {
	st := Status{Model: "claude-sonnet-4-5", State: "thinking", Busy: true, Frame: 3, Usage: "5h 9% · 7d 58%", Replay: true, Agents: 2}
	for _, hints := range [][]string{drawHintsPrompt, drawHintsPanel, nil} {
		for w := 0; w <= 200; w++ {
			drawCheckSize(t, renderStatus(st, hints, w), w, 1)
		}
	}
	wide := renderStatus(st, drawHintsPrompt, 190)
	// Model, limits, the steady part; what claude does last, without the agents at work or the camera.
	for _, want := range []string{
		"● claude-sonnet-4-5 · 5h 9% · 7d 58% · replay · ⠸ thinking  ",
		"shift+tab mode · / commands · F1 help · esc x2 quit",
	} {
		if !strings.Contains(ansi.Strip(wide), want) {
			t.Errorf("wide status %q lacks %q", ansi.Strip(wide), want)
		}
	}
	if strings.Contains(ansi.Strip(wide), "agents at work") || strings.Contains(ansi.Strip(wide), "cam ") {
		t.Errorf("the agents at work or the camera in the status: %q", ansi.Strip(wide))
	}
	// The separators are grey.
	if !strings.Contains(wide, styleDim.Render(" · ")) || strings.Contains(wide, styleFg.Render(" · ")) {
		t.Errorf("separators not grey: %q", wide)
	}
	narrow := ansi.Strip(renderStatus(st, drawHintsPrompt, 110))
	if strings.Contains(narrow, "shift+tab mode") || !strings.Contains(narrow, "esc x2 quit") {
		t.Errorf("narrow status should keep only the last hints: %q", narrow)
	}

	// Nothing is cut short.
	for w := 40; w <= 200; w++ {
		if got := drawPlainLines(renderStatus(st, drawHintsPrompt, w))[0]; strings.Contains(got, "…") {
			t.Errorf("%d: cut short: %q", w, got)
		}
	}

	// Narrow, the steady part makes room for what claude does, the model first; the mode stays.
	asks := Status{Model: "claude-opus-5-5", Mode: "auto", State: "waiting for you", Busy: true, Elapsed: "12s", Usage: "5h 9% · 7d 58%", Context: 0.03}
	for _, w := range []int{50, 64, 80} {
		if got := drawPlainLines(renderStatus(asks, drawHintsPrompt, w))[0]; !strings.Contains(got, "waiting for you 12s") || !strings.Contains(got, "auto mode") {
			t.Errorf("%d: %q", w, got)
		}
	}
	if got := drawPlainLines(renderStatus(asks, drawHintsPrompt, 120))[0]; !strings.Contains(got, "claude-opus-5-5") {
		t.Errorf("wide: %q", got)
	}

	// The hints stay as what claude does changes size.
	for _, state := range []string{"idle", "running Bash", "running a very long tool name indeed"} {
		st := st
		st.State = state
		if got := drawPlainLines(renderStatus(st, drawHintsPrompt, 150))[0]; !strings.HasSuffix(got, "shift+tab mode · / commands · F1 help · esc x2 quit") {
			t.Errorf("%s: hints changed: %q", state, got)
		}
	}

	// Hints by what has the keys.
	for _, tt := range []struct {
		hints []string
		want  string
	}{
		{drawHintsPanel, "↑/↓ select · enter pin · alt+0 auto · tab back"},
	} {
		if got := drawPlainLines(renderStatus(st, tt.hints, 200))[0]; !strings.HasSuffix(got, tt.want) {
			t.Errorf("status %q does not end with %q", got, tt.want)
		}
	}
	if got := drawPlainLines(renderStatus(st, drawHintsPanel, 110))[0]; strings.Contains(got, "↑/↓") || !strings.HasSuffix(got, "tab back") {
		t.Errorf("narrow panel status: %q", got)
	}

	if failed := renderStatus(Status{Model: "m", State: "API error 529 · Overloaded", Alert: true}, nil, 80); !strings.Contains(failed, styleErr.Render("API error 529 · Overloaded")) {
		t.Errorf("a failure is not red: %q", failed)
	}
	idle := ansi.Strip(renderStatus(Status{Model: "m", State: "idle"}, drawHintsPrompt, 80))
	if !strings.Contains(idle, "● m · idle") || strings.Contains(idle, "cam") ||
		strings.ContainsAny(idle, strings.Join(drawSpinner, "")) {
		t.Errorf("idle status: %q", idle)
	}
	// While claude starts, a spinner stands in for the dot.
	starting := ansi.Strip(renderStatus(Status{Model: "m", State: "starting", Frame: 3}, nil, 80))
	if !strings.HasPrefix(strings.TrimSpace(starting), drawSpinner[3]+" m · starting") {
		t.Errorf("starting status: %q", starting)
	}

	// The context in few cells, after the limits.
	ctx := renderStatus(Status{Model: "m", State: "idle", Context: 0.25, Usage: "5h 9%"}, nil, 80)
	if got := ansi.Strip(ctx); !strings.Contains(got, "● m · 5h 9% · ctx 25% · idle") {
		t.Errorf("context: status %q", got)
	}

	// The permission mode follows the model, in its colour in Claude Code; every mode shows, the default too.
	if got := ansi.Strip(renderStatus(Status{Model: "m", State: "idle", Mode: "default"}, nil, 80)); strings.TrimSpace(got) != "● m · ⏵ manual mode · idle" {
		t.Errorf("mode default: status %q", got)
	}
	for mode, want := range map[string]string{
		"plan":              styleModePlan.Render("⏸ plan mode"),
		"acceptEdits":       styleModeAccept.Render("⏵⏵ accept edits"),
		"auto":              styleModeAuto.Render("⏵⏵ auto mode"),
		"bypassPermissions": styleModeBypass.Render("⏵⏵ bypass permissions"),
		"dontAsk":           styleModeBypass.Render("⏵⏵ don't ask"),
	} {
		if got := renderStatus(Status{Model: "m", State: "idle", Mode: mode, Usage: "5h 9%"}, nil, 80); !strings.Contains(got, styleFg.Render("m")+styleDim.Render(" · ")+want+styleDim.Render(" · ")+styleDim.Render("5h ")+styleFg.Render("9%")) {
			t.Errorf("mode %s: status %q", mode, got)
		}
	}
}

// The effort follows the model, grey.
func TestChatStatusEffort(t *testing.T) {
	got := renderStatus(Status{Model: "claude-opus-5-5", Effort: "high", State: "idle"}, nil, 100)
	if want := styleFg.Render("claude-opus-5-5") + styleDim.Render(" high"); !strings.Contains(got, want) {
		t.Errorf("status %q lacks %q", got, want)
	}
}

// A window at its limit shows red, extra usage paying past it as a warning; the colour of a use
// goes by the thresholds alone, whatever claude says of the window.
func TestChatUsageStates(t *testing.T) {
	got := drawUsage("5h limit until 13:30 · 7d 40% fable 95% · extra usage")
	sep := styleDim.Render(" · ")
	want := styleDim.Render("5h ") + styleErr.Render("limit until 13:30") + sep + styleDim.Render("7d ") + styleFg.Render("40%") +
		styleDim.Render(" fable ") + styleAccent.Render("95%") + sep + styleModeAuto.Render("extra usage")
	if got != want {
		t.Errorf("drawUsage = %q, want %q", got, want)
	}
}

// A window used 80% shows as the auto mode does, from 90% in Claude's orange; so does the context.
// The names of the windows and of the context are grey.
func TestChatUsageColours(t *testing.T) {
	got := drawUsage("5h 79% 4h12m · 7d 80% fable 95% 2d8h · $0.0123")
	sep := styleDim.Render(" · ")
	want := styleDim.Render("5h ") + styleFg.Render("79%") + styleDim.Render(" 4h12m") + sep + styleDim.Render("7d ") + styleModeAuto.Render("80%") +
		styleDim.Render(" fable ") + styleAccent.Render("95%") + styleDim.Render(" 2d8h") + sep + styleFg.Render("$0.0123")
	if got != want {
		t.Errorf("drawUsage = %q, want %q", got, want)
	}
	ctx := styleDim.Render("ctx ")
	for f, want := range map[float64]string{0.794: ctx + styleFg.Render("79%"), 0.8: ctx + styleModeAuto.Render("80%"), 0.95: ctx + styleAccent.Render("95%")} {
		if got := drawContext(f); got != want {
			t.Errorf("drawContext(%v) = %q, want %q", f, got, want)
		}
	}
}

// A subagent's call is prefixed with "↳ " and the subagent's name in its colour.
// Without the keys the prompt field is dim.
func TestChatInputDim(t *testing.T) {
	in := &Input{Text: []rune("hello"), Cursor: 5}
	on, x1 := renderInput(in, 30, "Ask claude…", false)
	off, x2 := renderInput(in, 30, "Ask claude…", true)
	drawCheckSize(t, off, 30, 1)
	if ansi.Strip(on) != ansi.Strip(off) || x1 != x2 {
		t.Errorf("dimming changed the text: %q / %q", ansi.Strip(on), ansi.Strip(off))
	}
	if !strings.Contains(on, styleFg.Render("hello")) || !strings.Contains(off, styleDim.Render("hello")) ||
		!strings.Contains(off, styleDim.Render("› ")) {
		t.Errorf("dim input not dim: %q", off)
	}
}

// The cache must redraw an entry that changed (streaming text) and every entry when the width changes.
func TestChatCache(t *testing.T) {
	c := new(chatCache)
	entries := []ChatEntry{
		{Kind: UserEntry, Text: "hi"},
		{Kind: AssistantEntry, Text: "Hel"},
	}
	renderChat(c, entries, 30, 6, 0)
	entries[1].Text += "lo **there**"
	out, _, _ := renderChat(c, entries, 30, 6, 0)
	if want, _, _ := renderChat(new(chatCache), entries, 30, 6, 0); out != want {
		t.Fatalf("cached render\n%s\nwant\n%s", out, want)
	}
	narrow, _, _ := renderChat(c, entries, 12, 6, 0)
	if want, _, _ := renderChat(new(chatCache), entries, 12, 6, 0); narrow != want {
		t.Fatalf("after a resize\n%s\nwant\n%s", narrow, want)
	}
}

// Each entry shows the time it was said, grey at the right of its first line; an entry without a
// time and a narrow chat show none.
func TestChatTimes(t *testing.T) {
	at := time.Date(2026, 10, 30, 14, 7, 0, 0, time.UTC)
	entries := []ChatEntry{
		{Kind: UserEntry, Text: "fix the division", At: at},
		{Kind: AssistantEntry, Text: strings.Repeat("word ", 30), At: at.Add(time.Minute)},
		{Kind: CommandEntry, Text: "no time"},
	}
	out, _, _ := renderChat(new(chatCache), entries, 60, 10, 0)
	lines := drawPlainLines(out)
	var timed []string
	for _, l := range lines {
		if strings.HasSuffix(l, "14:07") || strings.HasSuffix(l, "14:08") {
			timed = append(timed, l)
		}
	}
	if len(timed) != 2 || !strings.HasPrefix(timed[0], "  › fix the division") || !strings.HasPrefix(timed[1], "  › word") {
		t.Fatalf("timed lines %q of:\n%s", timed, strings.Join(lines, "\n"))
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(ansi.Strip(l), "14:07") && !strings.Contains(l, styleDim.Render("14:07")) {
			t.Errorf("the time is not grey: %q", l)
		}
		if w := ansi.StringWidth(l); w != 60 {
			t.Errorf("line of %d cells: %q", w, ansi.Strip(l))
		}
	}
	if i := strings.Index(lines[len(lines)-2], "14:08"); i >= 0 || ansi.StringWidth(timed[0]) != 60-2 {
		t.Errorf("time on a wrapped line, or not at the right: %q", lines)
	}
	if narrow, _, _ := renderChat(new(chatCache), entries, 25, 10, 0); strings.Contains(ansi.Strip(narrow), "14:0") {
		t.Errorf("a narrow chat shows times:\n%s", ansi.Strip(narrow))
	}
}

// A long line of code wraps beside the bar, as the prose does, and keeps all its text: cut, a
// draft to send or a command lost its end.
func TestDrawMarkdownCodeWraps(t *testing.T) {
	line := "олег, предлагаю разбить паритет на 7 подисторий supercalifragilisticexpialidocious 😀😀 end"
	for _, w := range []int{5, 9, 24, 40} {
		lines := drawMarkdown("```\n"+line+"\n```", w)
		var got strings.Builder
		for _, l := range lines {
			l = ansi.Strip(l)
			if ansi.StringWidth(l) > w || !strings.HasPrefix(l, "│ ") {
				t.Errorf("w %d: line %q", w, l)
			}
			got.WriteString(strings.TrimPrefix(l, "│ "))
		}
		if strings.ReplaceAll(got.String(), " ", "") != strings.ReplaceAll(line, " ", "") {
			t.Errorf("w %d: the code lost text: %q", w, got.String())
		}
		if len(lines) < 2 {
			t.Errorf("w %d: the code did not wrap: %q", w, lines)
		}
	}
	// A comment or a string goes on in its colour past the wrap: its words are not code there.
	bar := styleRule.Render("│ ")
	sgr := func(s string) string { return s[:strings.IndexByte(s, 'm')+1] }
	for _, c := range []struct{ line, rest string }{
		{"x := 1 // if the value is nil we return early from func", sgr(ideview.Highlight("// c", "go"))},
		{`s := "for range if return else go func"`, sgr(ideview.Highlight(`"s"`, "go"))},
	} {
		lines := drawMarkdown("```go\n"+c.line+"\n```", 20)
		if len(lines) < 3 {
			t.Fatalf("%s: did not wrap: %q", c.line, lines)
		}
		for _, l := range lines[1:] {
			part := strings.TrimPrefix(l, bar)
			if !strings.HasPrefix(part, c.rest) || strings.Count(part, "\x1b[38") != 1 {
				t.Errorf("%s: a part not in the colour it goes on in: %q", c.line, l)
			}
		}
	}
	// Too narrow to wrap, a line is cut as before, not spread over lines of nothing.
	if lines := drawMarkdown("```\nabc def\n```", 3); len(lines) != 1 {
		t.Errorf("w 3: %q", lines)
	}
	if lines := drawMarkdown("```\nab cd\n```", 4); len(lines) != 2 {
		t.Errorf("w 4: %q", lines)
	}
}
