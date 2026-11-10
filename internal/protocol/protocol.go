// Package protocol speaks the stream-json protocol of
// `claude -p --input-format stream-json --output-format stream-json --verbose
// --include-partial-messages --permission-prompt-tool stdio`.
//
// Every line on stdout is one JSON object; Parse turns it into zero or more Events.
// Every line written to stdin is produced by Encode.
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Event is something the agent did, as seen on its stdout.
type Event interface{ isEvent() }

// Init is the session handshake (`{"type":"system","subtype":"init"}`); it comes with every turn.
type Init struct {
	SessionID      string
	Model          string
	Cwd            string
	PermissionMode string
	OutputStyle    string
	Version        string      // of the CLI
	Effort         string      // "low" … "max"; "" for a model without efforts
	Commands       []string    // slash commands the session runs, without "/"
	MCP            []MCPServer // the MCP servers and how they are doing
}

// MCPServer is one MCP server of the session; Status is "connected", "pending", "failed", ….
type MCPServer struct{ Name, Status string }

// Reset: the conversation starts over (`/clear`); a new Init follows.
type Reset struct{}

// Commands is the answer to Initialize: the slash commands of the session and what some of them
// choose from.
type Commands struct {
	List         []Command
	Models       []Model  // what /model takes, in the CLI's order
	OutputStyles []string // what /output-style takes
	Version      string   // of the CLI
	Account      string   // who is signed in, e.g. "ann@example.com · Claude Max"; "" with an API key
}

// Command is one slash command; Hint describes its arguments, Aliases are other names for it.
type Command struct {
	Name, Description, Hint string
	Aliases                 []string
}

// Model is one choice of /model: Value is what the command takes, Resolved the model it runs.
type Model struct {
	Value, Name, Description, Resolved string
	Efforts                            []string // the effort levels it supports
}

// ModeChanged: the permission mode is now Mode (`{"type":"system","subtype":"status","permissionMode":…}`),
// e.g. after a SetMode.
type ModeChanged struct{ Mode string }

// ModelChanged: after a SetModel, the session runs Model ("claude-haiku-5-5").
type ModelChanged struct{ Model string }

// ServingModel is the model reported by a response, including a server-side fallback.
type ServingModel struct{ Model string }

// Settings answers GetSettings: Effort is the level the next message runs at, as claude picks it
// from the env, the flags, the settings of the model and the caps; "" for a model without efforts.
type Settings struct{ Effort string }

// ControlError: the CLI refused a control request of ours, e.g. a SetMode to a mode it cannot enter.
type ControlError struct{ RequestID, Message string }

// APIRetry: an API call of the turn failed and goes again after Delay, as try Attempt of MaxRetries.
// Status is the HTTP status that failed it (529: overloaded); 0 for a connection error.
type APIRetry struct {
	Attempt, MaxRetries int
	Delay               time.Duration
	Status              int
}

// ModelFallback: the turn runs on To from here on, because From failed it (Trigger: "overloaded",
// "server_error", "model_not_found", …). Text says so for the user. The next turn tries From again.
type ModelFallback struct{ From, To, Trigger, Text string }

// Compacting: the CLI is summarizing the conversation to free its context (On), or done with it.
type Compacting struct{ On bool }

// Compacted: the CLI replaced the conversation with its summary (Trigger "manual" for /compact,
// "auto" when the context filled up). Before and After are the context's tokens either side of it:
// what main's next call reads, until that call tells it again.
type Compacted struct {
	Trigger       string
	Before, After int
}

// SideAnswer answers a SideQuestion.
type SideAnswer struct{ RequestID, Text string }

// Thinking: the model started a thinking block.
type Thinking struct{}

// TextStart: the model started a new block of visible text. TextDelta chunks follow.
type TextStart struct{}

// TextDelta is a streamed chunk of the current text block.
type TextDelta struct{ Text string }

// Context is how full the main agent's context is as a model call starts: the tokens the call
// reads (`message_start` usage).
type Context struct {
	Model  string
	Tokens int
}

// CLIReply is a reply of the CLI itself rather than the model: a slash command's output or an
// API error. It comes whole, not streamed.
type CLIReply struct{ Text string }

