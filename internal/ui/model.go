// Package ui is the Bubble Tea program: a simulated IDE on top, the conversation and the prompt
// below it, and popups when the agent needs an answer.
package ui

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/history"
	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/protocol"
	"github.com/pixelbender/claudit/internal/sessions"
	"github.com/pixelbender/claudit/internal/sim"
)

// AgentMsg delivers the agent events that arrived since the last one, oldest first.
type AgentMsg struct{ Events []protocol.Event }

// Model is the whole UI state. View and Update share it through a pointer.
type Model struct {
	Sim        *sim.Sim
	Chat       []ChatEntry
	Input      Input
	Popup      Popup // nil when none is open
	Status     Status
	wakeup     time.Time                 // confirmed next dynamic-loop wakeup; zero when none is pending
	tasks      []protocol.BackgroundTask // the CLI's current unfinished background tasks
	ChatScroll int                       // chat lines scrolled up from the bottom; 0 follows the tail
	Width      int
	Height     int
	Now        time.Time // time of the last tick, for durations and spinners in the view

	Panel      ideview.Panel // what the IDE's left column shows
	PanelFocus bool          // keys go to the left column instead of the prompt
	PanelSel   string        // key of the agent highlighted in the agents panel
	ShowHelp   bool          // F1: the key reference over everything
	Wrap       bool          // the editor wraps long lines (alt+z)

	Suggest    []protocol.Command // slash commands completing the prompt, shown above it
	SuggestSel int                // highlighted one
	Away       bool               // the terminal lost the focus: what needs the user is announced
	StartModel string             // the model claude starts with as args or settings name it ("opus"), shown until a turn tells

	chatMax    int               // largest useful ChatScroll, written by View
	chatLines  chatCache         // drawn chat entries, kept by View
	zones      []zone            // what clicks act on, written by View
	sel        selection         // the text the mouse drags over; View writes what it covers
	scroll     ideview.Scroll    // how far the wheel scrolled the IDE's panes; View forgets those it no longer draws
	termCache  ideview.TermCache // wrapped row counts for the visible terminals
	burst      wheelBurst        // the wheel events Filter held back since the last frame of a scroll
	inspection *inspection       // the IDE the user is reading during the interaction cooldown
	ideHeight  int               // IDE height in the last frame; -1 before the first frame
	mouseDown  bool              // a held button protects the view until five seconds after release
	send       func(protocol.Outbound)
	queue      []Popup          // requests waiting behind the open popup
	behind     string           // the state of main while "waiting for you" covers it
	clock      func() time.Time // the time of agent events; ticks bring their own
	crew       crew             // what the model tracks about the agents beyond ide.Agent
	agentMode  string           // the permission mode the agent last reported; Status.Mode runs ahead of it
	extraMode  string           // a reported permission mode beyond modeCycle; shift+tab reaches it too
	modeReqs   int              // SetMode requests sent, for their ids
	modelReqs  int              // SetModel requests sent, for their ids

	btwReqs int               // side questions asked, for their ids
	btwSaid []protocol.SideQA // the side questions answered in this conversation, for follow-ups

	commands     []protocol.Command // slash commands to complete, sorted by name
	known        map[string]bool    // every slash command and alias claude runs, hidden ones too
	cli          cliInfo            // what claude tells about itself, for /status and the pickers
	unknown      string             // a prompt whose command claude does not know, typed once: again sends it
	suggestFor   string             // the prompt Suggest was made for
	shownPopup   Popup              // the popup popupAt is about
	popupAt      time.Time          // when the open popup came up, for its drop-in
	notice       string             // what to announce after this message, if Away
	typedAt      time.Time          // when a key last went to the prompt
	streak       bool               // the open popup came up mid-typing: keys go on to the prompt until a pause
	history      []history.Entry    // prompts sent, oldest first: Claude Code's of the project, then this session's
	historyFile  string             // where prompts sent are added for Claude Code too; "" for nowhere
	historyDir   string             // the project the history file names them by
	historyOld   int                // how many of history are earlier sessions': their pictures are not in attached
	histAt       int                // the history entry in the prompt while browsing it; len(history) when not
	histDraft    Input              // the prompt as typed before browsing the history
	interrupting bool               // an Interrupt was sent: the error Result it brings ends the turn as asked
	interrupts   int                // Interrupts sent, for their ids
	cmdTurn      bool               // a slash command started the turn: it is not in the chat to be cut
	onRelease    func()             // what a press on an agent in the IDE or a Bash line of the chat does as the button lets go; a drag does nothing
	onDrag       func(x, y int)     // a scrollbar holds the mouse until release
	effortTurn   bool               // the turn runs /effort: claude's settings tell the level it set once it ends
	quitAt       time.Time          // when quitKey last asked to be pressed again to quit
	quitKey      string             // ctrl+c or esc
	stderr       []string           // the last lines claude wrote to stderr, for the report of its exit
	exitReport   string             // why claude exited, when it failed; "" while it runs or after a clean exit
	turnAt       time.Time          // when the current turn began
	session      string             // id of the conversation, once claude reports it
	failure      string             // what went wrong in the turn, for the status once it ends
	usage        protocol.Usage     // what claude's process has taken, as of the last result
	usageKnown   bool               // usage counts this process alone: false until its first result

	limits    protocol.RateLimits // the rate-limit use claude reported last
	cost      float64             // the session's cost in USD, as of the last turn
	context   protocol.Context    // how full main's context was at its last model call
	windows   map[string]int      // the context window of each model, from the turns' results
	pending   []string            // prompts typed while claude works, held until a quiet period or its turn ends
	pendingAt time.Time           // when a prompt was last queued or its edit saved
	queueAt   int                 // index in pending of the prompt taken into the prompt to edit; -1 for none

	attached map[string]attachment // what the placeholders of long pastes and pictures stand for, by placeholder

	bangRun  *bangRun // the ! command running; nil for none
	bangs    int      // ! commands run, for their ids
	bangSaid []string // the ! commands run since the last prompt, as claude is to see them with the next
}

