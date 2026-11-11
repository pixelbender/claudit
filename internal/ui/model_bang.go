package ui

import (
	"cmp"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/protocol"
)

// A prompt that starts with "!" is a shell command, as in Claude Code's bash mode: claudit runs it
// itself in the project, since claude -p would take it for words to the model. The chat shows
// what it printed; claude gets the command and its output in the tags Claude Code uses, as a
// message of its own that starts a turn, or, while claude works, once its turn ends.

// bangTimeout is how long a ! command may run before it is stopped.
const bangTimeout = 10 * time.Minute

// bangLimit is how many bytes of each of stdout and stderr go to the chat and to claude, bangKept
// how many claudit keeps of each as printed: room for the colours, which neither shows.
const (
	bangLimit = 30000
	bangKept  = 4 * bangLimit
)

// bangRun is the ! command running.
type bangRun struct {
	id      int
	command string // as typed, as the chat shows it
	script  string // what runs: the command with its pasted texts in place of their placeholders
	stop    context.CancelFunc
	stopped bool // esc or ctrl+c stopped it
}

// bangDoneMsg is what a ! command left once it ended.
type bangDoneMsg struct {
	id             int
	stdout, stderr string // each cut to bangLimit bytes
	code           int    // exit code; -1 when it did not exit by itself
	err            error  // it could not start, or it ran out of time
}

// bangInput reports whether the prompt is in the shell mode: a "!" first.
func bangInput(text []rune) bool { return len(text) > 0 && text[0] == '!' }

// bangCommand reports the command of a prompt typed as "!command".
func bangCommand(text string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), "!")
	if rest = strings.TrimSpace(rest); !ok || rest == "" {
		return "", false
	}
	return rest, true
}

// bang runs a ! command off the UI's way: one at a time, not in a replay.
func (m *Model) bang(command string) tea.Cmd {
	switch {
	case m.Status.Replay:
		m.local("replay: commands are not run")
		return nil
	case m.bangRun != nil:
		m.local("! " + m.bangRun.command + " is still running: esc stops it")
		return nil
	}
	m.bangs++
	ctx, stop := context.WithTimeout(context.Background(), bangTimeout)
	script := m.pastedTexts(command)
	m.bangRun = &bangRun{id: m.bangs, command: command, script: script, stop: stop}
	at := len(m.Chat)
	if m.Status.Busy && at > 0 && m.Chat[at-1].Kind == AssistantEntry {
		at-- // keep the streaming reply last, as local output does
	}
	m.Chat = slices.Insert(m.Chat, at, ChatEntry{Kind: CommandEntry, Bang: command, At: m.clock()})
	id, dir := m.bangs, m.Sim.State().Root
	return func() tea.Msg {
		defer stop()
		return runBang(ctx, id, script, dir)
	}
}

// runBang runs command in dir through the user's shell, without a terminal or input.
func runBang(ctx context.Context, id int, command, dir string) bangDoneMsg {
	shell, flag := bangShell()
	cmd := exec.CommandContext(ctx, shell, flag, command)
	cmd.Dir = dir
	cmd.Env = bangEnv(os.Environ())
	var stdout, stderr bangOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	bangProcess(cmd)
	err := cmd.Run()
	done := bangDoneMsg{id: id, stdout: stdout.String(), stderr: stderr.String(), code: -1}
	if cmd.ProcessState != nil {
		done.code = cmd.ProcessState.ExitCode()
	}
	var exit *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		done.err = errors.New("stopped after " + bangTimeout.String())
	case err != nil && !errors.As(err, &exit) && ctx.Err() == nil:
		done.err = err
	}
	return done
}

// bangEnv is the environment of a ! command: claudit's own, without the debug dump of the API
// client, which would land in the output.
func bangEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "ANTHROPIC_LOG=") {
			out = append(out, kv)
		}
	}
	return out
}

