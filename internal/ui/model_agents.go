package ui

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/protocol"
	"github.com/pixelbender/claudit/internal/transcript"
)

// pollEvery is how often the transcripts of running workflows are read.
const pollEvery = 250 * time.Millisecond

// crew is what the model tracks about the agents beyond ide.Agent.
type crew struct {
	keys     map[string]string           // task id or agent id → agent key
	slots    map[string]string           // "<workflow key>#<phase>.<index>" → key of that workflow agent
	calls    map[string]protocol.ToolUse // tool calls waiting for their result, workflow agents' too
	activity map[string]string           // agent key → the Activity copied from workflow progress
	tailers  []tailer                    // transcripts of the running workflows
	polled   time.Time                   // last poll of the tailers
	named    int                         // subagents named so far
}

// tailer follows the transcript dir of one workflow.
type tailer struct {
	workflow string // key of the workflow node
	t        *transcript.Tailer
}

func newCrew() crew {
	return crew{
		keys:     map[string]string{},
		slots:    map[string]string{},
		calls:    map[string]protocol.ToolUse{},
		activity: map[string]string{},
	}
}

// agentByID finds the agent behind a task id or an agent id; nil when unknown.
func (m *Model) agentByID(id string) *ide.Agent {
	if id == "" {
		return nil
	}
	if k, ok := m.crew.keys[id]; ok {
		id = k
	}
	return m.Sim.FindAgent(id)
}

// subagentNames name the subagents in the order they start; past the last, the names come round
// again with a number.
var subagentNames = []string{"kevin", "stuart", "bob", "dave", "jerry", "carl", "phil", "tim", "mark", "jorge", "otto", "norbert"}

// addAgent registers a new agent with the sim. A subagent gets a name of its own.
func (m *Model) addAgent(key string, kind ide.AgentKind, parent, name string) *ide.Agent {
	a := m.Sim.Agent(key, kind)
	a.Parent, a.Name = parent, name
	if kind == ide.Subagent {
		n := m.crew.named
		m.crew.named++
		a.Name = subagentNames[n%len(subagentNames)]
		if n >= len(subagentNames) {
			a.Name += " " + strconv.Itoa(n/len(subagentNames)+1)
		}
	}
	return a
}

// toolUse handles a tool call from the stream: the main agent's or a subagent's.
func (m *Model) toolUse(ev protocol.ToolUse, now time.Time) {
	key := cmp.Or(ev.Parent, "main")
	a := m.Sim.FindAgent(key)
	if a == nil { // a subagent whose start we missed
		a = m.addAgent(key, ide.Subagent, "main", "agent")
		a.Started = now
	}
	switch ev.Name {
	case "Agent", "Task":
		if m.Sim.FindAgent(ev.ID) == nil {
			name := cmp.Or(inputString(ev.Input, "description"), inputString(ev.Input, "subagent_type"), "agent")
			m.addAgent(ev.ID, ide.Subagent, key, name).Started = now
		}
	case "Workflow":
		if m.Sim.FindAgent(ev.ID) == nil {
			m.addAgent(ev.ID, ide.Workflow, key, cmp.Or(workflowName(inputString(ev.Input, "script")), "workflow"))
		}
	}
	m.crew.calls[ev.ID] = ev
	m.Sim.ToolUse(key, ev.ID, ev.Name, ev.Input)
	a.ToolCalls++
	a.Model = cmp.Or(ev.Model, a.Model)
}

// metaName finds `name: '…'` in the `meta` object of a workflow script, before any nested object.
var metaName = regexp.MustCompile("\\bmeta\\s*=\\s*\\{[^{}]*?\\bname\\s*:\\s*['\"`]([^'\"`\\n]+)")

func workflowName(script string) string {
	if s := metaName.FindStringSubmatch(script); s != nil {
		return s[1]
	}
	return ""
}