type EntryKind int

const (
	UserEntry EntryKind = iota
	AssistantEntry
	CommandEntry // the CLI's own reply: a slash command's output or an API error
	CaretEntry   // where claude is while it works: a spin frame under what it got or said last
)

// ChatEntry is one item of the conversation: a prompt or a reply. What the agents do shows in
// the IDE, not here.
type ChatEntry struct {
	Kind     EntryKind
	Text     string
	Queued   bool           // a prompt typed while claude works, drawn under the chat until it goes to claude
	Cut      bool           // a prompt whose turn the user interrupted: red, a ✗ for its arrow
	Answer   bool           // the user's answer to a question of a popup, drawn right under it
	GoalEcho bool           // a locally displayed goal awaiting the CLI's confirmation
	Bash     bool           // a reply's: what a Bash call of main's says it does, no reply of claude's
	Call     string         // a Bash reply's: the id of its call, whose lines a click finds in main's terminal
	Bang     string         // a CommandEntry's: the ! command whose output Text is; "" while it runs
	At       time.Time      // when it was said, shown grey at the right; zero for not known
	Tokens   protocol.Usage // a prompt's: the tokens its turn took, zero for not known; not drawn while the count is wrong
}

// Status feeds the status line.
type Status struct {
	Model   string
	Effort  string  // of the model: "low" … "max"; "" when not known, for a model without efforts, or "auto" until claude tells its level
	State   string  // "starting", "idle", "thinking", "responding", "running", "waiting for you", "exited"
	Busy    bool    // the agent is working: show a spinner
	Noted   float64 // a copy to the clipboard just made: how bright its note is in place of the hints, 1 down to 0
	Frame   int     // spinner frame
	Usage   string  // rate-limit use, "5h 9% · 7d 58% · fable 3%", or the cost, "$0.0123", without limits
	Elapsed string  // how long the current turn has run, e.g. "12s"; "" between turns
	Agents  int     // subagents and workflow agents at work
	Tasks   int     // live background tasks, from the CLI's complete snapshot
	Wakeup  string  // time until the next confirmed wakeup, e.g. "7m5s"; "" when unknown
	Replay  bool
	Mode    string // permission mode: "default", "acceptEdits", "plan", …; "" until known
	Alert   bool   // State says what went wrong, such as an API error: red

	Context float64 // fraction of main's context window in use; 0 until the window is known
}

// Popup is a modal question from the agent, *QuestionPopup or *PermissionPopup, or a list the
// user opened: *ResumePopup of /resume, *PickPopup of /model and the like, *PluginsPopup of /plugins;
// or *BtwPopup, the answer to /btw.
type Popup interface{ isPopup() }

// QuestionPopup answers an AskUserQuestion call, one question at a time.
type QuestionPopup struct {
	Agent      string // key of the agent asking
	AgentName  string // the subagent asking; "" for the main agent
	AgentColor int
	RequestID  string
	Input      map[string]any // the tool input, echoed back with "answers" added
	Questions  []Question
	Current    int // index of the question on screen
}

type Question struct {
	Header  string
	Text    string
	Options []Option
	Multi   bool
	Cursor  int    // highlighted row: 0..len(Options)-1 are options, len(Options) is "Other"
	Checked []bool // multi-select marks, len(Options)+1
	Other   Input  // free text for the "Other" row
}

type Option struct{ Label, Description string }

// PermissionPopup asks whether a tool may run.
type PermissionPopup struct {
	Agent      string // key of the agent asking
	AgentName  string // the subagent asking; "" for the main agent
	AgentColor int
	RequestID  string
	ToolUseID  string // the call asking, whose scene waits for the answer
	Tool       string
	Input      map[string]any
	Detail     string // what the tool is about to do, ready to show; may span lines
	Scroll     int    // first line of Detail on screen
	Allow      bool   // highlighted button
}

// ResumePopup lists the saved conversations of the project, the latest first, the one claude is
// in among them: the list looks the same whichever is resumed.
type ResumePopup struct {
	Sessions []sessions.Info
	Current  string // id of the conversation claude is in, marked; picking it changes nothing
	Cursor   int
	Now      time.Time // when the list was read, for the ages
}

func (*QuestionPopup) isPopup()   {}
func (*PermissionPopup) isPopup() {}
func (*ResumePopup) isPopup()     {}

// New builds the model. send delivers messages to the agent; cwd seeds the explorer until the session reports its own.
func New(send func(protocol.Outbound), cwd string, replay bool) *Model {
	m := &Model{
		Sim:    sim.New(cwd),
		Status: Status{State: "starting", Replay: replay},
		Wrap:   true,
		Now:    time.Now(),
		send:   send,
		clock:  time.Now,
		crew:   newCrew(),

		known:     map[string]bool{},
		scroll:    ideview.Scroll{},
		termCache: ideview.TermCache{},
		attached:  map[string]attachment{},
		queueAt:   -1,
		ideHeight: -1,
	}
	m.addCommands(nil)
	m.sync()
	return m
}

