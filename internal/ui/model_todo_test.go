package ui

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/protocol"
)

// todoCall makes a task tool call of main's with the id and its result out.
func todoCall(m *Model, id, name string, in map[string]any, out string) {
	modelEvents(m, protocol.ToolUse{ID: id, Name: name, Input: in}, protocol.ToolResult{ID: id, Output: out})
}

// todoCreate creates a task per subject, numbered from 1 as Claude Code does, each saying it is
// at work on it as "<subject>ing".
func todoCreate(m *Model, subjects ...string) {
	for _, s := range subjects {
		n := strconv.Itoa(len(m.todo.items) + 1)
		todoCall(m, "c"+n, "TaskCreate", map[string]any{"subject": s, "description": "do " + s, "activeForm": s + "ing"},
			"Task #"+n+" created successfully: "+s)
	}
}

// todoSet moves task id to status.
func todoSet(m *Model, id, status string) {
	todoCall(m, "u"+id+status, "TaskUpdate", map[string]any{"taskId": id, "status": status}, "Updated task #"+id+" status")
}

// todoList is the list as "id status subject (active)".
func todoList(m *Model) []string {
	var out []string
	for _, t := range m.todo.items {
		out = append(out, fmt.Sprintf("%s %s %s (%s)", t.id, t.status, t.subject, t.active))
	}
	return out
}

// todoRows is drawTodo's rows without their styles and trailing blanks.
func todoRows(m *Model, w, rows int) []string {
	return drawPlainLines(strings.Join(m.drawTodo(w, rows), "\n"))
}

// The stream as claude -p writes it: TaskCreate tells the id it gave in its result, and
// TaskUpdate names the task by it.
func TestTodoFromStream(t *testing.T) {
	m, _ := modelNew(t, false)
	create := func(id, n, subject, active string) {
		modelEvents(m, protocol.Parse([]byte(`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"tool_use","id":"`+id+
			`","name":"TaskCreate","input":{"subject":"`+subject+`","description":"…","activeForm":"`+active+`"}}]}}`))...)
		modelEvents(m, protocol.Parse([]byte(`{"type":"user","parent_tool_use_id":null,"message":{"content":[{"tool_use_id":"`+id+
			`","type":"tool_result","content":"Task #`+n+` created successfully: `+subject+`"}]},"tool_use_result":{"task":{"id":"`+n+`","subject":"`+subject+`"}}}`))...)
	}
	create("t1", "7", "Find foo", "Finding foo")
	create("t2", "8", "Rename foo", "Renaming foo")
	modelEvents(m, protocol.Parse([]byte(`{"type":"assistant","parent_tool_use_id":null,"message":{"content":[{"type":"tool_use","id":"t3","name":"TaskUpdate","input":{"taskId":"7","status":"in_progress"}}]}}`))...)
	modelEvents(m, protocol.Parse([]byte(`{"type":"user","parent_tool_use_id":null,"message":{"content":[{"tool_use_id":"t3","type":"tool_result","content":"Updated task #7 status"}]},"tool_use_result":{"success":true,"taskId":"7","updatedFields":["status"],"statusChange":{"from":"pending","to":"in_progress"}}}`))...)
	if got, want := todoList(m), []string{"7 in_progress Find foo (Finding foo)", "8 pending Rename foo (Renaming foo)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("list %q, want %q", got, want)
	}
	if text, done, total := m.currentTask(); text != "Finding foo" || done != 0 || total != 2 {
		t.Errorf("current task %q %d/%d", text, done, total)
	}
}

