package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func parseDemo(t *testing.T) []Event {
	t.Helper()
	return parseFile(t, "../testdata/demo.jsonl")
}

func parseFile(t *testing.T, path string) []Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var evs []Event
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		evs = append(evs, Parse(line)...)
	}
	return evs
}

func countEvents(evs []Event) map[string]int {
	counts := map[string]int{}
	for _, ev := range evs {
		counts[fmt.Sprintf("%T", ev)]++
	}
	return counts
}

func TestParseDemoCounts(t *testing.T) {
	counts := countEvents(parseDemo(t))
	// Counted from testdata/demo.jsonl with a script: 282 lines, of which these map to events.
	want := map[string]int{
		"protocol.Init":              1,
		"protocol.Thinking":          4,
		"protocol.TextStart":         3,
		"protocol.TextDelta":         56,
		"protocol.ToolUse":           8,
		"protocol.ToolResult":        8,
		"protocol.PermissionRequest": 4,
		"protocol.Result":            1,
		"protocol.RateLimits":        1,
		"protocol.Context":           7,
		"protocol.ServingModel":      15,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("event counts:\n got %v\nwant %v", counts, want)
	}
}

func TestParseDemoDetails(t *testing.T) {
	evs := parseDemo(t)

	init, _ := evs[0].(Init)
	if !slices.Contains(init.Commands, "compact") {
		t.Errorf("slash commands %v", init.Commands)
	}
	init.Commands = nil
	if want := (Init{
		SessionID:      "d7ec29c5-7965-422f-87e5-45300f720b8f",
		Model:          "claude-haiku-5-5",
		Cwd:            "/Users/pixelbender/Projects/RustyChip/shell/internal/testdata/demo",
		PermissionMode: "default",
		OutputStyle:    "default",
		Version:        "2.1.312",
		MCP:            []MCPServer{{Name: "plugin:telegram:telegram", Status: "pending"}},
	}); !reflect.DeepEqual(init, want) {
		t.Errorf("first event = %#v, want %#v", init, want)
	}

	var tools, perms []string
	results := map[string]ToolResult{}
	var uses []ToolUse
	var block string // text since the last TextStart
	var res Result
	for _, ev := range evs {
		switch ev := ev.(type) {
		case ToolUse:
			tools = append(tools, ev.Name)
			uses = append(uses, ev)
			if ev.Parent != "" {
				t.Errorf("tool %s has parent %q; the demo has no subagents", ev.ID, ev.Parent)
			}
		case ToolResult:
			results[ev.ID] = ev
		case PermissionRequest:
			perms = append(perms, ev.ToolName)
		case TextStart:
			block = ""
		case TextDelta:
			block += ev.Text
		case Result:
			res = ev
		}
	}

	wantTools := []string{"AskUserQuestion", "Grep", "Read", "Bash", "Edit", "Write", "Bash", "Bash"}
	if !reflect.DeepEqual(tools, wantTools) {
		t.Errorf("tools = %v, want %v", tools, wantTools)
	}
	if want := []string{"AskUserQuestion", "Edit", "Write", "Bash"}; !reflect.DeepEqual(perms, want) {
		t.Errorf("permission requests = %v, want %v", perms, want)
	}
	for _, u := range uses {
		r, ok := results[u.ID]
		if !ok {
			t.Errorf("no result for %s %s", u.Name, u.ID)
			continue
		}
		if r.IsError != (u.Name == "Grep") {
			t.Errorf("%s result IsError = %v; only Grep failed in the demo", u.Name, r.IsError)
		}
	}
	if out := results["toolu_016bo1xo3ePtaTu5d58ApoWc"].Output; !strings.HasPrefix(out, "1\t\"\"\"Tiny calculator module.\"\"\"\n2\t\n") {
		t.Errorf("Read output = %q", out)
	}
	if got := results["toolu_01GA3uhs3LxkVH98HjAniH7P"].Output; got != "exit=0" {
		t.Errorf("Bash output = %q, want %q", got, "exit=0")
	}

	// The turn's result text is the final text block, streamed as deltas.
	if block == "" || block != res.Text {
		t.Errorf("final text block != result text:\nblock  %q\nresult %q", block, res.Text)
	}
	if res.IsError || res.CostUSD != 0.007306979999999999 || res.DurationMS != 18318 ||
		!reflect.DeepEqual(res.ContextWindows, map[string]int{"claude-haiku-5-5": 1000000}) {
		t.Errorf("result = %+v", res)
	}
}