func (m *Model) Init() tea.Cmd {
	return m.nextTick()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.onDrag = nil
		m.Width, m.Height = msg.Width, msg.Height
	case tickMsg:
		m.tick(time.Time(msg))
		cmd = m.nextTick()
	case AgentMsg:
		for _, ev := range msg.Events {
			m.handleEvent(ev)
		}
	case sessionsMsg:
		cmd = m.listed(msg)
	case historyMsg:
		m.resumed(msg)
	case pluginsMsg:
		m.pluginsRead(msg)
	case pluginChangedMsg:
		cmd = m.pluginChanged(msg)
	case exportedMsg:
		if msg.err != nil {
			m.local("Error: /export: " + msg.err.Error())
		} else {
			m.local("Exported to " + msg.path)
		}
	case tea.PasteMsg:
		m.interact()
		cmd = m.paste(msg.Content)
	case imagesMsg:
		m.pasted(msg)
	case bangDoneMsg:
		m.bangDone(msg)
	case tea.KeyPressMsg:
		m.interact()
		cmd = m.handleKey(msg)
	case tea.MouseWheelMsg:
		if d := wheelNotch(msg); !m.tooSmall() && d != 0 {
			m.wheel(msg.X, msg.Y, d)
			cmd = m.holdWheel()
		}
	case wheelMsg:
		cmd = m.flushWheel()
	case tea.MouseClickMsg:
		if !m.tooSmall() && msg.Button == tea.MouseLeft {
			m.interact()
			m.mouseDown = true
			m.onDrag = nil
			m.sel = m.selectAt(msg.X, msg.Y)
			cmd = m.click(msg.X, msg.Y)
		}
	case tea.MouseMotionMsg:
		if msg.Button == tea.MouseLeft {
			m.interact()
			if m.onDrag != nil {
				m.onDrag(msg.X, msg.Y)
			} else {
				m.dragTo(msg.X, msg.Y)
			}
		}
	case tea.MouseReleaseMsg:
		m.onDrag = nil
		if m.mouseDown {
			m.interact()
			m.mouseDown = false
		}
		if m.onRelease != nil && !m.sel.drag {
			m.onRelease()
		}
		m.onRelease = nil
		cmd = m.copySelection()
	case tea.FocusMsg:
		m.Away = false
	case tea.BlurMsg:
		m.onDrag = nil
		m.Away = true
		m.mouseDown = false
	}
	m.sync()
	if m.notice != "" {
		if m.Away {
			cmd = tea.Batch(cmd, tea.Raw(drawNotice(m.notice)))
		}
		m.notice = ""
	}
	return m, cmd
}

type tickMsg time.Time

const (
	tickBusy    = 33 * time.Millisecond // 30 fps: smooth typing at half the CPU of 60
	tickAmbient = 50 * time.Millisecond // effects that move by themselves
	tickIdle    = 200 * time.Millisecond
)

func (m *Model) tickInterval() time.Duration {
	switch {
	case m.Sim.Busy() || m.Status.Busy || len(m.crew.tailers) > 0:
		return tickBusy
	case m.animating():
		return tickAmbient
	}
	return tickIdle
}

// animating reports whether something on screen moves by itself: a popup dropping in, the spinner
// while claude starts, a copied selection and its note going out.
func (m *Model) animating() bool {
	return m.Status.State == "starting" || !m.popupAt.IsZero() && m.Now.Sub(m.popupAt) < drawPopupDrop ||
		m.sel.noted(m.Now) > 0 // it outlasts the selection's light
}