// toolResult handles a tool result from the stream.
func (m *Model) toolResult(ev protocol.ToolResult, now time.Time) {
	call := m.crew.calls[ev.ID]
	delete(m.crew.calls, ev.ID)
	m.Sim.ToolResult(ev.ID, ev.Output, ev.IsError)
	switch call.Name {
	case "Agent", "Task", "Workflow":
		// A background agent returns at once and TaskDone ends it; an error means it never ran.
		if a := m.Sim.FindAgent(ev.ID); a != nil && ev.IsError {
			m.wrapUp(a, ide.Failed, now)
		}
		if dir, ok := transcript.Dir(ev.Output); ok && call.Name == "Workflow" && !ev.IsError {
			m.crew.tailers = append(m.crew.tailers, tailer{workflow: ev.ID, t: transcript.New(dir)})
			if a := m.Sim.FindAgent(ev.ID); a != nil && (a.Status == ide.Done || a.Status == ide.Failed) {
				m.wrapUp(a, a.Status, now) // it ended before its launch was reported: read it once
			}
		}
	}
}

func (m *Model) taskStarted(ev protocol.TaskStarted, now time.Time) {
	kind, name := ide.Subagent, "agent"
	switch ev.Type {
	case "local_agent":
	case "local_workflow":
		kind, name = ide.Workflow, "workflow"
	default: // background shells and the like are not agents
		return
	}
	key := cmp.Or(ev.ToolUseID, ev.TaskID)
	m.crew.keys[ev.TaskID] = key
	a := m.Sim.FindAgent(key)
	if a == nil {
		a = m.addAgent(key, kind, "main", cmp.Or(ev.WorkflowName, ev.Description))
	}
	if kind == ide.Workflow && ev.WorkflowName != "" {
		a.Name = ev.WorkflowName
	}
	if a.Name == "" {
		a.Name = cmp.Or(ev.Description, name)
	}
	a.Status = ide.Running
	if a.Started.IsZero() {
		a.Started = now
	}
}

// taskAgent finds the subagent or workflow a task event is about; nil when unknown.
func (m *Model) taskAgent(taskID, toolUseID string) *ide.Agent {
	a := m.agentByID(taskID)
	if a == nil {
		a = m.agentByID(toolUseID)
	}
	if a == nil || a.Kind != ide.Subagent && a.Kind != ide.Workflow {
		return nil
	}
	return a
}

func (m *Model) taskProgress(ev protocol.TaskProgress, now time.Time) {
	wf := m.taskAgent(ev.TaskID, ev.ToolUseID)
	if wf == nil || wf.Kind != ide.Workflow {
		return
	}
	for _, it := range ev.Workflow {
		if !it.Phase {
			m.workflowAgent(wf.Key, it, now)
		}
	}
}

// workflowAgent updates the agent of one workflow_agent progress item, adding it when new.
// A queued agent has no id yet: it lives under a placeholder key until the id is known.
func (m *Model) workflowAgent(wf string, it protocol.WorkflowItem, now time.Time) {
	slot := fmt.Sprintf("%s#%d.%d", wf, it.PhaseIndex, it.Index)
	key := cmp.Or(m.crew.slots[slot], slot)
	if it.AgentID != "" && key != it.AgentID {
		if m.Sim.FindAgent(it.AgentID) == nil {
			m.Sim.Rekey(key, it.AgentID)
		} else {
			m.dropAgent(key, it.AgentID) // its transcript showed up first: the placeholder is a duplicate
		}
		key = it.AgentID
	}
	m.crew.slots[slot] = key
	a := m.Sim.FindAgent(key)
	if a == nil {
		a = m.addAgent(key, ide.WorkflowAgent, wf, "")
	}
	a.Name, a.Model = cmp.Or(it.Title, a.Name), cmp.Or(it.Model, a.Model)
	a.Phase, a.Tokens, a.ToolCalls = it.PhaseTitle, it.Tokens, it.ToolCalls
	switch {
	case it.AgentID == "":
		a.Status = ide.Queued
	case it.State == "done":
		m.finish(a, ide.Done, now)
	case slices.Contains([]string{"error", "failed", "killed", "cancelled"}, it.State):
		m.finish(a, ide.Failed, now)
	default:
		a.Status = ide.Running
		if a.Started.IsZero() {
			a.Started = now
		}
		// Progress tells what an agent does when the sim has nothing better: no transcript, or idle.
		if it.LastTool != "" && (a.Activity == "" || a.Activity == m.crew.activity[key]) {
			a.Activity = strings.TrimSpace(it.LastTool + " " + firstLineOf(it.LastToolSummary))
			m.crew.activity[key] = a.Activity
		}
	}
}