func TestTodoUpdate(t *testing.T) {
	m, _ := modelNew(t, false)
	todoCreate(m, "read", "write", "test", "ship")
	todoSet(m, "1", "completed")
	todoSet(m, "2", "in_progress")
	todoSet(m, "4", "deleted")
	todoCall(m, "u3", "TaskUpdate", map[string]any{"id": "3", "subject": "check", "active_form": "checking"}, "Updated task #3 subject, activeForm")
	todoCall(m, "u2", "TaskUpdate", map[string]any{"taskId": "2", "activeForm": "writing it"}, "Updated task #2 activeForm")
	// What did not go through changes nothing: an unknown task, a failed call, a refused one.
	todoCall(m, "u9", "TaskUpdate", map[string]any{"taskId": "9", "status": "completed"}, "Task not found")
	todoCall(m, "u1", "TaskUpdate", map[string]any{"taskId": "1", "status": "pending"}, "Task #1 not found")
	modelEvents(m, protocol.ToolUse{ID: "e1", Name: "TaskUpdate", Input: map[string]any{"taskId": "3", "status": "completed"}},
		protocol.ToolResult{ID: "e1", Output: "Updated task #3 status", IsError: true})
	todoCall(m, "c9", "TaskCreate", map[string]any{"subject": "lost"}, "Error: no task list")
	want := []string{"1 completed read (reading)", "2 in_progress write (writing it)", "3 pending check (checking)"}
	if got := todoList(m); !reflect.DeepEqual(got, want) {
		t.Errorf("list %q, want %q", got, want)
	}
	if text, done, total := m.currentTask(); text != "writing it" || done != 1 || total != 3 {
		t.Errorf("current task %q %d/%d", text, done, total)
	}
	todoSet(m, "2", "completed")
	if text, done, _ := m.currentTask(); text != "" || done != 2 {
		t.Errorf("between tasks: %q, %d done", text, done)
	}
	// A task in progress without words for it is said by its subject.
	todoCall(m, "c4", "TaskCreate", map[string]any{"subject": "tag"}, "Task #4 created successfully: tag")
	todoSet(m, "4", "in_progress")
	if text, _, _ := m.currentTask(); text != "tag" {
		t.Errorf("current task %q, want its subject", text)
	}
	// An id given again names a new task in place of the old one.
	todoCall(m, "c1", "TaskCreate", map[string]any{"subject": "redo"}, "Task #1 created successfully: redo")
	if got := todoList(m); len(got) != 4 || got[0] != "1 pending redo ()" {
		t.Errorf("an id given again: %q", got)
	}
}

func TestTodoWrite(t *testing.T) {
	m, _ := modelNew(t, false)
	todoCreate(m, "old")
	write := func(todos ...map[string]any) {
		var list []any
		for _, td := range todos {
			list = append(list, td)
		}
		todoCall(m, "w"+strconv.Itoa(len(list)), "TodoWrite", map[string]any{"todos": list}, "Todos have been modified successfully")
	}
	write(map[string]any{"content": "Plan", "status": "completed", "activeForm": "Planning"},
		map[string]any{"content": "Build", "status": "in_progress", "activeForm": "Building"},
		map[string]any{"content": "Test", "status": "pending", "activeForm": "Testing"})
	if got, want := todoList(m), []string{"w1 completed Plan (Planning)", "w2 in_progress Build (Building)", "w3 pending Test (Testing)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("list %q, want %q", got, want)
	}
	write(map[string]any{"content": "Ship", "status": "in_progress", "activeForm": "Shipping"})
	if got, want := todoList(m), []string{"w1 in_progress Ship (Shipping)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("each call writes the whole list: %q, want %q", got, want)
	}
	write()
	if len(m.todo.items) != 0 || m.drawTodo(80, 10) != nil {
		t.Errorf("an empty list shows: %q", todoList(m))
	}
}