func (m *Model) nextTick() tea.Cmd {
	return tea.Tick(m.tickInterval(), func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) handleEvent(ev protocol.Event) {
	root, now := m.Sim.State().Root, m.clock()
	switch ev := ev.(type) {
	case protocol.Init:
		if m.session != "" && m.session != ev.SessionID {
			m.Status.Tasks, m.wakeup = 0, time.Time{}
			m.tasks = nil
			m.inspection = nil
		}
		m.Status.Model, m.session = ev.Model, ev.SessionID
		m.Status.Effort = cmp.Or(ev.Effort, m.Status.Effort) // claude -p (2.1.314) leaves it out
		if ev.Cwd != "" {
			m.Sim.SetRoot(ev.Cwd)
		}
		if ev.PermissionMode != "" {
			m.setMode(ev.PermissionMode)
		}
		m.cli.outputStyle, m.cli.mcp = ev.OutputStyle, ev.MCP
		m.setVersion(ev.Version)
		var list []protocol.Command
		for _, c := range ev.Commands {
			list = append(list, protocol.Command{Name: c})
		}
		m.addCommands(list)
		// Live sessions report init after the first prompt; keep "thinking" then.
		if !m.Status.Busy {
			m.Status.State = "idle"
		}
	case protocol.ModeChanged:
		m.setMode(ev.Mode)
	case protocol.ModelChanged:
		m.Status.Model = ev.Model // the status says it: the chat need not
		// Each model has an effort of its own. Asked only now: claude answers get_settings
		// before a set_model sent ahead of it.
		m.send(protocol.GetSettings{RequestID: fmt.Sprintf("settings-%d", m.modelReqs)})
	case protocol.ServingModel:
		m.Status.Model = ev.Model
		m.Sim.FindAgent("main").Model = ev.Model
	case protocol.Settings:
		m.Status.Effort = ev.Effort
	case protocol.SideAnswer:
		m.btwAnswered(ev.RequestID, ev.Text, false)
	case protocol.ControlError:
		// The CLI answers requests in order, so after a refused switch the agent is in the mode it reported last.
		if strings.HasPrefix(ev.RequestID, "mode-") {
			m.Status.Mode = m.agentMode
		}
		if strings.HasPrefix(ev.RequestID, "btw-") {
			m.btwAnswered(ev.RequestID, ev.Message, true)
			break
		}
		if strings.HasPrefix(ev.RequestID, "settings") { // a claude without get_settings: the effort stays unknown
			break
		}
		m.local("Error: " + ev.Message)
	case protocol.APIRetry:
		if m.Status.Busy { // a side call's or a background agent's starts no turn
			m.working(retryState(ev))
		}
	case protocol.Compacting:
		state := m.Status.State
		if state == "waiting for you" {
			state = m.behind
		}
		switch {
		case !m.Status.Busy:
		case ev.On:
			m.working("compacting")
		case state == "compacting": // the call that goes on with the turn is on its way
			m.working("thinking")
		}
	case protocol.ModelFallback:
		m.Status.Model = ev.To // until the next turn's Init names the model again
		m.Sim.FindAgent("main").Model = ev.To
		// After what was said before it: the call that fell back has said nothing yet.
		m.Chat = append(m.Chat, ChatEntry{Kind: CommandEntry, At: now,
			Text: cmp.Or(ev.Text, fmt.Sprintf("%s failed (%s): this turn runs on %s", ev.From, ev.Trigger, ev.To))})
	case protocol.Commands:
		m.addCommands(ev.List)
		m.cli.models, m.cli.outputStyles, m.cli.account = ev.Models, ev.OutputStyles, ev.Account
		m.setVersion(ev.Version)
		if m.Status.Model == "" && m.StartModel != "" { // claude -p tells its model only with a turn
			m.Status.Model = m.StartModel
			for _, md := range ev.Models {
				if md.Value == m.StartModel {
					m.Status.Model = md.Resolved
				}
			}
		}
		if m.Status.State == "starting" { // claude answered: it is ready
			m.Status.State = "idle"
		}
	case protocol.Reset:
		m.Chat, m.chatLines, m.ChatScroll, m.btwSaid = nil, chatCache{}, 0, nil
		m.context = protocol.Context{}
		m.local("Conversation cleared")
	case protocol.Thinking:
		m.working("thinking")
	case protocol.TextStart:
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, At: now})
		m.working("responding")
	case protocol.TextDelta:
		if n := len(m.Chat); n == 0 || m.Chat[n-1].Kind != AssistantEntry || m.Chat[n-1].Bash {
			m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, At: now})
		}
		m.Chat[len(m.Chat)-1].Text += ev.Text
	case protocol.CLIReply:
		if f := apiFailure(ev.Text); f != "" { // the status says it; the chat stays the conversation
			m.failure = f
			break
		}
		if m.confirmGoal(ev.Text) {
			break
		}
		m.Chat = append(m.Chat, ChatEntry{Kind: CommandEntry, Text: ev.Text, At: now})
	case protocol.ToolUse:
		m.toolUse(ev, now)
		if ev.Parent == "" { // the status line is about main
			m.working("running")
		}
		if d := inputString(ev.Input, "description"); ev.Parent == "" && ev.Name == "Bash" && d != "" {
			m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: d, At: now, Bash: true, Call: ev.ID}) // what it does, in claude's words
		}
	case protocol.ToolResult:
		m.toolResult(ev, now)
	case protocol.TaskStarted:
		m.taskStarted(ev, now)
	case protocol.TaskProgress:
		m.taskProgress(ev, now)
	case protocol.TaskDone:
		m.taskDone(ev, now)
	case protocol.BackgroundTasks:
		m.tasks, m.Status.Tasks = ev.Tasks, len(ev.Tasks)
		if p, ok := m.Popup.(*QuitPopup); ok {
			p.Tasks = ev.Tasks
		}
	case protocol.WakeupScheduled:
		m.wakeup = ev.At
	case protocol.CommandStarted:
		if ev.WakeupSource == "loop_wakeup" && ev.ScheduledFor.Equal(m.wakeup) {
			m.wakeup = time.Time{}
		}
		if !m.Status.Busy {
			m.working("thinking")
		}
	case protocol.PermissionRequest:
		var asker *ide.Agent
		if a := m.agentByID(ev.AgentID); a != nil && a.Kind != ide.Main {
			asker = a
		}
		m.Sim.Hold(ev.ToolUseID) // no edit on screen before the user permits it
		p := newPopup(ev, root, asker)
		if m.Status.State != "waiting for you" {
			m.behind = m.Status.State
		}
		if m.Popup == nil {
			m.Popup = p
		} else {
			m.queue = append(m.queue, p)
		}
		m.Status.State = "waiting for you"
	case protocol.RateLimits:
		m.limits = ev
	case protocol.Context:
		m.context = ev
		if ev.Model != "" { // what main's call runs on, a fallback's model too, not what the turn asked for
			m.Status.Model = ev.Model
			m.Sim.FindAgent("main").Model = ev.Model
		}
	case protocol.Result:
		if m.cmdTurn {
			for i := range m.Chat {
				m.Chat[i].GoalEcho = false
			}
		}
		interrupted := m.interrupting && ev.IsError
		m.interrupting = false
		switch {
		case interrupted:
			if !m.cmdTurn {
				m.cut()
			}
		case ev.IsError:
			m.notice = "claude stopped: " + ev.Text
		case m.Status.Busy && m.Status.Elapsed != "":
			m.notice = "claude is done · " + m.Status.Elapsed
		case m.Status.Busy:
			m.notice = "claude is done"
		}
		m.Status.State, m.Status.Busy = "idle", false
		if m.effortTurn { // "auto" is the model's own level: claude knows which
			m.effortTurn = false
			m.send(protocol.GetSettings{RequestID: "settings-effort"})
		}
		m.cost = ev.CostUSD
		if m.windows == nil {
			m.windows = map[string]int{}
		}
		maps.Copy(m.windows, ev.ContextWindows)
		if ev.IsError && !interrupted && strings.TrimSpace(ev.Text) != "" {
			m.failure = cmp.Or(m.failure, failureState(ev.Text))
		}
		if m.failure != "" { // what went wrong stays in the status until claude goes on
			m.Status.State, m.Status.Alert, m.failure = m.failure, true, ""
		}
		// The result counts the whole process, subagents too: the turn took the difference. A
		// process's first result may count earlier processes of the conversation (--resume): the
		// turn's own count, main's alone, stands in for it.
		took := ev.Turn
		if m.usageKnown {
			took = protocol.Usage{Input: ev.Usage.Input - m.usage.Input, Output: ev.Usage.Output - m.usage.Output,
				CacheRead: ev.Usage.CacheRead - m.usage.CacheRead, CacheWrite: ev.Usage.CacheWrite - m.usage.CacheWrite}
		}
		m.usage, m.usageKnown = ev.Usage, true
		if i := m.turnPrompt(); i >= 0 { // a turn claude starts by itself adds to the prompt's
			t := &m.Chat[i].Tokens
			t.Input, t.Output, t.CacheRead, t.CacheWrite = t.Input+took.Input, t.Output+took.Output, t.CacheRead+took.CacheRead, t.CacheWrite+took.CacheWrite
		}
		switch {
		case len(m.pending) > 0: // claude goes on with what was typed meanwhile: it is not done
			m.notice = ""
			m.flush()
		case len(m.bangSaid) > 0: // or with the ! commands run meanwhile
			m.notice = ""
			m.sendBangs()
		}
	case protocol.Stderr:
		m.stderr = append(m.stderr[max(0, len(m.stderr)-maxStderr+1):], ev.Line)
	case protocol.Exited:
		first := m.Status.State != "exited"
		m.Status.State, m.Status.Busy = "exited", false
		// The reason stays in the conversation.
		report := "claude exited"
		if ev.Err != nil {
			report = "Error: claude exited (" + ev.Err.Error() + ")"
		}
		for _, l := range m.stderr {
			report += "\n  " + l
		}
		if slices.ContainsFunc(m.stderr, func(l string) bool { // a flag, or a value of one, it does not know
			return strings.Contains(l, "unknown option") || strings.Contains(l, "Allowed choices are")
		}) {
			report += "\n  claudit needs claude " + minClaude + " or newer · " + updateHint
		}
		if first {
			m.Chat = append(m.Chat, ChatEntry{Kind: CommandEntry, Text: report, At: now})
			if ev.Err != nil || len(m.stderr) > 0 {
				m.exitReport = strings.TrimPrefix(report, "Error: ")
			}
		}
		// Nobody is left to answer the popups or to finish the tool calls: end their scenes.
		m.Popup, m.queue = nil, nil
		m.exited(now)
	}
}