// dropAgent removes an agent that never did anything from the IDE state: the agent to stands for
// it, and takes over the camera pinned on it, the panel selection, and its hotkey when lower (the
// other one is free again: no gap).
func (m *Model) dropAgent(key, to string) {
	st := m.Sim.State()
	if a, b := m.Sim.FindAgent(key), m.Sim.FindAgent(to); a != nil && b != nil && a.Hotkey != 0 && (b.Hotkey == 0 || a.Hotkey < b.Hotkey) {
		b.Hotkey = a.Hotkey
	}
	if st.Camera == key && st.Pinned {
		m.Sim.Pin(to)
	}
	if m.PanelSel == key {
		m.PanelSel = to
	}
	st.Agents = slices.DeleteFunc(st.Agents, func(a *ide.Agent) bool { return a.Key == key })
}

func (m *Model) taskDone(ev protocol.TaskDone, now time.Time) {
	a := m.taskAgent(ev.TaskID, ev.ToolUseID)
	if a == nil {
		return
	}
	status := ide.Failed
	if ev.Status == "completed" {
		status = ide.Done
	}
	m.wrapUp(a, status, now)
}

// wrapUp ends a subagent, or a workflow with its agents after a last read of their transcripts.
// The calls they left open end too: nothing will report on them any more.
func (m *Model) wrapUp(a *ide.Agent, status ide.AgentStatus, now time.Time) {
	if a.Kind == ide.Workflow {
		if i := slices.IndexFunc(m.crew.tailers, func(t tailer) bool { return t.workflow == a.Key }); i >= 0 {
			m.follow(m.crew.tailers[i], now)
			m.crew.tailers = slices.Delete(m.crew.tailers, i, i+1)
		}
		for _, b := range m.Sim.State().Agents {
			if b.Parent == a.Key && (b.Status == ide.Running || b.Status == ide.Queued) {
				m.finish(b, status, now)
			}
		}
	}
	m.finish(a, status, now)
	m.abandon(func(key string) bool {
		b := m.Sim.FindAgent(key)
		return b != nil && (b == a || b.Parent == a.Key)
	})
}

// abandon fails the open calls of the agents gone picks by key: their results will never come.
func (m *Model) abandon(gone func(key string) bool) {
	for _, id := range slices.Sorted(maps.Keys(m.crew.calls)) {
		if gone(cmp.Or(m.crew.calls[id].Parent, "main")) {
			m.Sim.ToolResult(id, "", true)
			delete(m.crew.calls, id)
		}
	}
}

// finish ends an agent once; later reports of its end change nothing.
func (m *Model) finish(a *ide.Agent, status ide.AgentStatus, now time.Time) {
	if a.Status == ide.Done || a.Status == ide.Failed {
		return
	}
	a.Status, a.Ended = status, now
	if a.Activity != "" && a.Activity == m.crew.activity[a.Key] {
		a.Activity = ""
	}
}