// Claude Code keeps one task list for the session, its subagents' calls go to it too: the tasks
// they create join main's list and their updates change it. TodoWrite's list is each agent's own.
func TestTodoSubagent(t *testing.T) {
	m, _ := modelNew(t, false)
	todoCreate(m, "main's")
	modelEvents(m, protocol.ToolUse{ID: "a1", Name: "Agent", Input: map[string]any{"description": "explore"}},
		protocol.ToolUse{ID: "s1", Name: "TaskCreate", Parent: "a1", Input: map[string]any{"subject": "its"}},
		protocol.ToolResult{ID: "s1", Output: "Task #2 created successfully: its"},
		protocol.ToolUse{ID: "s2", Name: "TaskUpdate", Parent: "a1", Input: map[string]any{"taskId": "1", "status": "completed"}},
		protocol.ToolResult{ID: "s2", Output: "Updated task #1 status"},
		protocol.ToolUse{ID: "s3", Name: "TodoWrite", Parent: "a1", Input: map[string]any{"todos": []any{}}},
		protocol.ToolResult{ID: "s3", Output: "Todos have been modified successfully"})
	if got, want := todoList(m), []string{"1 completed main's (main'sing)", "2 pending its ()"}; !reflect.DeepEqual(got, want) {
		t.Errorf("list %q, want %q", got, want)
	}
}

func TestTodoLine(t *testing.T) {
	m, _ := modelNew(t, false)
	if rows := m.drawTodo(80, 10); rows != nil {
		t.Errorf("no list draws %q", rows)
	}
	todoCreate(m, "read", "write", "test")
	todoSet(m, "1", "completed")
	todoSet(m, "2", "in_progress")
	if got, want := todoRows(m, 80, 10), []string{"  ✻ writeing · 1/3 · next: test · ctrl+t list"}; !reflect.DeepEqual(got, want) {
		t.Errorf("line %q, want %q", got, want)
	}
	todoSet(m, "2", "completed")
	if got, want := todoRows(m, 80, 10), []string{"  ✻ 2/3 · next: test · ctrl+t list"}; !reflect.DeepEqual(got, want) {
		t.Errorf("between tasks: %q, want %q", got, want)
	}
	todoSet(m, "3", "in_progress")
	if got, want := todoRows(m, 80, 10), []string{"  ✻ testing · 2/3 · ctrl+t list"}; !reflect.DeepEqual(got, want) {
		t.Errorf("last task: %q, want %q", got, want)
	}
	// Cut short of the frame's edge, every row exactly as wide as the screen.
	todoCall(m, "u3", "TaskUpdate", map[string]any{"taskId": "3", "activeForm": strings.Repeat("слово ", 20)}, "Updated task #3 activeForm")
	rows := m.drawTodo(40, 10)
	if len(rows) != 1 || ansi.StringWidth(rows[0]) != 40 {
		t.Fatalf("rows %q", rows)
	}
	if got := strings.TrimRight(ansi.Strip(rows[0]), " "); got != "  ✻ "+strings.Repeat("слово ", 5)+"сло…" {
		t.Errorf("cut line %q", got)
	}
}

// A finished list says so until the next turn starts: the prompt sent, or a turn claude starts
// by itself or a replay plays.
func TestTodoDone(t *testing.T) {
	for _, replay := range []bool{false, true} {
		m, _ := modelNew(t, replay)
		modelEvents(m, protocol.Init{SessionID: "s"}, protocol.Thinking{})
		todoCreate(m, "a", "b")
		todoSet(m, "1", "completed")
		todoSet(m, "2", "completed")
		modelEvents(m, protocol.TextStart{}, protocol.TextDelta{Text: "done"}, protocol.Result{})
		if got, want := todoRows(m, 80, 10), []string{"  ✔ 2/2 done"}; !reflect.DeepEqual(got, want) {
			t.Errorf("replay %v: finished %q, want %q", replay, got, want)
		}
		if replay {
			modelEvents(m, protocol.Thinking{})
		} else {
			modelPress(m, inputTyped("next")...)
			modelPress(m, inputKey("enter"))
		}
		if rows := m.drawTodo(80, 10); rows != nil {
			t.Errorf("replay %v: the next turn keeps %q", replay, rows)
		}
	}
	// One not done yet stays.
	m, _ := modelNew(t, false)
	todoCreate(m, "a", "b")
	todoSet(m, "1", "completed")
	modelPress(m, inputTyped("go on")...)
	modelPress(m, inputKey("enter"))
	if _, done, total := m.currentTask(); done != 1 || total != 2 {
		t.Errorf("the next turn took an unfinished list: %d/%d", done, total)
	}
}

