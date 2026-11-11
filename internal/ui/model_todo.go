package ui

import (
	"cmp"
	"image"
	"regexp"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/protocol"
	"github.com/pixelbender/claudit/internal/sessions"
	"github.com/pixelbender/claudit/internal/theme"
)

// Claude Code's task tools keep a list of what is to be done: TaskCreate adds a task, TaskUpdate
// changes or deletes one, TaskList tells them all (CLAUDE_CODE_ENABLE_TODO_TOOLS turns them on
// under claude -p). The list is the session's: the subagents' calls go to it as main's do. TodoWrite,
// the tool of the models before them, writes the whole list of the agent calling it each time:
// claudit follows main's. It shows the list above the prompt.

// todo is the session's task list, as the calls left it.
type todo struct {
	items   []todoItem
	open    bool // ctrl+t: a row per task in place of the one line
	partial bool // a task changed that the list does not hold: how many there are is not known
}

type todoItem struct {
	id      string // the number TaskCreate gave it; "w" and its place in TodoWrite's list
	subject string // "" for a task the list missed: it is shown by its id
	active  string // what it says while in progress, "Running tests"; "" for its subject
	status  string // "pending", "in_progress" or "completed"
}

// todoCreated is how TaskCreate's result starts; Claude Code itself reads the task's id from it
// to rebuild the list of a resumed conversation.
var todoCreated = regexp.MustCompile(`^Task #(\S+) created successfully`)

// todoUpdated starts the result of a TaskUpdate that changed its task; one that did not ("Task
// #9 not found") changes nothing here either.
const todoUpdated = "Updated task #"

// todoListed is a line of TaskList's result: "#4 [pending] Ship it (owner) [blocked by #5]".
var todoListed = regexp.MustCompile(`^#(\S+) \[(pending|in_progress|completed)\] (.*?)(?: \[blocked by [^\]]*\])?$`)

// todoTools are the tools whose calls keep the list.
var todoTools = []string{"TaskCreate", "TaskUpdate", "TaskList", "TodoWrite"}

// todoResult applies a task call that went through to the list. It runs before the result ends
// the call, which is what tells the call's input.
func (m *Model) todoResult(ev protocol.ToolResult) {
	call, ok := m.crew.calls[ev.ID]
	if !ok || ev.IsError || call.Name == "TodoWrite" && call.Parent != "" { // a subagent's TodoWrite list is its own
		return
	}
	m.todo.apply(call.Name, call.Input, ev.Output)
}

// todoResumed rebuilds the list from the calls of a resumed conversation.
func (m *Model) todoResumed(calls []sessions.Call) {
	m.todo = todo{}
	for _, c := range calls {
		if !c.Error {
			m.todo.apply(c.Name, c.Input, c.Output)
		}
	}
}