// follow plays what the agents of a workflow did since the last poll of its transcripts.
func (m *Model) follow(t tailer, now time.Time) {
	for _, ev := range t.t.Poll() {
		switch ev := ev.(type) {
		case protocol.ToolUse:
			if m.Sim.FindAgent(ev.Parent) == nil { // not in the progress yet
				m.addAgent(ev.Parent, ide.WorkflowAgent, t.workflow, ev.Parent[:min(7, len(ev.Parent))]).Started = now
			}
			m.Sim.ToolUse(ev.Parent, ev.ID, ev.Name, ev.Input)
			m.crew.calls[ev.ID] = ev
		case protocol.ToolResult:
			delete(m.crew.calls, ev.ID)
			m.Sim.ToolResult(ev.ID, ev.Output, ev.IsError)
		}
	}
}

func (m *Model) tick(now time.Time) {
	m.Now = now
	if len(m.pending) > 0 && m.queueAt < 0 && !m.Status.Replay && m.Status.State != "exited" && now.Sub(m.pendingAt) >= 5*time.Second {
		if _, isCmd := slash(m.pending[0]); !isCmd && !m.cmdTurn {
			m.flush()
		}
	}
	if m.inspection != nil {
		switch {
		case m.mouseDown:
			m.inspection.until = now.Add(inspectionCooldown)
		case !now.Before(m.inspection.until):
			m.inspection = nil
		}
	}
	if len(m.crew.tailers) > 0 && now.Sub(m.crew.polled) >= pollEvery {
		m.crew.polled = now
		for _, t := range m.crew.tailers {
			m.follow(t, now)
		}
	}
	m.replayAllow(now)
	m.Sim.Tick(now)
	m.Status.Frame = int(now.UnixMilli() / 100)
}

// exited ends everything still running: claude is gone, nothing will report on it any more.
func (m *Model) exited(now time.Time) {
	m.Status.Tasks, m.wakeup = 0, time.Time{}
	m.tasks = nil
	m.inspection = nil
	m.abandon(func(string) bool { return true })
	for _, a := range m.Sim.State().Agents {
		if a.Kind != ide.Main && (a.Status == ide.Running || a.Status == ide.Queued) {
			m.finish(a, ide.Failed, now)
		}
	}
	m.crew.tailers = nil
}

// sync derives the rest of the UI state from the agents after every message: the one automatic
// switch of the left panel to the agents tree, the status of main, and the camera in the status line.
func (m *Model) sync() {
	st := m.Sim.State()
	m.Status.Wakeup = ""
	if !m.wakeup.IsZero() {
		left := max(m.wakeup.Sub(m.Now), 0)
		m.Status.Wakeup = (time.Duration(math.Ceil(left.Seconds())) * time.Second).String()
	}
	// The left column shows the agents while others than main are at work, the files otherwise.
	if m.inspection == nil {
		m.Panel = ideview.Files
		if slices.ContainsFunc(st.Agents, func(a *ide.Agent) bool {
			return a.Kind != ide.Main && (a.Status == ide.Running || a.Status == ide.Queued)
		}) {
			m.Panel = ideview.Agents
		}
	}
	m.PanelFocus = m.PanelFocus && m.Panel == ideview.Agents
	st.Agents[0].Status = ide.Done // main between turns
	if m.Status.Busy {
		st.Agents[0].Status = ide.Running
	}
	m.syncSuggest()
	if m.Popup != m.shownPopup {
		m.shownPopup, m.popupAt = m.Popup, m.Now
		m.notice = popupNotice(m.Popup)
		m.streak = m.Popup != nil && !m.typedAt.IsZero() && m.clock().Sub(m.typedAt) < popupGrace
	}
	m.Sim.Ask(popupAgent(m.Popup))
	switch {
	case !m.Status.Busy:
		m.turnAt, m.Status.Elapsed = time.Time{}, ""
	case m.turnAt.IsZero():
		m.turnAt = m.Now
	default:
		m.Status.Elapsed = elapsed(m.Now.Sub(m.turnAt))
	}
	m.Status.Agents = 0
	for _, a := range st.Agents {
		if (a.Kind == ide.Subagent || a.Kind == ide.WorkflowAgent) && a.Status == ide.Running {
			m.Status.Agents++
		}
	}
	m.Status.Usage = usage(m.limits, m.cost, m.Now)
	m.Status.Context = 0
	if w := m.windows[m.context.Model]; w > 0 {
		m.Status.Context = float64(m.context.Tokens) / float64(w)
	}
	m.Status.CacheLeft, m.Status.CacheTTL = 0, 0
	if !m.cacheAt.IsZero() {
		m.Status.CacheTTL = cmp.Or(m.cacheTTL, defaultCacheTTL)
		m.Status.CacheLeft = max(0, m.cacheAt.Add(m.Status.CacheTTL).Sub(m.Now))
	}
}

