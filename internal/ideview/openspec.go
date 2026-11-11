package ideview

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// spec is an OpenSpec spec: the requirements of a capability, each with its scenarios, in the
// sections of the file. A change's spec is a delta: its sections say what it adds, modifies,
// removes or renames.
type spec struct {
	capability string // the path between "specs/" and "/spec.md": "auth/login"
	delta      bool   // a change's spec, not a main one
	archived   bool   // a delta of a change archived: the main spec holds it already
	lead       []block
	sections   []*section
}

// section is what a "## " heading heads.
type section struct {
	title string // as written: "ADDED Requirements", "Purpose"
	op    string // ADDED, MODIFIED, REMOVED or RENAMED; "" for a section of a main spec
	text  []block
	reqs  []*requirement
}

type requirement struct {
	name      string // without the "Requirement:" keyword
	key       string // what its fold is kept by: its section's title and its name
	body      []block
	scenarios []*scenario
	diff      *reqDiff // a MODIFIED one against the main spec's of its name; nil for none
}

type scenario struct {
	name  string // without the "Scenario:" keyword
	key   string // what its fold is kept by: its requirement's key and its name
	steps []block
}

// blockKind is what a block of text is.
type blockKind int

const (
	blockText    blockKind = iota // a paragraph or a list item, its lines joined
	blockStep                     // a scenario's step: a keyword, then the text
	blockQuote                    // a "> " quote, its lines joined
	blockHeading                  // a heading the spec does not structure
	blockCode                     // a line of a fenced block, a table or a rule, as written
)

type block struct {
	kind    blockKind
	keyword string // a step's: WHEN, THEN, AND, GIVEN, BUT or OR
	text    string
	apart   bool // a blank line sets it apart from the block before it
}

var (
	specHeadRe  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	specStepRe  = regexp.MustCompile(`^\s*[-*]\s+\*\*(WHEN|THEN|AND|GIVEN|BUT|OR)\*\*:?\s*(.*)$`)
	specItemRe  = regexp.MustCompile(`^\s*([-*+]|\d{1,3}[.)])\s`)
	specLabelRe = regexp.MustCompile(`^\*\*[^*]+\*\*:`) // "**Reason**: …" starts a line of its own
	specRuleRe  = regexp.MustCompile(`^(-[ \t]*){3,}$|^(\*[ \t]*){3,}$|^(_[ \t]*){3,}$`)
)

// specPath is the capability of the OpenSpec spec at path, and whether it is a change's delta;
// ok is false for any other file.
func specPath(path string) (capability string, delta, ok bool) {
	p, ok := strings.CutSuffix("/"+filepath.ToSlash(path), "/spec.md")
	i := strings.LastIndex(p, "/openspec/")
	if !ok || i < 0 {
		return "", false, false
	}
	p = p[i+len("/openspec/"):]
	if rest, ok := strings.CutPrefix(p, "specs/"); ok {
		return rest, false, rest != ""
	}
	if !strings.HasPrefix(p, "changes/") {
		return "", false, false
	}
	j := strings.Index(p, "/specs/")
	if j <= len("changes") {
		return "", false, false
	}
	capability = p[j+len("/specs/"):]
	return capability, true, capability != ""
}

