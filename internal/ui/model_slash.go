package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/protocol"
)

// localCommands are the slash commands claudit runs itself: its own, and some claude -p turns
// down that claudit can stand in for.
var localCommands = []protocol.Command{
	{Name: "btw", Description: "Ask a side question while claude works; it stays out of the conversation", Hint: "<question>"},
	{Name: "copy", Description: "Copy claude's last reply to the clipboard"},
	{Name: "exit", Description: "Quit claudit"},
	{Name: "export", Description: "Save the conversation to a Markdown file", Hint: "[file]"},
	{Name: "help", Description: "Keys and glyphs of claudit"},
	{Name: "plugins", Description: "Turn plugins on and off, install and uninstall them", Aliases: []string{"plugin"}},
	{Name: "quit", Description: "Quit claudit"},
	{Name: "resume", Description: "Resume a conversation of this project", Hint: "[id or title]"},
	{Name: "status", Description: "Version, account, model, session and MCP servers"},
}

// interactiveOnly are the commands of Claude Code that run only in its own terminal UI, most of
// them opening a panel there; claude -p turns them down (as of claude 2.1.314).
var interactiveOnly = map[string]bool{
	"add-dir": true, "allowed-tools": true, "artifacts": true, "autofix-pr": true, "background": true, "bg": true,
	"branch": true, "btw": true, "bug": true, "cd": true, "checkpoint": true, "chrome": true, "cloud-plugins": true,
	"daemon": true, "design-login": true, "desktop": true, "diff": true, "feedback": true, "fork": true, "hooks": true,
	"ide": true, "install-github-app": true, "install-slack-app": true, "keybindings": true, "login": true,
	"logout": true, "marketplace": true, "memory": true, "mobile": true, "passes": true, "permissions": true,
	"plan": true, "powerup": true, "privacy-settings": true, "radio": true,
	"rate-limit-options": true, "rc": true, "release-notes": true, "remote": true, "remote-control": true,
	"restart": true, "rewind": true, "sandbox": true, "scroll-speed": true, "session": true, "setup-bedrock": true,
	"setup-vertex": true, "share": true, "skills": true, "statusline": true, "stickers": true, "tasks": true,
	"teleport": true, "terminal-setup": true, "theme": true, "tp": true, "tui": true, "ultraplan": true,
	"undo": true, "update": true, "upgrade": true, "vim": true, "voice": true, "web-setup": true,
}

// hiddenCommands are commands claude -p lists that are not for typing: removed or renamed ones,
// and ones for sessions a server starts.
var hiddenCommands = map[string]bool{
	"agents": true, "design-consent": true, "design-revoke": true, "extra-usage": true, "heapdump": true,
	"workflow-launch-exec": true,
}

// maxSuggest is how many slash commands the completion list keeps.
const maxSuggest = 50

// addCommands merges slash commands into the completions and the known ones: new names join, known
// ones keep the description they have unless list brings one. Hidden ones are only known.
func (m *Model) addCommands(list []protocol.Command) {
	for _, c := range list {
		m.known[c.Name] = true
		for _, a := range c.Aliases {
			m.known[a] = true
		}
	}
	for _, c := range slices.Concat(list, localCommands) {
		if c.Name == "" || hiddenCommands[c.Name] || strings.HasPrefix(c.Name, "__") {
			continue
		}
		i, found := slices.BinarySearchFunc(m.commands, c.Name, func(a protocol.Command, n string) int { return strings.Compare(a.Name, n) })
		switch {
		case !found:
			m.commands = slices.Insert(m.commands, i, c)
		case c.Description != "":
			m.commands[i] = c
		}
	}
}

// slashCommand runs the command cmd of text when claudit has it run otherwise than by sending it
// to claude, and reports whether it did.
func (m *Model) slashCommand(text, cmd string) (done bool, run tea.Cmd) {
	args := strings.TrimSpace(strings.TrimPrefix(text, "/"+cmd))
	switch {
	case cmd == "exit" || cmd == "quit":
		return true, tea.Quit
	case cmd == "help":
		m.ShowHelp = true
	case cmd == "resume":
		return true, m.resume(args)
	case cmd == "status":
		m.local(m.statusReport())
	case cmd == "export":
		return true, m.export(args)
	case cmd == "copy":
		return true, m.copyReply()
	case cmd == "plugins" || cmd == "plugin":
		return true, m.openPlugins()
	case cmd == "btw":
		m.btw(m.pastedTexts(args))
	case args == "" && m.openPicker(cmd):
	case cmd == "model" && args != "" && !m.Status.Replay && m.Status.State != "exited":
		m.setModel(args)
	case cmd == "effort" && args != "" && !m.Status.Replay && m.Status.State != "exited":
		m.setEffort(args)
		return false, nil // claude runs the command
	case interactiveOnly[cmd] && !m.known[cmd]:
		m.local("/" + cmd + " runs only in the interactive claude: claude -p turns it down")
	case len(m.known) > 0 && !m.known[cmd] && !strings.Contains(cmd, "/") && m.unknown != text:
		// claude -p would hand an unknown command to the model as a prompt: a paid turn for a typo.
		// A path such as "/tmp/x what is this" is a prompt, not a command.
		m.unknown = text
		m.Input = Input{Text: []rune(text), Cursor: len([]rune(text))}
		hint := ""
		if near := m.nearest(cmd); near != "" {
			hint = " · /" + near + "?"
		}
		m.local("Error: unknown command /" + cmd + hint + " · enter again to send it anyway")
	default:
		return false, nil
	}
	return true, nil
}