// retryState is the status while an API call waits to go again: why it failed, and which try comes.
func retryState(r protocol.APIRetry) string {
	why := fmt.Sprintf("API error %d", r.Status)
	switch r.Status {
	case 0:
		why = "connection error"
	case 429:
		why = "rate limited"
	case 529:
		why = "overloaded"
	}
	return fmt.Sprintf("%s · retry %d/%d", why, r.Attempt, r.MaxRetries)
}

// turnState is the status of a turn a prompt starts: "compacting" for /compact, "thinking" else.
func turnState(cmd string, isCmd bool) string {
	if isCmd && cmd == "compact" {
		return "compacting"
	}
	return "thinking"
}

// turnPrompt is the index of the prompt of the turn that ends: the last in the chat, if nothing
// claude said since is before it; an answer to claude's question is no prompt. -1 for none (a
// slash command's turn).
func (m *Model) turnPrompt() int {
	if m.cmdTurn {
		return -1
	}
	for i := len(m.Chat) - 1; i >= 0; i-- {
		if m.Chat[i].Kind == UserEntry && !m.Chat[i].Answer {
			return i
		}
	}
	return -1
}

// apiFailures start the CLI's replies that say an API call failed: they go to the status, not
// the chat.
var apiFailures = []string{"API Error", "API error", "Credit balance is too low", "Invalid API key", "OAuth token",
	"Request timed out", "Prompt is too long"}