func TestTodoToggle(t *testing.T) {
	m, _ := modelNew(t, false)
	modelPress(m, inputKey("ctrl+t"))
	if m.todo.open {
		t.Error("ctrl+t opens no list where there is none")
	}
	todoCreate(m, "read", "write", "test")
	todoSet(m, "1", "completed")
	todoSet(m, "2", "in_progress")
	modelPress(m, inputKey("ctrl+t"))
	want := []string{"  ✻ writeing · 1/3 · next: test · ctrl+t hide", "  └ ✔ read", "    ◼ write", "    ☐ test"}
	if got := todoRows(m, 80, 10); !reflect.DeepEqual(got, want) {
		t.Errorf("list %q, want %q", got, want)
	}
	if len(m.Input.Text) != 0 {
		t.Errorf("ctrl+t typed %q", string(m.Input.Text))
	}
	modelPress(m, inputKey("ctrl+t"))
	if got := todoRows(m, 80, 10); len(got) != 1 {
		t.Errorf("ctrl+t again: %q", got)
	}
}

// Past ten tasks, or what the screen has room for, the rows count what they leave out: the tasks
// done above, the rest below. The window starts at the task before the first not done and keeps
// the task in progress.
func TestTodoRowLimit(t *testing.T) {
	m, _ := modelNew(t, false)
	for i := range 14 {
		todoCreate(m, "t"+strconv.Itoa(i+1))
	}
	for i := range 3 {
		todoSet(m, strconv.Itoa(i+1), "completed")
	}
	m.todo.open = true
	got := todoRows(m, 80, 40)
	if len(got) != 12 || got[1] != "  └ ✔ +2 done" || got[2] != "    ✔ t3" || got[3] != "    ☐ t4" || got[10] != "    ☐ t11" || got[11] != "    … +3 more" {
		t.Errorf("14 tasks: %q", got)
	}
	got = todoRows(m, 80, 4)
	if want := []string{"  ✻ 3/14 · next: t4 · ctrl+t hide", "  └ ✔ +3 done", "    ☐ t4", "    … +10 more"}; !reflect.DeepEqual(got, want) {
		t.Errorf("4 rows: %q, want %q", got, want)
	}
	if got := todoRows(m, 80, 1); len(got) != 1 {
		t.Errorf("1 row: %q", got)
	}
	if got := todoRows(m, 80, 2); len(got) != 2 || got[1] != "  └ ☐ t4" {
		t.Errorf("2 rows: %q", got)
	}
	// The task in progress stays in the window, far down as it is.
	todoSet(m, "12", "in_progress")
	got = todoRows(m, 80, 40)
	if len(got) != 12 || got[1] != "  └ ✔ +3 done" || got[2] != "    ☐ t4" || got[10] != "    ◼ t12" || got[11] != "    … +2 more" {
		t.Errorf("in progress far down: %q", got)
	}
	// Left out above and not all done: they are counted as more.
	for i := range 3 {
		todoSet(m, strconv.Itoa(i+1), "pending")
	}
	todoSet(m, "12", "pending")
	todoSet(m, "14", "in_progress")
	if got = todoRows(m, 80, 40); len(got) != 12 || got[1] != "  └ … +4 more" || got[2] != "    ☐ t5" || got[11] != "    ◼ t14" {
		t.Errorf("in progress last: %q", got)
	}
	// All done: the last ones.
	for i := range 14 {
		todoSet(m, strconv.Itoa(i+1), "completed")
	}
	if got = todoRows(m, 80, 40); len(got) != 12 || got[0] != "  ✔ 14/14 done · ctrl+t hide" || got[1] != "  └ ✔ +4 done" || got[2] != "    ✔ t5" || got[11] != "    ✔ t14" {
		t.Errorf("all done: %q", got)
	}
}

