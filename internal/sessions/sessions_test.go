package sessions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// sessionsWrite writes a conversation file of lines and dates it at.
func sessionsWrite(t *testing.T, dir, id string, at time.Time, lines ...string) {
	t.Helper()
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestDir(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	cwd := filepath.Join(t.TempDir(), "my.project_x")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	real, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(config, "projects", strings.NewReplacer("/", "-", `\`, "-", ":", "-", ".", "-", "_", "-").Replace(real))
	if got := Dir(cwd); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	if !strings.HasSuffix(want, "-my-project-x") {
		t.Errorf("slug %q", want)
	}
	t.Run("symlink", func(t *testing.T) {
		if err := os.Symlink(cwd, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if got := Dir(link); got != want {
			t.Errorf("Dir(symlink) = %q, want %q", got, want)
		}
	})
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	sessionsWrite(t, dir, "old", t0,
		`{"type":"last-prompt","lastPrompt":"first prompt","sessionId":"old"}`,
		`{"parentUuid":null,"type":"user","message":{"role":"user","content":"a {\"type\":\"ai-title\"} lookalike"}}`,
		`{"type":"last-prompt","lastPrompt":"second prompt","sessionId":"old"}`,
		`{"type":"last-prompt","leafUuid":"u"}`)
	sessionsWrite(t, dir, "named", t0.Add(time.Hour),
		`{"type":"custom-title","customTitle":"My name","sessionId":"named"}`,
		`{"type":"ai-title","aiTitle":"Made up","sessionId":"named"}`,
		`{"type":"last-prompt","lastPrompt":"p","sessionId":"named"}`)
	sessionsWrite(t, dir, "ai", t0.Add(2*time.Hour),
		`{"type":"ai-title","aiTitle":"Made up first","sessionId":"ai"}`,
		`{"type":"ai-title","aiTitle":"Made up later","sessionId":"ai"}`,
		`{"type":"last-prompt","lastPrompt":"p","sessionId":"ai"}`)
	sessionsWrite(t, dir, "empty", t0.Add(3*time.Hour), `{"type":"queue-operation","operation":"enqueue"}`)
	if err := os.Mkdir(filepath.Join(dir, "sub.jsonl"), 0o755); err != nil { // subagent dirs and the like
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(`{"type":"last-prompt","lastPrompt":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		got[i].Modified = got[i].Modified.UTC()
	}
	want := []Info{
		{ID: "ai", Title: "Made up later", Modified: t0.Add(2 * time.Hour)},
		{ID: "named", Title: "My name", Modified: t0.Add(time.Hour)},
		{ID: "old", Title: "second prompt", Modified: t0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List =\n%+v\nwant\n%+v", got, want)
	}

	if list, err := List(filepath.Join(dir, "missing")); err != nil || list != nil {
		t.Errorf("missing dir: %v, %v", list, err)
	}
}

func TestHistory(t *testing.T) {
	dir := t.TempDir()
	sessionsWrite(t, dir, "s", time.Now(),
		`{"type":"queue-operation","operation":"enqueue","content":"queued"}`,
		`{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"add div"},"timestamp":"2026-10-30T07:00:14.347Z"}`,
		`{"type":"attachment","attachment":{"type":"date"}}`,
		`{"type":"assistant","isSidechain":false,"message":{"model":"m","content":[{"type":"thinking","thinking":"hm"}]}}`,
		`{"type":"assistant","isSidechain":false,"message":{"model":"m","content":[{"type":"text","text":"Reading calc.py."}]}}`,
		`{"type":"assistant","isSidechain":false,"message":{"model":"m","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}`,
		`{"type":"assistant","isSidechain":false,"message":{"model":"m","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"ls","description":"List the files"}}]}}`,
		`{"type":"assistant","isSidechain":false,"message":{"model":"m","content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"pwd"}}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"model":"m","content":[{"type":"tool_use","id":"t4","name":"Bash","input":{"command":"ls","description":"a subagent's"}}]}}`,
		`{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"model":"m","content":[{"type":"text","text":"a subagent"}]}}`,
		`{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>Caveat</local-command-caveat>"}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/model</command-name>\n  <command-message>model</command-message>\n  <command-args>haiku</command-args>"}}`,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout>Set model to haiku</local-command-stdout>"}`,
		`{"type":"system","subtype":"local_command","content":"/usage"}`,
		`{"type":"system","subtype":"local_command","content":"<local-command-stdout></local-command-stdout>"}`,
		`{"type":"system","subtype":"stop_hook_summary","content":"hooks"}`,
		`{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>x</task-id>\n</task-notification>"}}`,
		`{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued"}}`,
		`{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error: 500"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<ide_opened_file>a.go</ide_opened_file>"},{"type":"text","text":"  and mul  "}]}}`,
		`{"type":"assistant","message":{"model":"m","content":[{"type":"text","text":"Done."},{"type":"text","text":"Both."}]}}`,
		`not json`)

	got, err := History(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	want := []Message{
		{Role: Prompt, Text: "add div", At: time.Date(2026, 10, 30, 7, 0, 14, 347e6, time.UTC)},
		{Role: Reply, Text: "Reading calc.py."},
		{Role: Reply, Text: "List the files"}, // what a Bash call says it does
		{Role: Cut},
		{Role: Prompt, Text: "/model haiku"},
		{Role: Output, Text: "Set model to haiku"},
		{Role: Prompt, Text: "/usage"},
		{Role: Output, Text: "API Error: 500"},
		{Role: Prompt, Text: "and mul"},
		{Role: Reply, Text: "Done."},
		{Role: Reply, Text: "Both."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("History =\n%+v\nwant\n%+v", got, want)
	}

	if _, err := History(dir, "missing"); err == nil {
		t.Error("History of a missing conversation succeeded")
	}
}

func TestCalls(t *testing.T) {
	dir := t.TempDir()
	sessionsWrite(t, dir, "s", time.Now(),
		`{"type":"user","message":{"role":"user","content":"plan it"}}`,
		`{"type":"assistant","message":{"model":"m","content":[{"type":"tool_use","id":"c1","name":"TaskCreate","input":{"subject":"Read"}},{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"a.go"},{"type":"tool_result","tool_use_id":"c1","content":"Task #1 created successfully: Read"}]}}`,
		`{"type":"assistant","message":{"model":"m","content":[{"type":"tool_use","id":"u1","name":"TaskUpdate","input":{"taskId":"1","status":"completed"}}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"model":"m","content":[{"type":"tool_use","id":"x1","name":"TaskCreate","input":{"subject":"its"}}]}}`,
		`{"type":"user","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x1","content":"Task #2 created successfully: its"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u1","content":[{"type":"text","text":"Updated task #1 status"}],"is_error":true}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"gone","content":"no call"}]}}`,
		`{"type":"assistant","message":{"model":"m","content":[{"type":"tool_use","id":"w1","name":"TodoWrite","input":{"todos":[]}}]}}`,
		`not json`)
	got, err := Calls(dir, "s", "TaskCreate", "TaskUpdate", "TodoWrite")
	if err != nil {
		t.Fatal(err)
	}
	want := []Call{
		{Name: "TaskCreate", Input: map[string]any{"subject": "Read"}, Output: "Task #1 created successfully: Read"},
		{Name: "TaskUpdate", Input: map[string]any{"taskId": "1", "status": "completed"}, Output: "Updated task #1 status", Error: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Calls =\n%+v\nwant\n%+v", got, want)
	}
	if _, err := Calls(dir, "missing", "TaskCreate"); err == nil {
		t.Error("Calls of a missing conversation succeeded")
	}
}