func TestParseDemoAskUserQuestion(t *testing.T) {
	for _, ev := range parseDemo(t) {
		p, ok := ev.(PermissionRequest)
		if !ok || p.ToolName != "AskUserQuestion" {
			continue
		}
		if p.RequestID != "07009bde-ba10-427c-b4d3-813de99943d1" {
			t.Errorf("RequestID = %q", p.RequestID)
		}
		want := map[string]any{"questions": []any{map[string]any{
			"question": "Which function should I add to calc.py: mul or div?",
			"header":   "Function",
			"options": []any{
				map[string]any{"label": "mul", "description": "Multiplication: returns a * b"},
				map[string]any{"label": "div", "description": "Division: returns a / b"},
			},
			"multiSelect": false,
		}}}
		if !reflect.DeepEqual(p.Input, want) {
			t.Errorf("input =\n%#v\nwant\n%#v", p.Input, want)
		}
		return
	}
	t.Fatal("no AskUserQuestion permission request in the demo")
}

// testdata/multi.jsonl: two turns; the first runs an Agent subagent and launches a 2-agent workflow,
// the second starts when the workflow's notification arrives.
const (
	subagentTool = "toolu_01P5HQRgrYB9L6rKi69RVaUL" // the Agent call
	subagentID   = "a0b8c00280f91d74d"
	workflowTool = "toolu_01SYyRMgjWVknSVXCK8VrFCu" // the Workflow call
	workflowID   = "wrd0ne1z4"
	testsAgent   = "ac4272cdc31efcff1"
	docsAgent    = "ad85e07399d07f94d"
)

func TestParseMultiCounts(t *testing.T) {
	// Counted from testdata/multi.jsonl with a script (390 lines). TaskDone: a task_updated with status
	// "completed" and a task_notification for each of the two tasks.
	want := map[string]int{
		"protocol.Init":              2,
		"protocol.Thinking":          5,
		"protocol.TextStart":         2,
		"protocol.TextDelta":         42,
		"protocol.ToolUse":           7,
		"protocol.ToolResult":        7,
		"protocol.PermissionRequest": 6,
		"protocol.TaskStarted":       2,
		"protocol.TaskProgress":      13,
		"protocol.TaskDone":          4,
		"protocol.BackgroundTasks":   2,
		"protocol.ServingModel":      12,
		"protocol.RateLimits":        1,
		"protocol.Context":           7,
		"protocol.Result":            2,
	}
	if got := countEvents(parseFile(t, "../testdata/multi.jsonl")); !reflect.DeepEqual(got, want) {
		t.Errorf("event counts:\n got %v\nwant %v", got, want)
	}
}