// The task rows sit right above the prompt's frame; the IDE gives them up, so that the
// conversation keeps what it shows, and takes them back as the list goes.
func TestTodoView(t *testing.T) {
	const w, h = 100, 40
	m := drawTestModel(t, w, h)
	m.crew, m.ideHeight = newCrew(), -1
	m.Chat = nil
	for i := range 30 {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: "line " + strconv.Itoa(i)})
	}
	before := drawPlainLines(m.View().Content)
	todoCreate(m, "read", "write")
	todoSet(m, "1", "in_progress")
	modelEvents(m, protocol.Result{}) // no caret under the chat
	modelPress(m, inputKey("ctrl+t"))
	v := m.View()
	drawCheckSize(t, v.Content, w, h)
	lines := drawPlainLines(v.Content)
	if v.Cursor == nil || v.Cursor.Y != h-3 || !strings.HasPrefix(lines[h-3], "│ › rewrite") {
		t.Fatalf("the prompt moved: cursor %v, rows %q", v.Cursor, lines[h-5:])
	}
	if got, want := lines[h-7:h-4], []string{"  ✻ reading · 0/2 · next: write · ctrl+t hide", "  └ ◼ read", "    ☐ write"}; !reflect.DeepEqual(got, want) {
		t.Errorf("above the frame %q, want %q", got, want)
	}
	// The chat, 35% of the screen, is as it was, right above the list.
	chatH := h * drawChatShare / 100
	if got, was := lines[h-7-chatH:h-7], before[h-4-chatH:h-4]; !reflect.DeepEqual(got, was) {
		t.Errorf("chat %q, was %q", got, was)
	}
	if before[0] != lines[0] {
		t.Errorf("IDE top %q, was %q", lines[0], before[0])
	}
	modelPress(m, inputKey("ctrl+t"))
	modelEvents(m, protocol.Reset{})
	if after := drawPlainLines(m.View().Content); after[h-5] == lines[h-7] || strings.Contains(strings.Join(after, "\n"), "✻") {
		t.Errorf("the list stays after /clear: %q", after[h-6:h-4])
	}
}

// Too low for the IDE, the screen takes the list's rows from the conversation.
func TestTodoViewLow(t *testing.T) {
	const w, h = 60, 12
	m := drawTestModel(t, w, h)
	m.crew = newCrew()
	todoCreate(m, "read", "write")
	modelEvents(m, protocol.Result{})
	m.todo.open = true
	v := m.View()
	drawCheckSize(t, v.Content, w, h)
	lines := drawPlainLines(v.Content)
	if v.Cursor == nil || v.Cursor.Y != h-3 || lines[h-5] != "    ☐ write" || lines[h-6] != "  └ ☐ read" {
		t.Errorf("cursor %v, rows %q", v.Cursor, lines)
	}
}

func TestTodoForgotten(t *testing.T) {
	m, _ := modelNew(t, false)
	modelEvents(m, protocol.Init{SessionID: "s"})
	todoCreate(m, "a")
	m.todo.open = true
	modelEvents(m, protocol.Reset{})
	if len(m.todo.items) != 0 || m.todo.open {
		t.Errorf("/clear keeps %q", todoList(m))
	}
}