// parseSpec reads the lines of a spec, a change's delta or not. Nothing is dropped: a line the
// format does not know stays as text where it is; a fenced block, a table and a rule stay as
// written, line by line.
func parseSpec(lines []string, delta bool) *spec {
	sp := &spec{delta: delta}
	var sec *section
	var req *requirement
	var sc *scenario
	var last *block // the block a line that goes on goes on; nil after a blank line
	blank := false  // a blank line since the last block
	add := func(b block) {
		b.apart, blank = blank, false
		to := &sp.lead
		switch {
		case sc != nil:
			to = &sc.steps
		case req != nil:
			to = &req.body
		case sec != nil:
			to = &sec.text
		}
		*to = append(*to, b)
		last = &(*to)[len(*to)-1]
	}
	seen := map[string]int{}
	key := func(k string) string { // a name repeated in its section or requirement gets its count
		if seen[k]++; seen[k] > 1 { // after a NUL, which no name has
			return k + "\x00#" + strconv.Itoa(seen[k])
		}
		return k
	}
	fence := "" // the marker of the fenced block the lines are in; "" for none
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		trimmed := strings.TrimSpace(line)
		indented := strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") // neither a heading nor a fence
		if m := fenceOf(trimmed); fence != "" || m != "" && !indented || strings.HasPrefix(trimmed, "|") || specRuleRe.MatchString(trimmed) {
			switch {
			case fence == "" && m != "" && !indented:
				fence = m
			case fence != "" && m != "" && !indented && trimmed == m && m[0] == fence[0] && len(m) >= len(fence):
				fence = ""
			}
			add(block{kind: blockCode, text: line})
			last = nil
			continue
		}
		if m := specHeadRe.FindStringSubmatch(trimmed); m != nil && !indented {
			last = nil
			title := strings.TrimSpace(m[2])
			reqName, isReq := strings.CutPrefix(title, "Requirement:")
			scName, isScen := strings.CutPrefix(title, "Scenario:")
			switch {
			case len(m[1]) == 2:
				blank = false
				sec = &section{title: title}
				if delta {
					sec.op = deltaOp(title)
				}
				sp.sections = append(sp.sections, sec)
				req, sc = nil, nil
			case len(m[1]) == 3 && isReq:
				if sec == nil { // requirements before any section: a section without a heading
					sec = &section{}
					sp.sections = append(sp.sections, sec)
				}
				blank = false
				name := strings.TrimSpace(reqName)
				req, sc = &requirement{name: name, key: key(sec.title + "\n" + name)}, nil
				sec.reqs = append(sec.reqs, req)
			case len(m[1]) == 4 && isScen && req != nil:
				blank = false
				name := strings.TrimSpace(scName)
				sc = &scenario{name: name, key: key(req.key + "\n" + name)}
				req.scenarios = append(req.scenarios, sc)
			default:
				add(block{kind: blockHeading, text: title})
				last = nil
			}
			continue
		}
		m := specStepRe.FindStringSubmatch(line)
		quote, isQuote := strings.CutPrefix(trimmed, ">")
		quote = strings.TrimSpace(quote)
		switch {
		case trimmed == "":
			last, blank = nil, true
		case m != nil:
			add(block{kind: blockStep, keyword: m[1], text: m[2]})
		case isQuote && quote == "": // a blank line of a quote ends its paragraph
			last = nil
		case isQuote && last != nil && last.kind == blockQuote:
			last.text += " " + quote
		case isQuote:
			add(block{kind: blockQuote, text: quote})
		case specItemRe.MatchString(line) || specLabelRe.MatchString(trimmed):
			add(block{kind: blockText, text: trimmed})
		case last != nil:
			last.text += " " + trimmed
		default:
			add(block{kind: blockText, text: trimmed})
		}
	}
	return sp
}

// fenceOf is the marker of a fence that s starts with: three or more backticks or tildes; ""
// for none.
func fenceOf(s string) string {
	for _, c := range []string{"`", "~"} {
		if n := len(s) - len(strings.TrimLeft(s, c)); n >= 3 {
			return s[:n]
		}
	}
	return ""
}

// deltaOp is what a section of a delta titled title does: ADDED, MODIFIED, REMOVED or RENAMED;
// "" for another title.
func deltaOp(title string) string {
	op, _, _ := strings.Cut(title, " ")
	switch op = strings.ToUpper(op); op {
	case "ADDED", "MODIFIED", "REMOVED", "RENAMED":
		return op
	}
	return ""
}

// counts is how many requirements and scenarios sp holds.
func (sp *spec) counts() (reqs, scenarios int) {
	for _, s := range sp.sections {
		reqs += len(s.reqs)
		for _, r := range s.reqs {
			scenarios += len(r.scenarios)
		}
	}
	return reqs, scenarios
}

// stepCount is how many steps sc has.
func (sc *scenario) stepCount() int {
	n := 0
	for _, b := range sc.steps {
		if b.kind == blockStep {
			n++
		}
	}
	return n
}