// apply changes the list as a call of tool name with input in did, by its result out.
func (t *todo) apply(name string, in map[string]any, out string) {
	switch name {
	case "TaskCreate":
		s := todoCreated.FindStringSubmatch(out)
		if s == nil {
			return
		}
		it := todoItem{id: s[1], subject: inputString(in, "subject"), active: inputString(in, "activeForm"), status: "pending"}
		if i := t.find(it.id); i >= 0 {
			t.items[i] = it
		} else {
			t.items = append(t.items, it)
		}
	case "TaskUpdate":
		// Claude Code takes "id" and "task_id" for "taskId", "active_form" for "activeForm".
		id := cmp.Or(todoID(in, "taskId"), todoID(in, "id"), todoID(in, "task_id"))
		if id == "" || !strings.HasPrefix(out, todoUpdated) {
			return
		}
		i := t.find(id)
		if i < 0 { // one the list missed: it is shown as far as the call tells it
			t.partial = true
			t.items = append(t.items, todoItem{id: id, status: "pending"})
			i = len(t.items) - 1
		}
		it := &t.items[i]
		it.subject = cmp.Or(inputString(in, "subject"), it.subject)
		it.active = cmp.Or(inputString(in, "activeForm"), inputString(in, "active_form"), it.active)
		switch s := inputString(in, "status"); s {
		case "deleted":
			t.items = slices.Delete(t.items, i, i+1)
		case "pending", "in_progress", "completed":
			it.status = s
		}
	case "TaskList": // the whole list: what it held of each task stays, but for the status
		var items []todoItem
		for l := range strings.Lines(out) {
			s := todoListed.FindStringSubmatch(strings.TrimRight(l, "\n"))
			if s == nil {
				continue
			}
			it := todoItem{id: s[1]}
			if i := t.find(it.id); i >= 0 {
				it = t.items[i]
			}
			it.subject, it.status = cmp.Or(it.subject, s[3]), s[2] // the line may add the owner to the subject
			items = append(items, it)
		}
		if len(items) == 0 && strings.TrimSpace(out) != "No tasks found" {
			return // not a list it reads
		}
		t.items, t.partial = items, false
	case "TodoWrite":
		todos, _ := in["todos"].([]any)
		t.items, t.partial = nil, false
		for i, td := range todos {
			o, _ := td.(map[string]any)
			t.items = append(t.items, todoItem{id: "w" + strconv.Itoa(i+1),
				subject: inputString(o, "content"), active: inputString(o, "activeForm"), status: inputString(o, "status")})
		}
	}
	if len(t.items) == 0 {
		t.open, t.partial = false, false
	}
}

// label is what the task is called: its subject, else its id.
func (t todoItem) label() string { return cmp.Or(t.subject, "#"+t.id) }