// ToolUse: the model called a tool. Parent is the Task tool call id for subagent calls, empty otherwise.
type ToolUse struct {
	ID     string
	Name   string
	Input  map[string]any
	Parent string
	Model  string // the model that made the call, when the message names it
}

// ToolResult is the outcome of the ToolUse with the same ID.
type ToolResult struct {
	ID      string
	Output  string
	IsError bool
}

// PermissionRequest asks the host whether a tool may run. AskUserQuestion arrives this way too:
// the answers go back inside the Allow input.
type PermissionRequest struct {
	RequestID string
	ToolName  string
	Input     map[string]any
	ToolUseID string
	AgentID   string // the subagent or workflow agent asking; empty for the main agent
}

// TaskStarted: a subagent (Type "local_agent") or a workflow ("local_workflow") began.
// ToolUseID is the Agent or Workflow tool call that started it.
type TaskStarted struct {
	TaskID       string // for a subagent, its agent id (as in PermissionRequest.AgentID)
	ToolUseID    string
	Type         string
	Description  string
	WorkflowName string
}

// TaskProgress reports on a running task. Workflow lists the workflow's phases and agents
// (possibly only some of them); it is nil for subagents.
type TaskProgress struct {
	TaskID      string
	ToolUseID   string
	Description string // e.g. "Reading calc.py", or "Build: tests" for a workflow
	LastTool    string
	Workflow    []WorkflowItem
}

// WorkflowItem is one phase or one agent of a workflow, as in task_progress.workflow_progress.
type WorkflowItem struct {
	Phase           bool   // a phase header rather than an agent
	Index           int    // phase index, or agent index
	Title           string // phase title, or agent label
	PhaseIndex      int    // agents: their phase
	PhaseTitle      string // agents: their phase
	AgentID         string // agents: empty while queued
	State           string // agents, as reported: "start", "progress", "done", …
	Model           string
	LastTool        string
	LastToolSummary string
	ResultPreview   string
	Tokens          int
	ToolCalls       int
	DurationMS      int64
}

// TaskDone: a task finished (task_notification, or task_updated with a final status).
type TaskDone struct {
	TaskID    string
	ToolUseID string
	Status    string // "completed", "failed", "killed", …
	Summary   string
}

// BackgroundTasks is the complete set of live background tasks, replacing the previous set.
type BackgroundTasks struct{ Tasks []BackgroundTask }

// BackgroundTask names one unfinished task as the CLI reports it.
type BackgroundTask struct {
	ID          string `json:"task_id"`
	Description string `json:"description"`
}

// WakeupScheduled reports ScheduleWakeup's confirmed deadline; zero means the loop ended.
type WakeupScheduled struct{ At time.Time }

// CommandStarted marks a new turn, including one started by a scheduled wakeup.
type CommandStarted struct {
	WakeupSource string
	ScheduledFor time.Time
}

// Result ends a turn.
type Result struct {
	IsError        bool
	Text           string
	CostUSD        float64
	DurationMS     int64
	ContextWindows map[string]int // the context window of each model the session used, by model id
	// Usage is what the process has taken so far, of every model, subagents' too; with --resume it
	// starts from what the conversation's last process took. Turn is this turn's, main's calls alone.
	Usage, Turn Usage
}

// Usage counts tokens by what they are.
type Usage struct {
	Input      int // read anew, not cached
	Output     int // written by the model
	CacheRead  int // read again from the cache, on every call
	CacheWrite int // put in the cache
}

// RateLimits is how much of the subscription's rate-limit windows is used, as the headers of the
// last API response had it (`rate_limit_event`). A window the account lacks is nil.
type RateLimits struct {
	Session *Window // five_hour
	Week    *Window // seven_day
	Fable   *Window // seven_day_overage_included: the weekly bucket of Fable
	Overage string  // extra usage: "allowed" or "allowed_warning" when it pays past a limit, "rejected" when not
}

// Window is one rate-limit window.
type Window struct {
	Used     float64 // fraction of the window used; can pass 1
	ResetsAt time.Time
	Status   string // "allowed", "allowed_warning" (on course for the limit), "rejected" (at it); "" when not told
}

// Stderr is one line the process wrote to stderr.
type Stderr struct{ Line string }

// Exited: the agent process is gone. Err is nil on a clean exit.
type Exited struct{ Err error }

