// Package sessions reads the conversations claude keeps on disk, for /resume: which there are in
// a project, and what was said in one.
package sessions

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Info is one saved conversation.
type Info struct {
	ID       string
	Title    string // the name it was given, else the title claude made up, else its last prompt
	Name     string // the name it was given (/rename); "" for none
	Modified time.Time
}

// Dir is where claude keeps the conversations held in cwd: <config dir>/projects/<the real path
// of cwd with every character but ASCII letters and digits turned into "-">.
func Dir(cwd string) string {
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		home, _ := os.UserHomeDir()
		config = filepath.Join(home, ".claude")
	}
	slug := strings.Map(func(r rune) rune {
		if 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' {
			return r
		}
		return '-'
	}, cwd)
	return filepath.Join(config, "projects", slug)
}

// List returns the conversations in dir, the latest first. One without a title or a prompt has
// nothing to resume and is left out; a dir that does not exist holds none.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Info
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if name, t := title(data); t != "" {
			list = append(list, Info{ID: id, Title: t, Name: name, Modified: fi.ModTime()})
		}
	}
	slices.SortFunc(list, func(a, b Info) int { return cmp.Or(b.Modified.Compare(a.Modified), strings.Compare(a.ID, b.ID)) })
	return list, nil
}

// titled are the starts of the records that can name a conversation. Only those are decoded: a
// conversation can take many megabytes.
var titled = [][]byte{[]byte(`{"type":"custom-title"`), []byte(`{"type":"ai-title"`), []byte(`{"type":"last-prompt"`)}

// title picks the latest name of each kind out of a conversation's records: the one it was
// given, and the one to list it by.
func title(data []byte) (name, t string) {
	var custom, ai, prompt string
	for line := range bytes.Lines(data) {
		if !slices.ContainsFunc(titled, func(p []byte) bool { return bytes.HasPrefix(line, p) }) {
			continue
		}
		var r struct {
			Custom string `json:"customTitle"`
			AI     string `json:"aiTitle"`
			Prompt string `json:"lastPrompt"`
		}
		if json.Unmarshal(line, &r) == nil {
			custom, ai, prompt = cmp.Or(r.Custom, custom), cmp.Or(r.AI, ai), cmp.Or(r.Prompt, prompt)
		}
	}
	return strings.TrimSpace(custom), strings.TrimSpace(cmp.Or(custom, ai, prompt))
}

// Role is who said a Message.
type Role int

const (
	Prompt Role = iota // the user
	Reply              // the model
	Output             // the CLI itself: a slash command's output, an API error
	Cut                // no text: the user interrupted the turn of the last prompt
)

// Message is a prompt or a reply of a saved conversation, as the chat shows it.
type Message struct {
	Role Role
	Text string
	At   time.Time // when it was said; zero when the record does not say
}

// History reads what was said in conversation id of dir, oldest first: the prompts and the main
// agent's replies, what its Bash calls say they do among them, without the calls themselves and
// without what subagents said.
func History(dir, id string) ([]Message, error) {
	data, err := os.ReadFile(filepath.Join(dir, id+".jsonl"))
	if err != nil {
		return nil, err
	}
	var out []Message
	for line := range bytes.Lines(data) {
		var r struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			Sidechain bool   `json:"isSidechain"`
			Meta      bool   `json:"isMeta"`
			Summary   bool   `json:"isCompactSummary"`
			At        string `json:"timestamp"`
			Content   string `json:"content"` // of a system record
			Message   struct {
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &r) != nil || r.Sidechain || r.Meta || r.Summary {
			continue
		}
		n := len(out)
		switch r.Type {
		case "user":
			for _, t := range texts(r.Message.Content) {
				out = addUser(out, t)
			}
		case "assistant":
			role := Reply
			if r.Message.Model == "<synthetic>" {
				role = Output
			}
			for _, t := range texts(r.Message.Content) {
				if t = strings.TrimSpace(t); t != "" {
					out = append(out, Message{Role: role, Text: t})
				}
			}
		case "system":
			if r.Subtype == "local_command" { // a slash command as typed, or its output
				out = addUser(out, r.Content)
			}
		}
		at, _ := time.Parse(time.RFC3339Nano, r.At) // a time it does not read is no time
		for i := n; i < len(out); i++ {
			out[i].At = at
		}
	}
	return out, nil
}

// texts are the text blocks of message content, a plain string or a list of blocks, and what a
// Bash call among them says it does.
func texts(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var blocks []struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Name  string `json:"name"`
		Input struct {
			Description string `json:"description"`
		} `json:"input"`
	}
	_ = json.Unmarshal(raw, &blocks) // anything else holds no text
	var out []string
	for _, b := range blocks {
		switch {
		case b.Type == "text":
			out = append(out, b.Text)
		case b.Type == "tool_use" && b.Name == "Bash":
			out = append(out, b.Input.Description) // none said: blank, it goes
		}
	}
	return out
}

// noise are the tags the CLI wraps around what it puts in the user's turn by itself.
var noise = []string{"<task-notification>", "<system-reminder>", "<local-command-caveat>", "<ide_opened_file>", "<ide_selection>"}

// addUser adds what a record on the user's side says: a prompt, a slash command as typed, or the
// command's output.
func addUser(out []Message, text string) []Message {
	text = strings.TrimSpace(text)
	switch {
	case text == "" || slices.ContainsFunc(noise, func(p string) bool { return strings.HasPrefix(text, p) }):
	case strings.HasPrefix(text, "<command-name>") || strings.HasPrefix(text, "<command-message>"):
		cmd := strings.TrimSpace(tag(text, "command-name") + " " + tag(text, "command-args"))
		out = append(out, Message{Role: Prompt, Text: cmd})
	case strings.HasPrefix(text, "<local-command-"):
		if s := strings.TrimSpace(tag(text, "local-command-stdout") + tag(text, "local-command-stderr")); s != "" {
			out = append(out, Message{Role: Output, Text: s})
		}
	case strings.HasPrefix(text, "[Request interrupted by user"):
		out = append(out, Message{Role: Cut})
	default:
		out = append(out, Message{Role: Prompt, Text: text})
	}
	return out
}

// tag is what s holds between <name> and </name>; "" without them.
func tag(s, name string) string {
	_, rest, ok := strings.Cut(s, "<"+name+">")
	if !ok {
		return ""
	}
	inner, _, _ := strings.Cut(rest, "</"+name+">")
	return strings.TrimSpace(inner)
}