// /resume rebuilds the list from the calls the conversation saved, main's whose result says they
// went through.
func TestTodoResume(t *testing.T) {
	m, _ := modelResumeNew(t)
	todoCreate(m, "this conversation's")
	modelEvents(m, protocol.Result{})
	use := func(id, name, input string) string {
		return `{"type":"assistant","message":{"model":"m","content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}`
	}
	result := func(id, out string) string {
		return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"` + out + `"}]}}`
	}
	modelResumeWrite(t, m, "t3", modelT0.Add(-time.Hour),
		`{"type":"user","message":{"role":"user","content":"plan it"}}`,
		use("c1", "TaskCreate", `{"subject":"Read","activeForm":"Reading"}`), result("c1", "Task #1 created successfully: Read"),
		use("c2", "TaskCreate", `{"subject":"Write"}`), result("c2", "Task #2 created successfully: Write"),
		use("c3", "TaskCreate", `{"subject":"Lost"}`), result("c3", "Error: no list"),
		use("u1", "TaskUpdate", `{"taskId":"1","status":"completed"}`), result("u1", "Updated task #1 status"),
		use("u2", "TaskUpdate", `{"taskId":"2","status":"in_progress","activeForm":"Writing"}`), result("u2", "Updated task #2 status, activeForm"),
		use("u3", "TaskUpdate", `{"taskId":"2","status":"deleted"}`),
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u3","content":"Updated task #2 status","is_error":true}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"model":"m","content":[{"type":"tool_use","id":"x1","name":"TaskUpdate","input":{"taskId":"2","status":"completed"}}]}}`,
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x1","content":"Updated task #2 status"}]}}`,
		`{"type":"ai-title","aiTitle":"Tasks"}`)
	modelResumeSubmit(m, "/resume t3")
	if got, want := todoList(m), []string{"1 completed Read (Reading)", "2 in_progress Write (Writing)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rebuilt %q, want %q", got, want)
	}
	// A conversation without task calls has no list.
	modelResumeSubmit(m, "/resume a1")
	if len(m.todo.items) != 0 {
		t.Errorf("a1 kept %q", todoList(m))
	}
}

// An update of a task the list does not hold (a resumed conversation's, from before what it
// saved) makes the counts unknown: the line leaves them out until a TaskList tells the whole list.
func TestTodoUnknownTask(t *testing.T) {
	m, _ := modelNew(t, false)
	todoCreate(m, "read")
	todoCall(m, "u5", "TaskUpdate", map[string]any{"taskId": "5", "status": "in_progress", "activeForm": "Fixing"}, "Updated task #5 status, activeForm")
	if got, want := todoList(m), []string{"1 pending read (reading)", "5 in_progress  (Fixing)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("list %q, want %q", got, want)
	}
	if got, want := todoRows(m, 80, 10), []string{"  ✻ Fixing · next: read · ctrl+t list"}; !reflect.DeepEqual(got, want) {
		t.Errorf("line %q, want %q", got, want)
	}
	if text, done, total := m.currentTask(); text != "Fixing" || done != 0 || total != 0 {
		t.Errorf("current task %q %d/%d, want no counts", text, done, total)
	}
	m.todo.open = true
	if got := todoRows(m, 80, 10); len(got) != 3 || got[2] != "    ◼ #5" {
		t.Errorf("rows %q: the task missed shows by its id", got)
	}
	m.todo.open = false
	todoSet(m, "1", "completed")
	todoSet(m, "5", "completed")
	if got, want := todoRows(m, 80, 10), []string{"  ✔ done"}; !reflect.DeepEqual(got, want) {
		t.Errorf("finished %q, want %q", got, want)
	}
	todoCall(m, "l1", "TaskList", map[string]any{}, "#1 [completed] read\n#4 [pending] Ship it [blocked by #5]\n#5 [in_progress] Fix the bug (kevin)")
	if got, want := todoList(m), []string{"1 completed read (reading)", "4 pending Ship it ()", "5 in_progress Fix the bug (kevin) (Fixing)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after TaskList %q, want %q", got, want)
	}
	if _, done, total := m.currentTask(); done != 1 || total != 3 {
		t.Errorf("after TaskList %d/%d", done, total)
	}
	todoCall(m, "l9", "TaskList", map[string]any{}, "Tasks are kept elsewhere")
	if _, _, total := m.currentTask(); total != 3 {
		t.Errorf("a TaskList result it does not read changed the list: %q", todoList(m))
	}
	todoCall(m, "l2", "TaskList", map[string]any{}, "No tasks found")
	if len(m.todo.items) != 0 {
		t.Errorf("no tasks: %q", todoList(m))
	}
}