// efforts are the levels /effort takes, and "auto", the model's own.
var efforts = []string{"low", "medium", "high", "xhigh", "max"}

// setEffort shows the effort /effort level sets: claude -p takes the command but tells the level
// to nobody. "auto" shows none until its turn ends and claude's settings tell the level; a level
// it will refuse changes nothing.
func (m *Model) setEffort(level string) {
	switch level = strings.ToLower(level); {
	case level == "auto":
		m.Status.Effort = ""
	case slices.Contains(efforts, level):
		m.Status.Effort = level
	}
}

// setModel switches claude to model at once. claude -p would run "/model …" sent as a prompt too,
// but it names the model only with the turn after, so the status would keep the old one.
func (m *Model) setModel(model string) {
	m.modelReqs++
	m.send(protocol.SetModel{RequestID: fmt.Sprintf("model-%d", m.modelReqs), Model: model})
}

// nearest is the slash command spelled most like cmd, a typo or two away; "" for none.
func (m *Model) nearest(cmd string) string {
	best, bestD := "", max(1, len([]rune(cmd))/3)+1
	for _, c := range m.commands {
		if d := editDistance(cmd, c.Name); d < bestD {
			best, bestD = c.Name, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance of a and b, by runes.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := range ra {
		cur := make([]int, len(rb)+1)
		cur[0] = i + 1
		for j := range rb {
			cost := 1
			if ra[i] == rb[j] {
				cost = 0
			}
			cur[j+1] = min(prev[j+1]+1, cur[j]+1, prev[j]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// suggest lists the slash commands that complete the prompt: those that start with what follows
// "/", shortest first (/mo: /model before /mobile-native), then those that contain it, then those
// whose description does (/cost: /usage). Nothing once the prompt is not a bare command.
func (m *Model) suggest() []protocol.Command {
	text := string(m.Input.Text)
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \n") {
		return nil
	}
	q := strings.ToLower(text[1:])
	var head, rest, about []protocol.Command
	for _, c := range m.commands {
		switch lc := strings.ToLower(c.Name); {
		case strings.HasPrefix(lc, q):
			head = append(head, c)
		case strings.Contains(lc, q):
			rest = append(rest, c)
		case len(q) >= 3 && strings.Contains(strings.ToLower(c.Description), q):
			about = append(about, c)
		}
	}
	if q != "" { // a bare "/" lists them all, by name
		slices.SortStableFunc(head, func(a, b protocol.Command) int { return len(a.Name) - len(b.Name) })
	}
	all := slices.Concat(head, rest, about)
	return all[:min(len(all), maxSuggest)]
}

// syncSuggest refreshes the completion list; the highlight goes back to the top when the query changes.
func (m *Model) syncSuggest() {
	q := string(m.Input.Text)
	if q != m.suggestFor {
		m.suggestFor, m.SuggestSel = q, 0
	}
	m.Suggest = nil
	if m.Popup == nil && !m.PanelFocus && !m.ShowHelp {
		m.Suggest = m.suggest()
	}
	m.SuggestSel = min(max(m.SuggestSel, 0), max(len(m.Suggest)-1, 0))
}

// suggestKey handles a key while the completion list is open and reports whether it was one of its keys.
func (m *Model) suggestKey(key string) bool {
	n := len(m.Suggest)
	if n == 0 {
		return false
	}
	switch key {
	case "up":
		m.SuggestSel = (m.SuggestSel + n - 1) % n
	case "down":
		m.SuggestSel = (m.SuggestSel + 1) % n
	case "tab":
		m.complete(" ")
	case "enter":
		m.complete("")
		return false // and send it
	default:
		return false
	}
	return true
}

// complete puts the highlighted command in the prompt, followed by after.
func (m *Model) complete(after string) {
	text := []rune("/" + m.Suggest[m.SuggestSel].Name + after)
	m.Input = Input{Text: text, Cursor: len(text)}
}

// slash is the command of a prompt such as "/model haiku": "model"; ok is false for other prompts.
func slash(text string) (cmd string, ok bool) {
	rest, ok := strings.CutPrefix(text, "/")
	if !ok || rest == "" || strings.HasPrefix(rest, "/") {
		return "", false
	}
	cmd, _, _ = strings.Cut(rest, " ")
	return cmd, true
}

// goalCondition is a goal-setting command's text, excluding status and clear commands.
func goalCondition(text string) string {
	cmd, ok := slash(text)
	if !ok || cmd != "goal" {
		return ""
	}
	condition := strings.TrimSpace(strings.TrimPrefix(text, "/goal"))
	switch strings.ToLower(condition) {
	case "", "clear", "stop", "off", "reset", "none", "cancel":
		return ""
	}
	return tidy(condition)
}

func (m *Model) echoGoal(text string) {
	if goal := goalCondition(m.pastedTexts(text)); goal != "" {
		m.Chat = append(m.Chat, ChatEntry{Kind: CommandEntry, Text: "Goal set: " + goal, At: m.clock(), GoalEcho: true})
	}
}

// confirmGoal replaces the local echo while preserving the time the user sent it.
func (m *Model) confirmGoal(text string) bool {
	if _, ok := chatGoal(ChatEntry{Kind: CommandEntry, Text: text}); !ok {
		return false
	}
	for i := len(m.Chat) - 1; i >= 0; i-- {
		if m.Chat[i].GoalEcho {
			m.Chat[i].Text, m.Chat[i].GoalEcho = text, false
			return true
		}
	}
	return false
}