func (Init) isEvent()              {}
func (ModeChanged) isEvent()       {}
func (ModelChanged) isEvent()      {}
func (ServingModel) isEvent()      {}
func (Settings) isEvent()          {}
func (ControlError) isEvent()      {}
func (APIRetry) isEvent()          {}
func (ModelFallback) isEvent()     {}
func (Compacting) isEvent()        {}
func (Compacted) isEvent()         {}
func (SideAnswer) isEvent()        {}
func (Reset) isEvent()             {}
func (Commands) isEvent()          {}
func (Thinking) isEvent()          {}
func (TextStart) isEvent()         {}
func (TextDelta) isEvent()         {}
func (Context) isEvent()           {}
func (CLIReply) isEvent()          {}
func (ToolUse) isEvent()           {}
func (ToolResult) isEvent()        {}
func (PermissionRequest) isEvent() {}
func (TaskStarted) isEvent()       {}
func (TaskProgress) isEvent()      {}
func (TaskDone) isEvent()          {}
func (BackgroundTasks) isEvent()   {}
func (WakeupScheduled) isEvent()   {}
func (CommandStarted) isEvent()    {}
func (Result) isEvent()            {}
func (RateLimits) isEvent()        {}
func (Stderr) isEvent()            {}
func (Exited) isEvent()            {}

// Outbound is a message for the agent's stdin.
type Outbound interface{ isOutbound() }

// UserMessage starts (or queues) a turn. Without images the content is a plain string, which
// claude reads as a slash command when it is one.
type UserMessage struct {
	Text   string
	Images []Image
}

// Image is a picture attached to a UserMessage.
type Image struct {
	Media string // "image/png", "image/jpeg", "image/gif" or "image/webp"
	Data  []byte
}

// Allow answers a PermissionRequest; Input is what the tool runs with (for AskUserQuestion it carries "answers").
type Allow struct {
	RequestID string
	Input     map[string]any
}

// Deny answers a PermissionRequest with a refusal the model gets to read.
type Deny struct {
	RequestID string
	Message   string
}

// Initialize asks the CLI about the session before the first turn (an Init comes only with a
// turn); Commands answers it.
type Initialize struct{ RequestID string }

// GetSettings asks the CLI what the session runs with; Settings answers it.
type GetSettings struct{ RequestID string }

// Interrupt stops the turn in progress: the running tools are cancelled and a Result (an error)
// ends the turn.
type Interrupt struct{ RequestID string }

// SetMode switches the permission mode ("default", "acceptEdits", "plan", …); ModeChanged confirms it,
// ControlError refuses it.
type SetMode struct {
	RequestID string
	Mode      string
}

// SetModel switches the model, as /model does ("haiku", "default", "claude-sonnet-5-5");
// ModelChanged confirms it with the model it resolved to, ControlError refuses it.
type SetModel struct {
	RequestID string
	Model     string
}

// SideQuestion asks the model beside the conversation, as /btw does: a turn in progress goes on,
// and neither the question nor its answer joins the conversation. History holds the side
// questions answered before, for follow-ups. SideAnswer answers it, ControlError refuses it.
type SideQuestion struct {
	RequestID string
	Question  string
	History   []SideQA
}

// SideQA is a side question answered.
type SideQA struct{ Question, Response string }

// Resume restarts the agent in a saved conversation (`claude --resume`). It never reaches stdin:
// the session runs a new process, and nothing more is heard from the old one.
type Resume struct{ SessionID string }

func (UserMessage) isOutbound()  {}
func (Allow) isOutbound()        {}
func (Deny) isOutbound()         {}
func (SetMode) isOutbound()      {}
func (SetModel) isOutbound()     {}
func (Initialize) isOutbound()   {}
func (GetSettings) isOutbound()  {}
func (Interrupt) isOutbound()    {}
func (SideQuestion) isOutbound() {}
func (Resume) isOutbound()       {}