// usage formats the use of the rate-limit windows, each with the time until it resets: "5h 9%
// 4h12m · 7d 58% fable 3% 2d8h" (fable is a bucket of the week: it resets with it). A window at
// its limit says until when ("5h limit until 13:30"), unless extra usage pays past it: then
// "extra usage" ends the line. A window past its reset is left out, as in Claude Code: the next response
// reports it anew. A session that has no windows (an API key) shows its cost instead: "$0.0123".
func usage(l protocol.RateLimits, cost float64, now time.Time) string {
	if l.Session == nil && l.Week == nil && l.Fable == nil {
		if cost > 0 {
			return fmt.Sprintf("$%.4f", cost)
		}
		return ""
	}
	paid := l.Overage == "allowed" || l.Overage == "allowed_warning"
	var parts []string
	extra := false
	for _, w := range []struct {
		name string
		w    *protocol.Window
		with *protocol.Window // a bucket shown with it, before the reset
	}{{"5h", l.Session, nil}, {"7d", l.Week, l.Fable}, {"fable", l.Fable, nil}} {
		switch {
		case w.w == nil || !w.w.ResetsAt.After(now):
		case w.name == "fable" && l.Week != nil && l.Week.ResetsAt.After(now): // shown with the week
		case w.w.Status == "rejected" && !paid:
			parts = append(parts, w.name+" limit until "+clockTime(w.w.ResetsAt.In(now.Location()), now))
		default:
			extra = extra || w.w.Status == "rejected"
			// Rounded first, so that 0.58 is 58% and not 57.
			part := fmt.Sprintf("%s %d%%", w.name, int(math.Round(w.w.Used*1e4)/100))
			if b := w.with; b != nil && b.ResetsAt.After(now) {
				extra = extra || b.Status == "rejected"
				part += fmt.Sprintf(" fable %d%%", int(math.Round(b.Used*1e4)/100))
			}
			parts = append(parts, part+" "+untilReset(w.w.ResetsAt.Sub(now)))
		}
	}
	if extra {
		parts = append(parts, drawExtraUsage)
	}
	return strings.Join(parts, " · ")
}

// untilReset is how long until a window resets: to the minute within a day, "42m", "4h6m", "4h";
// to the hour past it, "3d4h", "3d".
func untilReset(d time.Duration) string {
	d = max(d, 0)
	m := int(d.Round(time.Minute) / time.Minute)
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	n, unit, rest, restUnit := m/60, "h", m%60, "m"
	if m >= 24*60 {
		h := int(d.Round(time.Hour) / time.Hour)
		n, unit, rest, restUnit = h/24, "d", h%24, "h"
	}
	s := fmt.Sprintf("%d%s", n, unit)
	if rest > 0 {
		s += fmt.Sprintf("%d%s", rest, restUnit)
	}
	return s
}

// clockTime is t as a clock shows it, "13:30", with its date before it when that is not now's:
// "Oct 31 12:00".
func clockTime(t, now time.Time) string {
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// elapsed formats a running time for the status line: "7s", "1m05s", "1h02m".
func elapsed(d time.Duration) string {
	d = max(d, 0).Truncate(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", d/time.Second)
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", d/time.Minute, d%time.Minute/time.Second)
	}
	return fmt.Sprintf("%dh%02dm", d/time.Hour, d%time.Hour/time.Minute)
}