// A number for the task's id names it as the string does.
func TestTodoNumericID(t *testing.T) {
	m, _ := modelNew(t, false)
	todoCreate(m, "read", "write")
	todoCall(m, "u2", "TaskUpdate", map[string]any{"taskId": float64(2), "status": "in_progress"}, "Updated task #2 status")
	if got := todoList(m); got[1] != "2 in_progress write (writeing)" {
		t.Errorf("list %q", got)
	}
}

// The list closes as it empties: the next one comes as a line.
func TestTodoEmptyCloses(t *testing.T) {
	m, _ := modelNew(t, false)
	todoCreate(m, "a")
	m.todo.open = true
	todoSet(m, "1", "deleted")
	if m.todo.open {
		t.Error("an empty list stays open")
	}
	todoCreate(m, "b")
	if got := todoRows(m, 80, 10); len(got) != 1 {
		t.Errorf("the next list: %q", got)
	}
}

// What the model wrote is drawn without its escapes and control characters.
func TestTodoClean(t *testing.T) {
	m, _ := modelNew(t, false)
	todoCall(m, "c1", "TaskCreate", map[string]any{"subject": "a\x1b[31mb\x07c", "activeForm": "d\x1b[31me\x07f"}, "Task #1 created successfully: x")
	todoCall(m, "c2", "TaskCreate", map[string]any{"subject": "g\x1b[31mh\x07i"}, "Task #2 created successfully: x")
	todoSet(m, "1", "in_progress")
	m.todo.open = true
	for _, r := range m.drawTodo(80, 10) {
		if strings.Contains(r, "\x1b[31m") || strings.Contains(r, "\x07") {
			t.Errorf("row %q", r)
		}
	}
	if got, want := todoRows(m, 80, 10), []string{"  ✻ def · 0/2 · next: ghi · ctrl+t hide", "  └ ◼ abc", "    ☐ ghi"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
}

// A finished list goes with the prompts sent once claude's turn ends, and with the ! commands run
// meanwhile: both start a turn.
func TestTodoDoneQueued(t *testing.T) {
	for _, bang := range []bool{false, true} {
		m, _ := modelNew(t, false)
		modelEvents(m, protocol.Init{SessionID: "s"}, protocol.Thinking{})
		todoCreate(m, "a")
		todoSet(m, "1", "completed")
		if bang {
			m.bangSaid = []string{"! ls"}
		} else {
			modelPress(m, inputTyped("next")...)
			modelPress(m, inputKey("enter"))
		}
		if len(m.todo.items) == 0 {
			t.Fatalf("bang %v: gone before the turn ends", bang)
		}
		modelEvents(m, protocol.Result{})
		if len(m.todo.items) != 0 {
			t.Errorf("bang %v: the turn they start keeps %q", bang, todoList(m))
		}
	}
}

// The completions cover the task rows, right above the prompt's frame, and take their clicks.
func TestTodoSuggest(t *testing.T) {
	const w, h = 100, 40
	m := drawTestModel(t, w, h)
	m.crew, m.ideHeight = newCrew(), -1
	todoCreate(m, "read", "write")
	modelEvents(m, protocol.Result{})
	m.todo.open = true
	m.Input = Input{Text: []rune("/c"), Cursor: 2}
	m.Suggest = []protocol.Command{{Name: "clear"}}
	lines := drawPlainLines(m.View().Content)
	if !strings.Contains(lines[h-5], "╰") || strings.Contains(lines[h-5], "☐") {
		t.Errorf("above the frame %q", lines[h-7:h-4])
	}
	m.Update(tea.MouseClickMsg{X: 0, Y: h - 5, Button: tea.MouseLeft}) // beside the completions, on the rows they cover
	m.Update(tea.MouseReleaseMsg{X: 0, Y: h - 5, Button: tea.MouseLeft})
	if !m.todo.open {
		t.Error("a click on the covered rows closed the list")
	}
}

// The IDE gives the list its rows down to todoIDEKeep; the conversation gives the rest.
func TestTodoIDEKeep(t *testing.T) {
	const w, h = 100, 20
	m := drawTestModel(t, w, h)
	m.crew, m.ideHeight = newCrew(), -1
	m.Chat = nil
	for i := range 30 {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: "line " + strconv.Itoa(i)})
	}
	for i := range 14 {
		todoCreate(m, "t"+strconv.Itoa(i+1))
	}
	modelEvents(m, protocol.Result{})
	m.todo.open = true
	lines := drawPlainLines(m.View().Content)
	rows := h / 4
	chat := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "  › line") {
			chat++
		}
	}
	if ide := h - 4 - rows - chat; ide != todoIDEKeep || !strings.HasPrefix(lines[ide-1], "╰") {
		t.Errorf("IDE %d rows, chat %d, want the IDE at %d: %q", ide, chat, todoIDEKeep, lines)
	}
}