// Parse decodes one stdout line. Lines it does not care about yield nothing.
func Parse(line []byte) []Event {
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(line, &head) != nil {
		return nil
	}
	switch head.Type {
	case "system":
		return parseSystem(line)
	case "stream_event":
		return parseStreamEvent(line)
	case "assistant":
		return parseAssistant(line)
	case "user":
		return parseUser(line)
	case "control_request":
		return parseControlRequest(line)
	case "result":
		return parseResult(line)
	case "rate_limit_event":
		return parseRateLimits(line)
	case "conversation_reset":
		return []Event{Reset{}}
	case "control_response":
		return parseControlResponse(line)
	case "command_lifecycle":
		var m struct {
			State        string `json:"state"`
			WakeupSource string `json:"wakeup_source"`
			ScheduledFor int64  `json:"scheduled_for"`
		}
		if json.Unmarshal(line, &m) == nil && m.State == "started" {
			ev := CommandStarted{WakeupSource: m.WakeupSource}
			if m.ScheduledFor > 0 {
				ev.ScheduledFor = time.UnixMilli(m.ScheduledFor)
			}
			return []Event{ev}
		}
	}
	return nil
}

func parseSystem(line []byte) []Event {
	var head struct {
		Subtype string `json:"subtype"`
	}
	if json.Unmarshal(line, &head) != nil {
		return nil
	}
	switch head.Subtype {
	case "init":
		return parseInit(line)
	case "status":
		return parseStatus(line)
	case "task_started":
		return parseTaskStarted(line)
	case "task_progress":
		return parseTaskProgress(line)
	case "task_notification", "task_updated":
		return parseTaskDone(line)
	case "background_tasks_changed":
		var m struct {
			Tasks []BackgroundTask `json:"tasks"`
		}
		if json.Unmarshal(line, &m) == nil && m.Tasks != nil {
			return []Event{BackgroundTasks{Tasks: m.Tasks}}
		}
	case "api_retry":
		return parseAPIRetry(line)
	case "model_fallback":
		return parseModelFallback(line)
	case "compact_boundary":
		return parseCompactBoundary(line)
	}
	return nil
}

func parseCompactBoundary(line []byte) []Event {
	var m struct {
		Parent string `json:"parent_tool_use_id"`
		Meta   struct {
			Trigger string `json:"trigger"`
			Pre     int    `json:"pre_tokens"`
			Post    int    `json:"post_tokens"`
		} `json:"compact_metadata"`
	}
	if json.Unmarshal(line, &m) != nil || m.Parent != "" { // a subagent's context is not main's
		return nil
	}
	return []Event{Compacted{Trigger: m.Meta.Trigger, Before: m.Meta.Pre, After: m.Meta.Post}}
}