func TestParseMultiTasks(t *testing.T) {
	var (
		started    []TaskStarted
		done       []TaskDone
		perms      [][3]string // tool, tool use id, agent id
		progress   []TaskProgress
		subagentOf = map[string]string{} // tool use id → parent
	)
	for _, ev := range parseFile(t, "../testdata/multi.jsonl") {
		switch ev := ev.(type) {
		case TaskStarted:
			started = append(started, ev)
		case TaskDone:
			done = append(done, ev)
		case PermissionRequest:
			perms = append(perms, [3]string{ev.ToolName, ev.ToolUseID, ev.AgentID})
		case TaskProgress:
			progress = append(progress, ev)
		case ToolUse:
			if ev.Parent != "" {
				subagentOf[ev.ID] = ev.Parent
			}
		}
	}

	wantStarted := []TaskStarted{
		{TaskID: subagentID, ToolUseID: subagentTool, Type: "local_agent", Description: "Add mul function to calc.py"},
		{TaskID: workflowID, ToolUseID: workflowTool, Type: "local_workflow",
			Description:  "Write calc tests and append a Functions section to README in parallel",
			WorkflowName: "calc-tests-and-docs"},
	}
	if !reflect.DeepEqual(started, wantStarted) {
		t.Errorf("TaskStarted =\n%+v\nwant\n%+v", started, wantStarted)
	}

	if len(done) == 4 {
		if !strings.HasPrefix(done[1].Summary, "`sub` exists, so I added `mul`") {
			t.Errorf("subagent summary = %q", done[1].Summary)
		}
		done[1].Summary = ""
	}
	wantDone := []TaskDone{
		{TaskID: subagentID, Status: "completed"}, // task_updated
		{TaskID: subagentID, ToolUseID: subagentTool, Status: "completed"},
		{TaskID: workflowID, Status: "completed"},
		{TaskID: workflowID, ToolUseID: workflowTool, Status: "completed",
			Summary: `Dynamic workflow "Write calc tests and append a Functions section to README in parallel" completed`},
	}
	if !reflect.DeepEqual(done, wantDone) {
		t.Errorf("TaskDone =\n%+v\nwant\n%+v", done, wantDone)
	}

	wantPerms := [][3]string{
		{"Edit", "toolu_018u7qLFKBqEzo6CxVnueb4K", subagentID},
		{"Workflow", workflowTool, ""},
		{"Write", "toolu_01QhSLyCxRYU566eiSVpGkRs", testsAgent},
		{"Bash", "toolu_015srjqh2CMUmrNqtaw4J4q5", testsAgent},
		{"Edit", "toolu_01SFnFyC44Qk3rmpvcCVQK5X", docsAgent},
		{"Bash", "toolu_013vxiziUckqwi7gWCnW2V3M", ""},
	}
	if !reflect.DeepEqual(perms, wantPerms) {
		t.Errorf("permission requests =\n%v\nwant\n%v", perms, wantPerms)
	}

	// The subagent's own calls carry the Agent call id as parent.
	wantSub := map[string]string{"toolu_01Fms7VBGJPmGCeKxeYXGxfB": subagentTool, "toolu_018u7qLFKBqEzo6CxVnueb4K": subagentTool}
	if !reflect.DeepEqual(subagentOf, wantSub) {
		t.Errorf("subagent tool calls = %v, want %v", subagentOf, wantSub)
	}

	if len(progress) != 13 {
		t.Fatalf("%d TaskProgress, want 13", len(progress))
	}
	if want := (TaskProgress{TaskID: subagentID, ToolUseID: subagentTool, Description: "Reading calc.py", LastTool: "Read"}); !reflect.DeepEqual(progress[0], want) {
		t.Errorf("first progress = %+v, want %+v", progress[0], want)
	}
	var lists [][]WorkflowItem
	for _, p := range progress {
		if p.Workflow != nil {
			lists = append(lists, p.Workflow)
		}
	}
	if len(lists) != 4 {
		t.Fatalf("%d progress events with workflow items, want 4", len(lists))
	}
	phase := WorkflowItem{Phase: true, Index: 1, Title: "Build"}
	model := "claude-haiku-5-5"
	// First list: docs is still queued (no agent id yet).
	wantFirst := []WorkflowItem{
		phase,
		{Index: 1, Title: "tests", PhaseIndex: 1, PhaseTitle: "Build", AgentID: testsAgent, State: "start", Model: model},
		{Index: 2, Title: "docs", PhaseIndex: 1, PhaseTitle: "Build", State: "start", Model: model},
	}
	if !reflect.DeepEqual(lists[0], wantFirst) {
		t.Errorf("first workflow items =\n%+v\nwant\n%+v", lists[0], wantFirst)
	}
	// Last list: both done.
	last := lists[3]
	if len(last) == 3 {
		for i, prefix := range []string{"", "Exit code: 0. Output: `all tests passed`", `I appended the "Functions" section`} {
			if !strings.HasPrefix(last[i].ResultPreview, prefix) {
				t.Errorf("item %d result preview = %q", i, last[i].ResultPreview)
			}
			last[i].ResultPreview = ""
		}
	}
	wantLast := []WorkflowItem{
		phase,
		{Index: 1, Title: "tests", PhaseIndex: 1, PhaseTitle: "Build", AgentID: testsAgent, State: "done", Model: model,
			LastTool: "Bash", LastToolSummary: `python3 test_calc.py; echo "EXIT CODE: $?"`, Tokens: 22228, ToolCalls: 3, DurationMS: 7729},
		{Index: 2, Title: "docs", PhaseIndex: 1, PhaseTitle: "Build", AgentID: docsAgent, State: "done", Model: model,
			LastTool: "Edit", LastToolSummary: "/Users/pixelbender/Projects/RustyChip/shell/internal/testdata/multi/…", Tokens: 22003, ToolCalls: 2, DurationMS: 7967},
	}
	if !reflect.DeepEqual(last, wantLast) {
		t.Errorf("last workflow items =\n%+v\nwant\n%+v", last, wantLast)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []Event
	}{
		{"invalid json", `{"type":`, nil},
		{"empty", ``, nil},
		{"other system subtype", `{"type":"system","subtype":"status","status":"requesting"}`, nil},
		{"permission mode status",
			`{"type":"system","subtype":"status","status":null,"permissionMode":"plan","session_id":"s"}`,
			[]Event{ModeChanged{Mode: "plan"}}},
		{"init",
			`{"type":"system","subtype":"init","session_id":"s","model":"m","cwd":"/w","permissionMode":"acceptEdits"}`,
			[]Event{Init{SessionID: "s", Model: "m", Cwd: "/w", PermissionMode: "acceptEdits"}}},
		{"init with an effort",
			`{"type":"system","subtype":"init","session_id":"s","model":"m","effort":"xhigh"}`,
			[]Event{Init{SessionID: "s", Model: "m", Effort: "xhigh"}}},
		{"unknown type", `{"type":"keep_alive"}`, nil},
		{"main's model call starts",
			`{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"message_start","message":{"model":"claude-haiku-5-5","usage":{"input_tokens":2,"cache_read_input_tokens":12906,"cache_creation_input_tokens":16514,"output_tokens":2}}}}`,
			[]Event{Context{Model: "claude-haiku-5-5", Tokens: 29422}}},
		{"a subagent's model call starts",
			`{"type":"stream_event","parent_tool_use_id":"toolu_1","event":{"type":"message_start","message":{"model":"m","usage":{"input_tokens":2}}}}`,
			nil},
		{"result with context windows",
			`{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":2,"output_tokens":4,"cache_read_input_tokens":11358,"cache_creation_input_tokens":16637},"modelUsage":{"claude-haiku-5-5":{"contextWindow":1000000,"inputTokens":14,"outputTokens":1820,"cacheReadInputTokens":209058,"cacheCreationInputTokens":21525},"x":{"inputTokens":1}}}`,
			[]Event{Result{Text: "ok", ContextWindows: map[string]int{"claude-haiku-5-5": 1000000},
				Usage: Usage{Input: 15, Output: 1820, CacheRead: 209058, CacheWrite: 21525},
				Turn:  Usage{Input: 2, Output: 4, CacheRead: 11358, CacheWrite: 16637}}}},
		{"rate limits without windows", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","overageStatus":"rejected"}}`, nil},
		{"rate limits",
			`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.09,"resetsAt":1793311200,"status":"allowed"},"seven_day_overage_included":{"utilization":1.2,"resetsAt":1793660400}}}}`,
			[]Event{RateLimits{Session: &Window{Used: 0.09, ResetsAt: time.Unix(1793311200, 0), Status: "allowed"}, Fable: &Window{Used: 1.2, ResetsAt: time.Unix(1793660400, 0)}}}},
		{"a rate limit hit, extra usage covering it",
			`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","overageStatus":"allowed","isUsingOverage":true,"unifiedWindows":{"five_hour":{"utilization":1,"resetsAt":1793311200,"status":"rejected"},"seven_day":{"utilization":0.8,"resetsAt":1793660400,"status":"allowed_warning"}}}}`,
			[]Event{RateLimits{Session: &Window{Used: 1, ResetsAt: time.Unix(1793311200, 0), Status: "rejected"},
				Week: &Window{Used: 0.8, ResetsAt: time.Unix(1793660400, 0), Status: "allowed_warning"}, Overage: "allowed"}}},
		{"an API call retried",
			`{"type":"system","subtype":"api_retry","attempt":2,"max_retries":10,"retry_delay_ms":5000,"error_status":529,"error":"overloaded","uuid":"u","session_id":"s"}`,
			[]Event{APIRetry{Attempt: 2, MaxRetries: 10, Delay: 5 * time.Second, Status: 529}}},
		{"an API call retried after a delay with a fraction",
			`{"type":"system","subtype":"api_retry","attempt":3,"max_retries":10,"retry_delay_ms":2563.28,"error_status":500,"uuid":"u","session_id":"s"}`,
			[]Event{APIRetry{Attempt: 3, MaxRetries: 10, Delay: 2563 * time.Millisecond, Status: 500}}},
		{"an API call retried after a connection error",
			`{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"retry_delay_ms":600,"error_status":null,"error":"unknown","uuid":"u","session_id":"s"}`,
			[]Event{APIRetry{Attempt: 1, MaxRetries: 10, Delay: 600 * time.Millisecond}}},
		{"the turn falls back to another model",
			`{"type":"system","subtype":"model_fallback","trigger":"overloaded","original_model":"claude-opus-5-5","fallback_model":"claude-sonnet-5-5","content":"Opus is overloaded: switched to Sonnet","uuid":"u","session_id":"s"}`,
			[]Event{ModelFallback{From: "claude-opus-5-5", To: "claude-sonnet-5-5", Trigger: "overloaded", Text: "Opus is overloaded: switched to Sonnet"}}},
		{"compaction starts",
			`{"type":"system","subtype":"status","status":"compacting","compact_output_chars":0,"session_id":"s"}`,
			[]Event{Compacting{On: true}}},
		{"compaction ends",
			`{"type":"system","subtype":"status","status":null,"compact_result":"success","session_id":"s"}`,
			[]Event{Compacting{On: false}}},
		{"compaction replaced the conversation",
			`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"manual","pre_tokens":46502,"post_tokens":6555,"cumulative_dropped_tokens":39947,"duration_ms":8443},"session_id":"s"}`,
			[]Event{Compacted{Trigger: "manual", Before: 46502, After: 6555}}},
		{"a subagent's compaction",
			`{"type":"system","subtype":"compact_boundary","parent_tool_use_id":"toolu_P","compact_metadata":{"trigger":"auto","pre_tokens":180000,"post_tokens":9000},"session_id":"s"}`,
			nil},
		{"a tool call names its model",
			`{"type":"assistant","parent_tool_use_id":"toolu_P","message":{"model":"claude-haiku-5-5","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
			[]Event{ToolUse{ID: "t1", Name: "Bash", Input: map[string]any{"command": "ls"}, Parent: "toolu_P", Model: "claude-haiku-5-5"}}},
		{"init with slash commands",
			`{"type":"system","subtype":"init","session_id":"s","slash_commands":["compact","doctor"],"terminal_slash_commands":["doctor"],` +
				`"output_style":"default","claude_code_version":"2.1.314","mcp_servers":[{"name":"docs","status":"pending","source":"claudeai"}]}`,
			[]Event{Init{SessionID: "s", Commands: []string{"compact", "doctor"}, OutputStyle: "default", Version: "2.1.314",
				MCP: []MCPServer{{Name: "docs", Status: "pending"}}}}},
		{"slash command output",
			`{"type":"assistant","parent_tool_use_id":null,"message":{"model":"<synthetic>","content":[{"type":"text","text":"Current model: Haiku"}]}}`,
			[]Event{CLIReply{Text: "Current model: Haiku"}}},
		{"model text is streamed, not repeated",
			`{"type":"assistant","parent_tool_use_id":null,"message":{"model":"claude-haiku-5-5","content":[{"type":"text","text":"hi"}]}}`,
			[]Event{ServingModel{Model: "claude-haiku-5-5"}}},
		{"conversation reset", `{"type":"conversation_reset","new_conversation_id":"c","trigger":"clear"}`, []Event{Reset{}}},
		{"initialize answer",
			`{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{"commands":[{"name":"compact","description":"Free up context","argumentHint":"<instructions>"},{"name":""},` +
				`{"name":"config","aliases":["settings"]}],"models":[{"value":"haiku","resolvedModel":"claude-haiku-5-5","displayName":"Haiku 5.5","description":"Fast","supportedEffortLevels":["low","high"]}],` +
				`"available_output_styles":["default","Concise"],"claude_code_version":"2.1.314","account":{"email":"a@b.c","subscriptionType":"Claude Max"}}}}`,
			[]Event{Commands{
				List:         []Command{{Name: "compact", Description: "Free up context", Hint: "<instructions>"}, {Name: "config", Aliases: []string{"settings"}}},
				Models:       []Model{{Value: "haiku", Name: "Haiku 5.5", Description: "Fast", Resolved: "claude-haiku-5-5", Efforts: []string{"low", "high"}}},
				OutputStyles: []string{"default", "Concise"}, Version: "2.1.314", Account: "a@b.c · Claude Max",
			}}},
		{"initialize answer with the permission mode",
			`{"type":"control_response","response":{"subtype":"success","request_id":"init","response":{"commands":[],"current_permission_mode":"plan"}}}`,
			[]Event{Commands{}, ModeChanged{Mode: "plan"}}},
		{"answer without commands", `{"type":"control_response","response":{"subtype":"success","request_id":"mode-1","response":{}}}`, nil},
		{"model switched",
			`{"type":"control_response","response":{"subtype":"success","request_id":"model-1","response":{"model":"claude-haiku-5-5"}}}`,
			[]Event{ModelChanged{Model: "claude-haiku-5-5"}}},
		{"settings",
			`{"type":"control_response","response":{"subtype":"success","request_id":"settings","response":{"effective":{"model":"opus"},"sources":[],` +
				`"applied":{"model":"claude-opus-5-5","effort":"xhigh","advisor":null}}}}`,
			[]Event{Settings{Effort: "xhigh"}}},
		{"settings of a model without efforts",
			`{"type":"control_response","response":{"subtype":"success","request_id":"settings","response":{"applied":{"model":"claude-haiku-4-5","effort":null}}}}`,
			[]Event{Settings{}}},
		{"failed request",
			`{"type":"control_response","response":{"subtype":"error","request_id":"mode-1","error":"Cannot set permission mode","error_code":"bypass_not_launched"}}`,
			[]Event{ControlError{RequestID: "mode-1", Message: "Cannot set permission mode"}}},
		{"side question answered",
			`{"type":"control_response","response":{"subtype":"success","request_id":"btw-1","response":{"response":"Сейчас идёт ping.","synthetic":false}}}`,
			[]Event{SideAnswer{RequestID: "btw-1", Text: "Сейчас идёт ping."}}},
		{"side question started",
			`{"type":"system","subtype":"control_request_progress","request_id":"btw-1","status":"started"}`, nil},
		{"thinking start",
			`{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`,
			[]Event{Thinking{}}},
		{"text delta",
			`{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hé"}}}`,
			[]Event{TextDelta{Text: "hé"}}},
		{"subagent text delta",
			`{"type":"stream_event","parent_tool_use_id":"toolu_P","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}}`,
			nil},
		{"tool input delta",
			`{"type":"stream_event","parent_tool_use_id":null,"event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\""}}}`,
			nil},
		{"subagent tool uses",
			`{"type":"assistant","parent_tool_use_id":"toolu_P","message":{"content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/a"}},{"type":"tool_use","id":"t2","name":"Glob","input":{"pattern":"*.go"}}]}}`,
			[]Event{
				ToolUse{ID: "t1", Name: "Read", Input: map[string]any{"file_path": "/a"}, Parent: "toolu_P"},
				ToolUse{ID: "t2", Name: "Glob", Input: map[string]any{"pattern": "*.go"}, Parent: "toolu_P"},
			}},
		{"user prompt", `{"type":"user","message":{"role":"user","content":"hello"}}`, nil},
		{"tool result blocks",
			`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"a"},{"type":"image","source":{}},{"type":"text","text":"b"}]},{"type":"tool_result","tool_use_id":"t2"}]}}`,
			[]Event{ToolResult{ID: "t1", Output: "a\nb", IsError: true}, ToolResult{ID: "t2"}}},
		{"other control request", `{"type":"control_request","request_id":"r","request":{"subtype":"interrupt"}}`, nil},
		{"subagent permission request",
			`{"type":"control_request","request_id":"r","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"},"tool_use_id":"t1","agent_id":"a1"}}`,
			[]Event{PermissionRequest{RequestID: "r", ToolName: "Bash", Input: map[string]any{"command": "ls"}, ToolUseID: "t1", AgentID: "a1"}}},
		{"task started",
			`{"type":"system","subtype":"task_started","task_id":"w1","tool_use_id":"t1","description":"d","task_type":"local_workflow","workflow_name":"wf","prompt":"p"}`,
			[]Event{TaskStarted{TaskID: "w1", ToolUseID: "t1", Type: "local_workflow", Description: "d", WorkflowName: "wf"}}},
		{"subagent progress",
			`{"type":"system","subtype":"task_progress","task_id":"a1","tool_use_id":"t1","description":"Reading x","usage":{"tool_uses":1},"last_tool_name":"Read"}`,
			[]Event{TaskProgress{TaskID: "a1", ToolUseID: "t1", Description: "Reading x", LastTool: "Read"}}},
		{"workflow progress",
			`{"type":"system","subtype":"task_progress","task_id":"w1","workflow_progress":[` +
				`{"type":"workflow_phase","index":2,"title":"Check"},` +
				`{"type":"workflow_agent","index":3,"label":"lint","phaseIndex":2,"phaseTitle":"Check","agentId":"a3","state":"error","durationMs":12.7,"toolCalls":4,"tokens":99},` +
				`{"type":"workflow_agent","index":"bad"},` +
				`{"type":"workflow_note","index":1}]}`,
			[]Event{TaskProgress{TaskID: "w1", Workflow: []WorkflowItem{
				{Phase: true, Index: 2, Title: "Check"},
				{Index: 3, Title: "lint", PhaseIndex: 2, PhaseTitle: "Check", AgentID: "a3", State: "error", DurationMS: 12, ToolCalls: 4, Tokens: 99},
			}}}},
		{"empty workflow progress",
			`{"type":"system","subtype":"task_progress","task_id":"w1","workflow_progress":[]}`,
			[]Event{TaskProgress{TaskID: "w1"}}},
		{"task notification",
			`{"type":"system","subtype":"task_notification","task_id":"a1","tool_use_id":"t1","status":"failed","summary":"oops","output_file":"/x"}`,
			[]Event{TaskDone{TaskID: "a1", ToolUseID: "t1", Status: "failed", Summary: "oops"}}},
		{"task updated: killed",
			`{"type":"system","subtype":"task_updated","task_id":"a1","patch":{"status":"killed","end_time":1}}`,
			[]Event{TaskDone{TaskID: "a1", Status: "killed"}}},
		{"task updated: running", `{"type":"system","subtype":"task_updated","task_id":"a1","patch":{"status":"running"}}`, nil},
		{"task updated: pending", `{"type":"system","subtype":"task_updated","task_id":"a1","patch":{"status":"pending"}}`, nil},
		{"task updated: no status", `{"type":"system","subtype":"task_updated","task_id":"a1","patch":{"description":"x"}}`, nil},
		{"error result with errors",
			`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["boom","bang"],"total_cost_usd":0.25,"duration_ms":12}`,
			[]Event{Result{IsError: true, Text: "boom\nbang", CostUSD: 0.25, DurationMS: 12}}},
		{"error result without text",
			`{"type":"result","subtype":"error_max_turns","is_error":true}`,
			[]Event{Result{IsError: true, Text: "error_max_turns"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse([]byte(tt.line)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse =\n%#v\nwant\n%#v", got, tt.want)
			}
		})
	}
}

func TestEncode(t *testing.T) {
	askInput := map[string]any{
		"questions": []any{map[string]any{"question": "Which?", "multiSelect": false}},
		"answers":   map[string]any{"Which?": "div"},
	}
	tests := []struct {
		name string
		in   Outbound
		want string // the documented shape
	}{
		{"user message", UserMessage{Text: "hello\n<world>"},
			`{"type":"user","message":{"role":"user","content":"hello\n<world>"}}`},
		{"user message with an image", UserMessage{Text: "what is [Image #1]?", Images: []Image{{Media: "image/png", Data: []byte("png")}}},
			`{"type":"user","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cG5n"}},{"type":"text","text":"what is [Image #1]?"}]}}`},
		{"allow", Allow{RequestID: "r1", Input: askInput},
			`{"type":"control_response","response":{"subtype":"success","request_id":"r1","response":{"behavior":"allow","updatedInput":{"questions":[{"question":"Which?","multiSelect":false}],"answers":{"Which?":"div"}}}}}`},
		{"allow without input", Allow{RequestID: "r2"},
			`{"type":"control_response","response":{"subtype":"success","request_id":"r2","response":{"behavior":"allow","updatedInput":{}}}}`},
		{"deny", Deny{RequestID: "r3", Message: "The user denied this tool call."},
			`{"type":"control_response","response":{"subtype":"success","request_id":"r3","response":{"behavior":"deny","message":"The user denied this tool call."}}}`},
		{"set mode", SetMode{RequestID: "m1", Mode: "plan"},
			`{"type":"control_request","request_id":"m1","request":{"subtype":"set_permission_mode","mode":"plan"}}`},
		{"set model", SetModel{RequestID: "model-1", Model: "haiku"},
			`{"type":"control_request","request_id":"model-1","request":{"model":"haiku","subtype":"set_model"}}`},
		{"initialize", Initialize{RequestID: "init"},
			`{"type":"control_request","request_id":"init","request":{"subtype":"initialize"}}`},
		{"get settings", GetSettings{RequestID: "settings"},
			`{"type":"control_request","request_id":"settings","request":{"subtype":"get_settings"}}`},
		{"interrupt", Interrupt{RequestID: "int-1"},
			`{"type":"control_request","request_id":"int-1","request":{"subtype":"interrupt"}}`},
		{"side question", SideQuestion{RequestID: "btw-1", Question: "why?"},
			`{"type":"control_request","request_id":"btw-1","request":{"history":[],"question":"why?","subtype":"side_question"}}`},
		{"side question with history", SideQuestion{RequestID: "btw-2", Question: "and?", History: []SideQA{{Question: "why?", Response: "because"}}},
			`{"type":"control_request","request_id":"btw-2","request":{"history":[{"question":"why?","response":"because"}],"question":"and?","subtype":"side_question"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := Encode(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.ContainsRune(b, '\n') {
				t.Errorf("Encode output is not a single line without newline: %q", b)
			}
			var got, want any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("Encode output is not JSON: %v: %s", err, b)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Encode =\n%s\nwant\n%s", b, tt.want)
			}
		})
	}

	if _, err := Encode(nil); err == nil {
		t.Error("Encode(nil) succeeded")
	}
}