// todoID reads a task id of a tool input: a string, or a number the model wrote for one.
func todoID(in map[string]any, key string) string {
	if n, ok := in[key].(float64); ok {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return inputString(in, key)
}

// find is the index of the task with that id; -1 for none.
func (t *todo) find(id string) int {
	return slices.IndexFunc(t.items, func(it todoItem) bool { return it.id == id })
}

// finished reports whether there is a list and every task of it is done.
func (t *todo) finished() bool {
	return len(t.items) > 0 && !slices.ContainsFunc(t.items, func(it todoItem) bool { return it.status != "completed" })
}

// todoTurn starts a turn for the list: a finished one has been seen done, and goes. A message sent
// counts as a turn even when no model call follows (a slash command claude runs by itself) or
// claude takes it into the turn at work (a prompt queued while it works): the line was seen.
func (m *Model) todoTurn() {
	if m.todo.finished() {
		m.todo = todo{}
	}
}

// currentTask is what the task list says is being done: the first task in progress, in the words
// it gives for that ("Running tests"), else its subject, "" between tasks; and how many of how
// many tasks are done. done and total are 0 when there is no list or how many tasks it has is not
// known.
func (m *Model) currentTask() (text string, done, total int) {
	for _, t := range m.todo.items {
		switch {
		case t.status == "completed":
			done++
		case t.status == "in_progress" && text == "":
			text = cmp.Or(t.active, t.label())
		}
	}
	if m.todo.partial {
		return drawOneLine(text), 0, 0
	}
	return drawOneLine(text), done, len(m.todo.items)
}

// todoMaxRows is how many tasks the open list shows at most; past them rows count the rest.
// todoIDEKeep is how low the list's rows make the IDE at most.
const (
	todoMaxRows = 10
	todoIDEKeep = drawIDEH / 2
)

// drawTodo is the task list above the prompt's frame, w wide and at most rows high: one line,
// or with ctrl+t that line over a row per task; nothing without a list.
func (m *Model) drawTodo(w, rows int) []string {
	items := m.todo.items
	if len(items) == 0 || rows < 1 {
		return nil
	}
	text, done, total := m.currentTask()
	var count []string
	if !m.todo.partial {
		count = []string{strconv.Itoa(done) + "/" + strconv.Itoa(total)}
	}
	hint := "ctrl+t list"
	if m.todo.open {
		hint = "ctrl+t hide"
	}
	var head string
	if m.todo.finished() {
		meta := append(count, "done")
		if m.todo.open {
			meta[len(meta)-1] += " · " + hint
		}
		head = styleOK.Render("✔ ") + styleDim.Render(strings.Join(meta, " "))
	} else {
		meta := count
		if i := slices.IndexFunc(items, func(t todoItem) bool { return t.status == "pending" }); i >= 0 {
			meta = append(meta, "next: "+drawOneLine(items[i].label()))
		}
		head = styleAccent.Render("✻ ")
		if text != "" {
			head += todoStrong.Render(text) + styleDim.Render(" · ")
		}
		head += styleDim.Render(strings.Join(append(meta, hint), " · "))
	}
	out := []string{todoLine(head, w)}
	if !m.todo.open {
		return out
	}
	from, shown, above, below := todoWindow(items, min(todoMaxRows+1, rows-1))
	var body []string
	if above {
		if slices.ContainsFunc(items[:from], func(t todoItem) bool { return t.status != "completed" }) {
			body = append(body, styleDim.Render("… +"+strconv.Itoa(from)+" more"))
		} else {
			body = append(body, styleOK.Render("✔ ")+styleDim.Render("+"+strconv.Itoa(from)+" done"))
		}
	}
	for _, t := range items[from : from+shown] {
		switch s := drawOneLine(t.label()); t.status {
		case "completed":
			body = append(body, styleOK.Render("✔ ")+todoDone.Render(s))
		case "in_progress":
			body = append(body, styleAccent.Render("◼ ")+todoStrong.Render(s))
		default:
			body = append(body, styleDim.Render("☐ "+s))
		}
	}
	if below {
		body = append(body, styleDim.Render("… +"+strconv.Itoa(len(items)-from-shown)+" more"))
	}
	for i, row := range body {
		lead := "  " // under the head's mark
		if i == 0 {
			lead = "└ "
		}
		out = append(out, todoLine(styleDim.Render(lead)+row, w))
	}
	return out
}

// todoWindow picks the tasks the open list shows in room rows: shown of them from from, with a
// row for those left out above and one for those below when there are any and room for it. It
// starts at the task before the first not done, what is past being the least to say, and keeps
// the first task in progress.
func todoWindow(items []todoItem, room int) (from, shown int, above, below bool) {
	n := len(items)
	switch {
	case room < 1:
		return 0, 0, false, false
	case n <= min(todoMaxRows, room):
		return 0, n, false, false
	}
	anchor := slices.IndexFunc(items, func(t todoItem) bool { return t.status != "completed" })
	if anchor < 0 {
		anchor = n
	}
	keep := slices.IndexFunc(items, func(t todoItem) bool { return t.status == "in_progress" })
	if keep < 0 {
		keep = min(anchor, n-1)
	}
	for shown = min(todoMaxRows, room); ; shown-- {
		from = max(0, min(anchor-1, n-shown))
		if keep >= from+shown {
			from = keep - shown + 1
		}
		above, below = from > 0, from+shown < n
		if extra := b2i(above) + b2i(below); shown+extra <= room || shown == 1 {
			break
		}
	}
	if shown+b2i(above)+b2i(below) > room { // one task row and room for one more at most: the rest below says more
		above = above && room >= 2+b2i(below)
		below = below && room >= 2
	}
	return from, shown, above, below
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

var (
	todoStrong = lipgloss.NewStyle().Foreground(theme.Bright).Bold(true) // the task at work
	todoDone   = styleDim.Strikethrough(true)
)

// todoLine indents s under the prompt's text and cuts it short of the frame's right edge.
func todoLine(s string, w int) string {
	return drawFit(ansi.Truncate("  "+s, max(0, w-2), "…"), w)
}

// todoZone is where a click on the task rows, y from the top and h of them, opens or closes the
// list as the button lets go; a drag over them selects their text instead.
func (m *Model) todoZone(y, w, h int) zone {
	return zone{y: y, w: w, h: h,
		click: func(int, int) tea.Cmd {
			m.PanelFocus = false
			m.onRelease = func() { m.todo.open = !m.todo.open && len(m.todo.items) > 0 }
			return nil
		},
		text: func(int, int) image.Rectangle { return image.Rect(2, 0, w-2, h) }}
}