// apiFailure is the status for a reply of the CLI that says an API call failed; "" for another.
func apiFailure(text string) string {
	if t := strings.TrimSpace(text); slices.ContainsFunc(apiFailures, func(p string) bool { return strings.HasPrefix(t, p) }) {
		return failureState(t)
	}
	return ""
}

// apiMessage is the message of an API error's body: {"error":{"message":"Overloaded"}}.
var apiMessage = regexp.MustCompile(`"message"\s*:\s*"([^"]*)"`)

// failureState is what went wrong, short for the status: "API error 529 · Overloaded".
func failureState(text string) string {
	t := drawOneLine(strings.TrimSpace(text))
	rest, ok := strings.CutPrefix(t, "API Error: ")
	if !ok {
		return t
	}
	code, body, _ := strings.Cut(rest, " ")
	if _, err := strconv.Atoi(code); err != nil {
		return "API error · " + rest // "Request timed out."
	}
	if m := apiMessage.FindStringSubmatch(body); m != nil {
		body = m[1]
	}
	return strings.TrimSuffix("API error "+code+" · "+body, " · ")
}

// maxStderr is how many of claude's last stderr lines the report of its exit keeps.
const maxStderr = 6

// ExitReport says why claude exited, when it failed while the UI ran; "" otherwise. claudit
// prints it once the screen is gone.
func (m *Model) ExitReport() string { return m.exitReport }

// modeCycle is the order shift+tab walks the permission modes in, as in Claude Code.
var modeCycle = []string{"default", "acceptEdits", "plan", "auto"}

// setMode shows the permission mode the agent reports. A mode beyond modeCycle (one the session
// was started in, such as bypassPermissions) joins the cycle, so shift+tab can come back to it.
func (m *Model) setMode(mode string) {
	if mode == "manual" { // what claude calls the default mode since 2.1.200
		mode = "default"
	}
	m.agentMode = mode
	if !slices.Contains(modeCycle, mode) {
		m.extraMode = mode
	}
	if m.Status.Replay && m.modeReqs > 0 {
		return // a replay plays on in the mode its viewer chose, whatever the recording says
	}
	m.Status.Mode = mode
}

// cycleMode switches the agent to the next permission mode. The status shows it at once;
// ModeChanged confirms it, ControlError takes it back. claude takes the switch only once it has
// the answers it waits for, and does not ask again what it asked already: the requests the new
// mode lets through are allowed first.
func (m *Model) cycleMode() {
	if m.Status.State == "exited" {
		return
	}
	cycle := modeCycle
	if m.extraMode != "" {
		cycle = append(slices.Clone(modeCycle), m.extraMode)
	}
	next := cycle[(slices.Index(cycle, cmp.Or(m.Status.Mode, "default"))+1)%len(cycle)]
	m.allowFor(next)
	m.modeReqs++
	m.send(protocol.SetMode{RequestID: fmt.Sprintf("mode-%d", m.modeReqs), Mode: next})
	m.Status.Mode = next
}

// working marks main busy with state, unless a popup is waiting for the user: that says more.
func (m *Model) working(state string) {
	m.Status.Alert, m.failure = false, ""    // claude goes on: what went wrong is past
	if m.Status.State == "waiting for you" { // only a question covers it; a list the user opened does not
		m.behind = state
	} else {
		m.Status.State = state
	}
	m.Status.Busy = true
}

func (m *Model) handleKey(k tea.KeyPressMsg) tea.Cmd {
	if k.String() == "ctrl+c" {
		if _, ok := m.Popup.(*QuitPopup); ok {
			m.cancelQuit()
			return nil
		}
		return m.ctrlC()
	}
	if m.Width > 0 && m.tooSmall() { // nothing on screen to aim the keys at
		return nil
	}
	// shift+tab switches the mode even over a popup: the questions the new mode would not have
	// asked are answered with it.
	if k.String() == "shift+tab" && !m.ShowHelp {
		m.cycleMode()
		return nil
	}
	// A popup takes every key; what was being typed when it came up goes nowhere, so that a
	// sentence never answers it.
	switch p := m.Popup.(type) {
	case *QuitPopup:
		return m.quitPopupKey(p, k)
	case *ResumePopup: // the user opened it: no typing runs on into it
		return m.resumeKey(p, k.String())
	case *PickPopup:
		return m.pickKey(p, k.String())
	case *BtwPopup:
		m.btwKey(p, k.String())
		return nil
	case *PluginsPopup:
		return m.pluginsKey(p, k)
	case *QuestionPopup:
		if !m.typing(k) {
			m.questionKey(p, k)
		}
		return nil
	case *PermissionPopup:
		if !m.typing(k) {
			m.permissionKey(p, k)
		}
		return nil
	}
	if m.ShowHelp {
		if k.Text == "" {
			if key := k.String(); key == "esc" || key == "f1" || key == "enter" {
				m.ShowHelp = false
			}
			return nil
		}
		m.ShowHelp = false // typing goes on to the prompt
	}
	if m.PanelFocus && k.Text != "" {
		m.PanelFocus = false // typing goes back to the prompt
	}
	if !m.PanelFocus && !m.browsing() && m.queueAt < 0 && m.suggestKey(k.String()) {
		return nil
	}
	if m.globalKey(k.String()) {
		return nil
	}
	if m.PanelFocus {
		m.panelKey(k.String())
		return nil
	}
	m.typedAt = m.clock()
	typed := string(m.Input.Text)
	defer func() {
		if string(m.Input.Text) != typed && k.String() != "up" && k.String() != "down" {
			m.histAt = len(m.history) // an edit ends browsing the history
		}
	}()
	switch k.String() {
	case "enter":
		if c := m.Input.Cursor; c > 0 && m.Input.Text[c-1] == '\\' { // "\" + enter: a new line
			m.Input.Text[c-1] = '\n'
			return nil
		}
		if m.queueAt >= 0 {
			if text := tidy(string(m.Input.Text)); text != "" {
				m.remember(text)
			}
			m.keepQueued()
			m.release()
			return nil
		}
		return m.submit()
	case "ctrl+j", "alt+enter", "shift+enter":
		m.Input.insert("\n")
	case "ctrl+v":
		return pasteClipboard
	case "up":
		if m.queueAt >= 0 || len(m.Input.Text) == 0 && !m.browsing() && len(m.pending) > 0 {
			m.walkQueue(-1)
		} else {
			m.recall(-1)
		}
	case "down":
		if m.queueAt >= 0 {
			m.walkQueue(+1)
		} else {
			m.recall(+1)
		}
	case "esc":
		switch {
		case m.ideState().Pinned: // first back to every agent at work: a claude at work goes on
			m.pinView("")
		case m.bangStop(): // a ! command first: claude goes on
		case m.canInterrupt():
			m.interrupt()
		case len(m.Input.Text) > 0 || m.queueAt >= 0:
			m.clearPrompt()
		default: // nothing left to cancel
			return m.quitTwice("esc")
		}
	case "pgup":
		m.scrollChat(10)
	case "pgdown":
		m.scrollChat(-10)
	default:
		m.Input.edit(k)
	}
	return nil
}