// The IDE height an interaction holds still is the one without the task rows: they come off it
// once each frame, not again in every frame after.
func TestTodoInspection(t *testing.T) {
	const w, h = 100, 40
	m := drawTestModel(t, w, h)
	m.crew, m.ideHeight = newCrew(), -1
	todoCreate(m, "read", "write")
	modelEvents(m, protocol.Result{})
	m.todo.open = true
	first := drawPlainLines(m.View().Content)
	modelPress(m, inputKey("x")) // an interaction: the IDE stays as it is
	if m.inspection == nil {
		t.Fatal("no inspection")
	}
	m.View()
	if again := drawPlainLines(m.View().Content); !reflect.DeepEqual(again[:5], first[:5]) || again[h-8] != first[h-8] {
		t.Errorf("the IDE moved:\n%q\nwas\n%q", again[h-10:h-4], first[h-10:h-4])
	}
}

// The list takes at most a quarter of the screen.
func TestTodoQuarter(t *testing.T) {
	const w, h = 100, 24
	m := drawTestModel(t, w, h)
	m.crew, m.ideHeight = newCrew(), -1
	for i := range 14 {
		todoCreate(m, "t"+strconv.Itoa(i+1))
	}
	modelEvents(m, protocol.Result{})
	m.todo.open = true
	lines := drawPlainLines(m.View().Content)
	n := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "    ☐") || strings.HasPrefix(l, "  └") || strings.HasPrefix(l, "    …") || strings.HasPrefix(l, "  ✻") {
			n++
		}
	}
	if n != h/4 {
		t.Errorf("%d rows of the list, want %d", n, h/4)
	}
}

// A click on the task line opens the list, as ctrl+t does; a click on the open list closes it.
func TestTodoClick(t *testing.T) {
	m := drawTestModel(t, 100, 40)
	m.crew, m.ideHeight = newCrew(), -1
	todoCreate(m, "read", "write")
	todoSet(m, "1", "in_progress")
	modelEvents(m, protocol.Result{})
	modelMouseClick(t, m, "next: write")
	if !m.todo.open {
		t.Fatal("a click on the line opens no list")
	}
	modelMouseClick(t, m, "☐ write")
	if m.todo.open {
		t.Error("a click on the open list leaves it open")
	}
	// It acts as the button lets go; a drag over the rows selects them and leaves the list as it is.
	x, y := modelMouseAt(t, m, "next: write")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if m.todo.open {
		t.Error("the press opened the list")
	}
	m.Update(tea.MouseMotionMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	if m.todo.open {
		t.Error("a drag opened the list")
	}
}

// Finished and open, the head still says how to close it.
func TestTodoDoneOpen(t *testing.T) {
	m, _ := modelNew(t, false)
	todoCreate(m, "a")
	todoSet(m, "1", "completed")
	m.todo.open = true
	if got, want := todoRows(m, 80, 10), []string{"  ✔ 1/1 done · ctrl+t hide", "  └ ✔ a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
}