// bangDone puts what a ! command printed in the chat and sends it to claude: at once when claude
// is free, else once its turn ends. A command the user stopped waits for the next prompt.
func (m *Model) bangDone(msg bangDoneMsg) {
	r := m.bangRun
	if r == nil || r.id != msg.id {
		return
	}
	m.bangRun = nil
	stdout, stderr := msg.stdout, msg.stderr
	var note string
	switch {
	case msg.err != nil:
		note = msg.err.Error()
	case r.stopped:
		note = "stopped"
	case msg.code > 0:
		note = "exit code " + strconv.Itoa(msg.code)
	}
	shown := strings.TrimRight(stdout, "\n")
	if e := strings.TrimRight(stderr, "\n"); e != "" {
		shown = strings.TrimLeft(shown+"\n"+e, "\n")
	}
	if note != "" {
		shown = strings.TrimLeft(shown+"\n["+note+"]", "\n")
	}
	for i := len(m.Chat) - 1; i >= 0; i-- {
		if e := &m.Chat[i]; e.Kind == CommandEntry && e.Bang == r.command && e.Text == "" {
			e.Text = cmp.Or(shown, "(no output)")
			break
		}
	}
	if note != "" {
		stderr = strings.TrimLeft(strings.TrimRight(stderr, "\n")+"\n["+note+"]", "\n")
	}
	m.bangSaid = append(m.bangSaid, "<bash-input>"+r.script+"</bash-input>\n<bash-stdout>"+
		strings.TrimRight(stdout, "\n")+"</bash-stdout><bash-stderr>"+strings.TrimRight(stderr, "\n")+"</bash-stderr>")
	if !r.stopped && !m.Status.Busy && len(m.pending) == 0 {
		m.sendBangs()
	}
}

// sendBangs sends the ! commands run since the last prompt as a message of their own, which
// starts a turn. It has no prompt in the chat: like a slash command's, there is none to cut.
func (m *Model) sendBangs() {
	if len(m.bangSaid) == 0 || m.Status.Replay || m.Status.State == "exited" {
		return
	}
	m.send(protocol.UserMessage{Text: strings.Join(m.bangSaid, "\n")})
	m.bangSaid = nil
	m.todoTurn()
	m.Status.State, m.Status.Busy, m.Status.Alert, m.cmdTurn, m.effortTurn = "thinking", true, false, true, false
}

// bangStop stops the ! command running, if any, and reports whether there was one.
func (m *Model) bangStop() bool {
	if m.bangRun == nil {
		return false
	}
	m.bangRun.stopped = true
	m.bangRun.stop()
	return true
}

// withBangs is text with the ! commands run since the last prompt in front, for claude to see
// them as Claude Code shows them. A slash command goes as it is: claude reads only a whole
// message as one; the commands wait for the next prompt.
func (m *Model) withBangs(text string) string {
	if len(m.bangSaid) == 0 {
		return text
	}
	if _, ok := slash(text); ok {
		return text
	}
	said := strings.Join(m.bangSaid, "\n")
	m.bangSaid = nil
	return said + "\n\n" + text
}

// bangOutput is a stream of a ! command: it keeps the first bangKept bytes and counts the rest,
// still taking all of it, for a command such as yes prints without end until it is stopped.
type bangOutput struct {
	kept []byte
	size int
}

func (o *bangOutput) Write(p []byte) (int, error) {
	if room := bangKept - len(o.kept); room > 0 {
		o.kept = append(o.kept, p[:min(room, len(p))]...)
	}
	o.size += len(p)
	return len(p), nil
}

// String is what the stream printed without its colours, cut to bangLimit bytes on a rune
// boundary, saying how much is left out.
func (o *bangOutput) String() string {
	s := ansi.Strip(string(o.kept))
	cut, left := len(s), o.size-len(o.kept)
	if cut > bangLimit {
		cut = bangLimit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
	}
	if left += len(s) - cut; left == 0 {
		return s
	}
	return s[:cut] + "\n… " + strconv.Itoa(left) + " more bytes"
}