func (m *Model) submit() tea.Cmd {
	if command, ok := bangCommand(string(m.Input.Text)); ok { // as typed: tidy would touch its quotes
		m.Input = Input{}
		m.ChatScroll = 0
		m.remember("!" + command)
		return m.bang(command)
	}
	text := tidy(string(m.Input.Text))
	if text == "" {
		return nil
	}
	m.Input = Input{}
	return m.prompt(text)
}

// tidy cleans a prompt up as typed: no blanks around it or at the ends of its lines, no runs of
// spaces inside a line (its indentation stays), at most one blank line in a row, and a capital first
// letter when the first word is a plain lowercase one ("fix", not "calc.py" or "iOS").
func tidy(text string) string {
	var lines []string
	for l := range strings.Lines(strings.TrimSpace(text)) {
		l = strings.TrimRight(l, " \t\n")
		if l == "" && len(lines) > 0 && lines[len(lines)-1] == "" {
			continue
		}
		body := strings.TrimLeft(l, " \t")
		words := strings.FieldsFunc(body, func(r rune) bool { return r == ' ' })
		lines = append(lines, l[:len(l)-len(body)]+strings.Join(words, " "))
	}
	s := strings.Join(lines, "\n")
	word := s
	if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
		word = s[:i]
	}
	word = strings.TrimRight(word, ",.!?:;")
	r, n := utf8.DecodeRuneInString(word)
	if unicode.IsLower(r) && !strings.ContainsFunc(word[n:], func(r rune) bool { return !unicode.IsLower(r) && r != '\'' && r != '-' }) {
		s = string(unicode.ToUpper(r)) + s[n:]
	}
	return s
}

// prompt handles text sent from the prompt: a slash command claudit runs itself, one claude
// cannot run, or else a message for claude, sent at once or queued behind its turn. A command
// claude runs stays out of the chat; what it prints joins it.
func (m *Model) prompt(text string) tea.Cmd {
	m.ChatScroll = 0
	if m.inspection != nil {
		m.inspection.height = -1 // reserve room for the submitted prompt without releasing the inspected content
	}
	m.remember(text)
	cmd, isCmd := slash(text)
	if isCmd {
		if done, run := m.slashCommand(text, cmd); done {
			return run
		}
	}
	m.unknown = ""
	if m.Status.State == "exited" {
		m.local("Error: claude has exited: prompts are not sent")
		return nil
	}
	if m.Status.Busy && !m.Status.Replay { // it joins the chat once it goes to claude
		m.pending = append(m.pending, text)
		m.pendingAt = m.clock()
		return nil
	}
	if !isCmd {
		m.Chat = append(m.Chat, ChatEntry{Kind: UserEntry, Text: m.pastedTexts(text), At: m.clock()})
	} else if !m.Status.Replay {
		m.echoGoal(text)
	}
	if m.Status.Replay {
		m.local("replay: prompts are not sent")
		return nil
	}
	m.send(m.message(text))
	m.Status.State, m.Status.Busy, m.Status.Alert, m.cmdTurn = turnState(cmd, isCmd), true, false, isCmd
	m.effortTurn = isCmd && cmd == "effort"
	return nil
}

// cut marks the last prompt in the chat as one whose turn the user interrupted.
func (m *Model) cut() {
	for i := len(m.Chat) - 1; i >= 0; i-- {
		if m.Chat[i].Kind == UserEntry {
			m.Chat[i].Cut = true
			return
		}
	}
}