func parseAPIRetry(line []byte) []Event {
	var m struct {
		Attempt    int     `json:"attempt"`
		MaxRetries int     `json:"max_retries"`
		DelayMS    float64 `json:"retry_delay_ms"`
		Status     *int    `json:"error_status"` // null for a connection error
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	ev := APIRetry{Attempt: m.Attempt, MaxRetries: m.MaxRetries, Delay: time.Duration(m.DelayMS) * time.Millisecond}
	if m.Status != nil {
		ev.Status = *m.Status
	}
	return []Event{ev}
}

func parseModelFallback(line []byte) []Event {
	var m struct {
		From    string `json:"original_model"`
		To      string `json:"fallback_model"`
		Trigger string `json:"trigger"`
		Text    string `json:"content"`
	}
	if json.Unmarshal(line, &m) != nil || m.To == "" {
		return nil
	}
	return []Event{ModelFallback{From: m.From, To: m.To, Trigger: m.Trigger, Text: m.Text}}
}

func parseInit(line []byte) []Event {
	var m struct {
		SessionID      string      `json:"session_id"`
		Model          string      `json:"model"`
		Cwd            string      `json:"cwd"`
		PermissionMode string      `json:"permissionMode"`
		OutputStyle    string      `json:"output_style"`
		Version        string      `json:"claude_code_version"`
		Effort         string      `json:"effort"`
		Commands       []string    `json:"slash_commands"`
		MCP            []MCPServer `json:"mcp_servers"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	return []Event{Init{SessionID: m.SessionID, Model: m.Model, Cwd: m.Cwd, PermissionMode: m.PermissionMode,
		OutputStyle: m.OutputStyle, Version: m.Version, Effort: m.Effort, Commands: m.Commands, MCP: m.MCP}}
}

// parseStatus reports a permission mode change, and a compaction starting ("compacting") or ending
// (with its compact_result); other status lines ("requesting", …) yield nothing.
func parseStatus(line []byte) []Event {
	var m struct {
		Status         string `json:"status"`
		PermissionMode string `json:"permissionMode"`
		CompactResult  string `json:"compact_result"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	var evs []Event
	if m.PermissionMode != "" {
		evs = append(evs, ModeChanged{Mode: m.PermissionMode})
	}
	switch {
	case m.Status == "compacting":
		evs = append(evs, Compacting{On: true})
	case m.CompactResult != "":
		evs = append(evs, Compacting{On: false})
	}
	return evs
}

func parseTaskStarted(line []byte) []Event {
	var m struct {
		TaskID       string `json:"task_id"`
		ToolUseID    string `json:"tool_use_id"`
		Type         string `json:"task_type"`
		Description  string `json:"description"`
		WorkflowName string `json:"workflow_name"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	return []Event{TaskStarted{TaskID: m.TaskID, ToolUseID: m.ToolUseID, Type: m.Type,
		Description: m.Description, WorkflowName: m.WorkflowName}}
}

func parseTaskProgress(line []byte) []Event {
	var m struct {
		TaskID      string            `json:"task_id"`
		ToolUseID   string            `json:"tool_use_id"`
		Description string            `json:"description"`
		LastTool    string            `json:"last_tool_name"`
		Workflow    []json.RawMessage `json:"workflow_progress"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	return []Event{TaskProgress{TaskID: m.TaskID, ToolUseID: m.ToolUseID, Description: m.Description,
		LastTool: m.LastTool, Workflow: workflowItems(m.Workflow)}}
}

// workflowItems decodes workflow_progress. Entries of another type, or that do not decode, are skipped.
func workflowItems(raw []json.RawMessage) []WorkflowItem {
	var items []WorkflowItem
	for _, r := range raw {
		var w struct {
			Type            string  `json:"type"`
			Index           int     `json:"index"`
			Title           string  `json:"title"`
			Label           string  `json:"label"`
			PhaseIndex      int     `json:"phaseIndex"`
			PhaseTitle      string  `json:"phaseTitle"`
			AgentID         string  `json:"agentId"`
			State           string  `json:"state"`
			Model           string  `json:"model"`
			LastTool        string  `json:"lastToolName"`
			LastToolSummary string  `json:"lastToolSummary"`
			ResultPreview   string  `json:"resultPreview"`
			Tokens          int     `json:"tokens"`
			ToolCalls       int     `json:"toolCalls"`
			DurationMS      float64 `json:"durationMs"`
		}
		if json.Unmarshal(r, &w) != nil {
			continue
		}
		switch w.Type {
		case "workflow_phase":
			items = append(items, WorkflowItem{Phase: true, Index: w.Index, Title: w.Title})
		case "workflow_agent":
			items = append(items, WorkflowItem{
				Index: w.Index, Title: w.Label, PhaseIndex: w.PhaseIndex, PhaseTitle: w.PhaseTitle,
				AgentID: w.AgentID, State: w.State, Model: w.Model,
				LastTool: w.LastTool, LastToolSummary: w.LastToolSummary, ResultPreview: w.ResultPreview,
				Tokens: w.Tokens, ToolCalls: w.ToolCalls, DurationMS: int64(w.DurationMS),
			})
		}
	}
	return items
}

// parseTaskDone handles task_notification, and task_updated once its patch carries a final status.
func parseTaskDone(line []byte) []Event {
	var m struct {
		Subtype   string `json:"subtype"`
		TaskID    string `json:"task_id"`
		ToolUseID string `json:"tool_use_id"`
		Status    string `json:"status"`
		Summary   string `json:"summary"`
		Patch     struct {
			Status string `json:"status"`
		} `json:"patch"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	if m.Subtype == "task_updated" {
		switch m.Patch.Status {
		case "", "running", "pending":
			return nil
		}
		m.Status = m.Patch.Status
	}
	return []Event{TaskDone{TaskID: m.TaskID, ToolUseID: m.ToolUseID, Status: m.Status, Summary: m.Summary}}
}

func parseStreamEvent(line []byte) []Event {
	var m struct {
		Parent string `json:"parent_tool_use_id"`
		Event  struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
				To   struct {
					Model string `json:"model"`
				} `json:"to"`
			} `json:"content_block"`
			Usage struct {
				Iterations []struct {
					Type  string `json:"type"`
					Model string `json:"model"`
				} `json:"iterations"`
			} `json:"usage"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					Input         int `json:"input_tokens"`
					CacheRead     int `json:"cache_read_input_tokens"`
					CacheCreation int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		} `json:"event"`
	}
	if json.Unmarshal(line, &m) != nil || m.Parent != "" {
		return nil
	}
	e := m.Event
	switch {
	case e.Type == "message_start":
		u := e.Message.Usage
		return []Event{Context{Model: e.Message.Model, Tokens: u.Input + u.CacheRead + u.CacheCreation}}
	case e.Type == "content_block_start" && e.ContentBlock.Type == "fallback" && e.ContentBlock.To.Model != "":
		return []Event{ServingModel{Model: e.ContentBlock.To.Model}}
	case e.Type == "message_delta":
		for i := len(e.Usage.Iterations) - 1; i >= 0; i-- {
			it := e.Usage.Iterations[i]
			if it.Type == "fallback_message" && it.Model != "" {
				return []Event{ServingModel{Model: it.Model}}
			}
		}
	case e.Type == "content_block_start" && e.ContentBlock.Type == "thinking":
		return []Event{Thinking{}}
	case e.Type == "content_block_start" && e.ContentBlock.Type == "text":
		return []Event{TextStart{}}
	case e.Type == "content_block_delta" && e.Delta.Type == "text_delta":
		return []Event{TextDelta{Text: e.Delta.Text}}
	}
	return nil
}

func parseAssistant(line []byte) []Event {
	var m struct {
		Parent  string `json:"parent_tool_use_id"`
		Message struct {
			Model   string `json:"model"`
			Content []struct {
				Type  string         `json:"type"`
				ID    string         `json:"id"`
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
				Text  string         `json:"text"`
				To    struct {
					Model string `json:"model"`
				} `json:"to"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	// The model's text arrives as stream events; the CLI's own replies (slash commands, API
	// errors) come only here, from the "<synthetic>" model.
	synthetic := m.Message.Model == "<synthetic>" && m.Parent == ""
	model := m.Message.Model
	var evs []Event
	for _, c := range m.Message.Content {
		switch {
		case c.Type == "tool_use":
			evs = append(evs, ToolUse{ID: c.ID, Name: c.Name, Input: c.Input, Parent: m.Parent, Model: m.Message.Model})
		case c.Type == "text" && synthetic && c.Text != "":
			evs = append(evs, CLIReply{Text: c.Text})
		case c.Type == "fallback" && c.To.Model != "":
			model = c.To.Model
		}
	}
	if m.Parent == "" && !synthetic && model != "" {
		evs = append(evs, ServingModel{Model: model})
	}
	return evs
}

func parseUser(line []byte) []Event {
	var m struct {
		ToolResult json.RawMessage `json:"tool_use_result"`
		Message    struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	var items []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
	}
	// A plain string content is a prompt, not tool results: nothing to report.
	if json.Unmarshal(m.Message.Content, &items) != nil {
		return nil
	}
	var evs []Event
	for _, it := range items {
		if it.Type == "tool_result" {
			evs = append(evs, ToolResult{ID: it.ToolUseID, Output: resultText(it.Content), IsError: it.IsError})
		}
	}
	var wakeup struct {
		ScheduledFor *int64 `json:"scheduledFor"`
	}
	if json.Unmarshal(m.ToolResult, &wakeup) == nil && wakeup.ScheduledFor != nil {
		ev := WakeupScheduled{}
		if *wakeup.ScheduledFor > 0 {
			ev.At = time.UnixMilli(*wakeup.ScheduledFor)
		}
		evs = append(evs, ev)
	}
	return evs
}

// resultText flattens tool_result content: a string, or a list of blocks whose text blocks are joined by newlines.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks) // anything else counts as no output
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// parseControlResponse picks the slash commands out of the answer to Initialize, the model out of
// the one to SetModel, the effort out of the one to GetSettings and the answer out of the one to
// SideQuestion; the answers to other requests say nothing the UI needs.
func parseControlResponse(line []byte) []Event {
	var m struct {
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Error     string `json:"error"`
			Response  struct {
				Commands []struct {
					Name         string   `json:"name"`
					Description  string   `json:"description"`
					ArgumentHint string   `json:"argumentHint"`
					Aliases      []string `json:"aliases"`
				} `json:"commands"`
				Models []struct {
					Value       string   `json:"value"`
					Name        string   `json:"displayName"`
					Description string   `json:"description"`
					Resolved    string   `json:"resolvedModel"`
					Efforts     []string `json:"supportedEffortLevels"`
				} `json:"models"`
				OutputStyles []string `json:"available_output_styles"`
				Version      string   `json:"claude_code_version"`
				Account      struct {
					Email        string `json:"email"`
					Subscription string `json:"subscriptionType"`
				} `json:"account"`
				Mode   string          `json:"current_permission_mode"`
				Model  string          `json:"model"`    // the answer to a SetModel
				Answer json.RawMessage `json:"response"` // the answer to a SideQuestion: a string

				Applied *struct { // the answer to a GetSettings
					Effort string `json:"effort"` // null for a model without efforts
				} `json:"applied"`
			} `json:"response"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	if m.Response.Subtype == "error" {
		return []Event{ControlError{RequestID: m.Response.RequestID, Message: m.Response.Error}}
	}
	if md := m.Response.Response.Model; m.Response.Subtype == "success" && md != "" {
		return []Event{ModelChanged{Model: md}}
	}
	if a := m.Response.Response.Applied; m.Response.Subtype == "success" && a != nil {
		return []Event{Settings{Effort: a.Effort}}
	}
	var answer string
	if json.Unmarshal(m.Response.Response.Answer, &answer) == nil && m.Response.Subtype == "success" {
		return []Event{SideAnswer{RequestID: m.Response.RequestID, Text: answer}}
	}
	if m.Response.Subtype != "success" || m.Response.Response.Commands == nil {
		return nil
	}
	r := m.Response.Response
	ev := Commands{OutputStyles: r.OutputStyles, Version: r.Version}
	for _, c := range r.Commands {
		if c.Name != "" {
			ev.List = append(ev.List, Command{Name: c.Name, Description: c.Description, Hint: c.ArgumentHint, Aliases: c.Aliases})
		}
	}
	for _, md := range r.Models {
		ev.Models = append(ev.Models, Model{Value: md.Value, Name: md.Name, Description: md.Description, Resolved: md.Resolved, Efforts: md.Efforts})
	}
	if a := r.Account; a.Email != "" {
		ev.Account = strings.TrimSuffix(a.Email+" · "+a.Subscription, " · ")
	}
	evs := []Event{ev}
	if mode := m.Response.Response.Mode; mode != "" { // as --permission-mode set it
		evs = append(evs, ModeChanged{Mode: mode})
	}
	return evs
}

func parseControlRequest(line []byte) []Event {
	var m struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype   string         `json:"subtype"`
			ToolName  string         `json:"tool_name"`
			Input     map[string]any `json:"input"`
			ToolUseID string         `json:"tool_use_id"`
			AgentID   string         `json:"agent_id"`
		} `json:"request"`
	}
	if json.Unmarshal(line, &m) != nil || m.Request.Subtype != "can_use_tool" {
		return nil
	}
	r := m.Request
	return []Event{PermissionRequest{RequestID: m.RequestID, ToolName: r.ToolName, Input: r.Input,
		ToolUseID: r.ToolUseID, AgentID: r.AgentID}}
}

func parseResult(line []byte) []Event {
	var m struct {
		Subtype    string   `json:"subtype"`
		IsError    bool     `json:"is_error"`
		Result     string   `json:"result"`
		Errors     []string `json:"errors"`
		CostUSD    float64  `json:"total_cost_usd"`
		DurationMS float64  `json:"duration_ms"`
		Usage      struct {
			Input         int `json:"input_tokens"`
			Output        int `json:"output_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		ModelUsage map[string]struct {
			ContextWindow int `json:"contextWindow"`
			Input         int `json:"inputTokens"`
			Output        int `json:"outputTokens"`
			CacheRead     int `json:"cacheReadInputTokens"`
			CacheCreation int `json:"cacheCreationInputTokens"`
		} `json:"modelUsage"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	var windows map[string]int
	var all Usage
	for model, u := range m.ModelUsage {
		all.Input, all.Output = all.Input+u.Input, all.Output+u.Output
		all.CacheRead, all.CacheWrite = all.CacheRead+u.CacheRead, all.CacheWrite+u.CacheCreation
		if u.ContextWindow > 0 {
			if windows == nil {
				windows = map[string]int{}
			}
			windows[model] = u.ContextWindow
		}
	}
	text := m.Result
	if text == "" {
		text = strings.Join(m.Errors, "\n")
	}
	if text == "" {
		text = m.Subtype
	}
	u := m.Usage
	turn := Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation}
	return []Event{Result{IsError: m.IsError, Text: text, CostUSD: m.CostUSD, DurationMS: int64(m.DurationMS), ContextWindows: windows, Usage: all, Turn: turn}}
}

func parseRateLimits(line []byte) []Event {
	type window struct {
		Utilization float64 `json:"utilization"`
		ResetsAt    int64   `json:"resetsAt"`
		Status      string  `json:"status"`
	}
	var m struct {
		Info struct {
			Overage string `json:"overageStatus"`
			Windows struct {
				FiveHour *window `json:"five_hour"`
				SevenDay *window `json:"seven_day"`
				Fable    *window `json:"seven_day_overage_included"`
			} `json:"unifiedWindows"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal(line, &m) != nil {
		return nil
	}
	conv := func(w *window) *Window {
		if w == nil {
			return nil
		}
		return &Window{Used: w.Utilization, ResetsAt: time.Unix(w.ResetsAt, 0), Status: w.Status}
	}
	w := m.Info.Windows
	ev := RateLimits{Session: conv(w.FiveHour), Week: conv(w.SevenDay), Fable: conv(w.Fable), Overage: m.Info.Overage}
	if ev.Session == nil && ev.Week == nil && ev.Fable == nil {
		return nil
	}
	return []Event{ev}
}

// Encode renders one stdin line, without the trailing newline.
func Encode(o Outbound) ([]byte, error) {
	switch o := o.(type) {
	case UserMessage:
		var content any = o.Text
		if len(o.Images) > 0 { // the pictures first, as the API advises
			var blocks []map[string]any
			for _, im := range o.Images {
				blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": im.Media, "data": im.Data, // []byte marshals as base64
				}})
			}
			if o.Text != "" { // the API refuses an empty text block
				blocks = append(blocks, map[string]any{"type": "text", "text": o.Text})
			}
			content = blocks
		}
		return json.Marshal(map[string]any{
			"type":    "user",
			"message": map[string]any{"role": "user", "content": content},
		})
	case Allow:
		input := o.Input
		if input == nil {
			input = map[string]any{} // the CLI expects an object, not null
		}
		return controlResponse(o.RequestID, map[string]any{"behavior": "allow", "updatedInput": input})
	case Deny:
		return controlResponse(o.RequestID, map[string]any{"behavior": "deny", "message": o.Message})
	case Initialize:
		return json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": o.RequestID,
			"request":    map[string]any{"subtype": "initialize"},
		})
	case GetSettings:
		return json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": o.RequestID,
			"request":    map[string]any{"subtype": "get_settings"},
		})
	case Interrupt:
		return json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": o.RequestID,
			"request":    map[string]any{"subtype": "interrupt"},
		})
	case SetMode:
		return json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": o.RequestID,
			"request":    map[string]any{"subtype": "set_permission_mode", "mode": o.Mode},
		})
	case SetModel:
		return json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": o.RequestID,
			"request":    map[string]any{"subtype": "set_model", "model": o.Model},
		})
	case SideQuestion:
		history := []map[string]string{} // the CLI wants an array, not null
		for _, h := range o.History {
			history = append(history, map[string]string{"question": h.Question, "response": h.Response})
		}
		return json.Marshal(map[string]any{
			"type":       "control_request",
			"request_id": o.RequestID,
			"request":    map[string]any{"subtype": "side_question", "question": o.Question, "history": history},
		})
	}
	return nil, fmt.Errorf("protocol: cannot encode %T", o)
}

func controlResponse(requestID string, response map[string]any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   response,
		},
	})
}