// chatEntries is the conversation as drawn: the chat, claude's caret while it works, then the
// prompts waiting for claude's turn: it has not seen those yet.
func (m *Model) chatEntries() []ChatEntry {
	entries := slices.Clip(m.Chat)
	if m.Status.Busy {
		entries = append(entries, ChatEntry{Kind: CaretEntry, Text: drawSpinner[m.Status.Frame%len(drawSpinner)]})
	}
	for i, p := range m.pending {
		if i != m.queueAt { // that one is in the prompt
			e := ChatEntry{Kind: UserEntry, Text: m.pastedTexts(p), Queued: true}
			if goal := goalCondition(e.Text); goal != "" {
				e.Kind, e.Text = CommandEntry, "Goal set: "+goal
			}
			entries = append(entries, e)
		}
	}
	return entries
}

// remember adds a prompt to the history, with the texts its placeholders stand for, unless it
// repeats the last one, pasted texts and all; Claude Code's history file gets it too.
func (m *Model) remember(text string) {
	e := history.Entry{Text: text, Pasted: m.pastedIn(text)}
	if n := len(m.history); n == 0 || m.history[n-1].Text != text || !maps.Equal(m.history[n-1].Pasted, e.Pasted) {
		m.history = append(m.history, e)
		if m.historyFile != "" && !m.Status.Replay {
			_ = history.Append(m.historyFile, m.historyDir, m.session, e, m.clock()) // the history is a convenience: a failed write costs a recall
		}
	}
	m.histAt = len(m.history)
}

// walkQueue walks the prompts waiting for claude's turn as ↑ and ↓ walk the history, each taken
// into the prompt to be edited in its place: d = -1 to an older one, +1 to a newer one, past the
// newest to an empty prompt. The one it leaves keeps what the prompt says.
func (m *Model) walkQueue(d int) {
	i := len(m.pending)
	if m.queueAt >= 0 {
		i = m.queueAt
		if m.keepQueued() && d > 0 {
			i-- // the newer ones moved up into its place
		}
	}
	if i = max(i+d, 0); i >= len(m.pending) {
		m.release()
		return
	}
	text := []rune(m.pending[i])
	m.queueAt, m.Input = i, Input{Text: text, Cursor: len(text)}
}

// keepQueued puts what the prompt says in place of the queued prompt it holds and empties the
// prompt; with nothing said, that one leaves the queue. It reports whether it left.
func (m *Model) keepQueued() bool {
	text := tidy(string(m.Input.Text))
	if text == "" {
		m.pending = slices.Delete(m.pending, m.queueAt, m.queueAt+1)
	} else {
		m.pending[m.queueAt] = text
	}
	m.pendingAt = m.clock()
	m.queueAt, m.Input = -1, Input{}
	return text == ""
}

// clearPrompt empties the prompt; a queued prompt taken into it to edit stays as it was.
func (m *Model) clearPrompt() {
	m.Input, m.histAt, m.queueAt = Input{}, len(m.history), -1
	m.release()
}

// release sends the queue held back while one of its prompts was being edited, if claude's turn
// ended meanwhile.
func (m *Model) release() {
	if m.queueAt < 0 && len(m.pending) > 0 && !m.Status.Busy && m.Status.State != "exited" {
		m.flush()
	}
}

// flush sends the prompts typed while claude worked as one message. A slash command goes alone,
// since claude reads only a whole message as a command; what follows it waits for the next turn.
// A prompt being edited holds itself and those after it back, so they go in their order.
func (m *Model) flush() {
	end := len(m.pending)
	if m.queueAt >= 0 {
		end = m.queueAt
	}
	if end == 0 {
		return
	}
	n := 1
	if _, ok := slash(m.pending[0]); !ok {
		for n < end {
			if _, ok := slash(m.pending[n]); ok {
				break
			}
			n++
		}
	}
	m.send(m.message(strings.Join(m.pending[:n], "\n\n")))
	first, isCmd := slash(m.pending[0])
	at := len(m.Chat)
	if m.Status.Busy && at > 0 && m.Chat[at-1].Kind == AssistantEntry {
		at-- // keep the streaming reply last, as local command output does
	}
	for _, p := range m.pending[:n] {
		if !isCmd { // a command goes alone and stays out of the chat
			m.Chat = slices.Insert(m.Chat, at, ChatEntry{Kind: UserEntry, Text: m.pastedTexts(p), At: m.clock()})
			at++
		} else {
			m.echoGoal(p)
		}
	}
	m.pending = m.pending[n:]
	if m.queueAt >= 0 {
		m.queueAt -= n
	}
	if !m.Status.Busy { // forwarding text mid-turn leaves the active turn's state alone
		m.cmdTurn, m.effortTurn = isCmd, isCmd && first == "effort"
		m.Status.State, m.Status.Busy, m.Status.Alert = turnState(first, isCmd), true, false
	}
}

// paste puts what the terminal pasted in the prompt: the pictures whose paths it is, a picture
// pasted as base64, a long text as a placeholder.
func (m *Model) paste(s string) tea.Cmd {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	switch p := m.Popup.(type) {
	case nil:
		if paths := imagePaths(s); paths != nil {
			return loadImages(paths)
		}
		if data := pastedImage(s); data != nil {
			return func() tea.Msg { return loadImageData(data) }
		}
		if isLongPaste(s) {
			s = m.attach(attachment{text: s})
		}
		m.Input.insert(s)
	case *QuestionPopup:
		if q := &p.Questions[p.Current]; q.onOther() {
			q.Other.insert(strings.ReplaceAll(s, "\n", " "))
			q.syncOther()
		}
	}
	return nil
}
