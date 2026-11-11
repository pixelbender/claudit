package ideview

import (
	"reflect"
	"strings"
	"testing"
)

func TestSpecPath(t *testing.T) {
	for _, tt := range []struct {
		path, capability string
		delta, ok        bool
	}{
		{"/p/openspec/specs/auth/login/spec.md", "auth/login", false, true},
		{"/p/openspec/specs/auth/spec.md", "auth", false, true},
		{"/p/openspec/changes/add-login/specs/auth/login/spec.md", "auth/login", true, true},
		{"/p/openspec/changes/archive/2026-01-01-add-login/specs/auth/spec.md", "auth", true, true},
		{"openspec/specs/auth/spec.md", "auth", false, true}, // relative
		{"/p/openspec/specs/spec.md", "", false, false},      // no capability
		{"/p/openspec/changes/specs/auth/spec.md", "", false, false},
		{"/p/openspec/changes/add-login/proposal.md", "", false, false},
		{"/p/openspec/changes/add-login/specs/auth/notes.md", "", false, false},
		{"/p/docs/specs/auth/spec.md", "", false, false},
		{"/p/openspec/project.md", "", false, false},
	} {
		capability, delta, ok := specPath(tt.path)
		if capability != tt.capability || delta != tt.delta || ok != tt.ok {
			t.Errorf("%s: %q %v %v, want %q %v %v", tt.path, capability, delta, ok, tt.capability, tt.delta, tt.ok)
		}
	}
}

// specText is a delta with every kind of section: two requirements added (scenarios, a
// continued step, a note, a list item that is not a step), one modified, one removed with its
// reason, and a rename.
const specText = `## ADDED Requirements

### Requirement: Login
The system SHALL let a user log in with ` + "`email`" + ` and **password**.
It MUST NOT keep the password.

#### Scenario: Good password
- **WHEN** a user logs in
  with the right password
- **THEN** the user is in

> **NOTE:** checked by ` + "`TestLogin`" + `.

#### Scenario: Bad password
- **WHEN** a user logs in with a wrong one
- **THEN** the user is not
- **NOTE** an unknown keyword stays text

### Requirement: Logout
The system SHALL let a user log out.

#### Scenario: Logout
- **WHEN** the user logs out
- **THEN** the session ends

## MODIFIED Requirements

### Requirement: Sessions
A session SHALL last a day.

## REMOVED Requirements

### Requirement: Remember me
**Reason**: nobody used it
**Migration**: none

## RENAMED Requirements

- FROM: ` + "`### Requirement: Sign in`" + `
- TO: ` + "`### Requirement: Login`"

// specOf parses text as the editor holds it.
func specOf(text string) *spec { return parseSpec(strings.Split(text, "\n"), true) }

// shape is what parseSpec found, flat: a line per section, requirement, scenario and block.
func shape(sp *spec) []string {
	var out []string
	blocks := func(indent string, bs []block) {
		for _, b := range bs {
			out = append(out, indent+[]string{"text", "step", "quote", "heading", "code"}[b.kind]+" "+b.keyword+"|"+b.text)
		}
	}
	blocks("", sp.lead)
	for _, s := range sp.sections {
		out = append(out, "## "+s.op+"|"+s.title)
		blocks("  ", s.text)
		for _, r := range s.reqs {
			out = append(out, "  ### "+r.name)
			blocks("    ", r.body)
			for _, sc := range r.scenarios {
				out = append(out, "    #### "+sc.name)
				blocks("      ", sc.steps)
			}
		}
	}
	return out
}

func TestParseSpec(t *testing.T) {
	want := []string{
		"## ADDED|ADDED Requirements",
		"  ### Login",
		"    text |The system SHALL let a user log in with `email` and **password**. It MUST NOT keep the password.",
		"    #### Good password",
		"      step WHEN|a user logs in with the right password",
		"      step THEN|the user is in",
		"      quote |**NOTE:** checked by `TestLogin`.",
		"    #### Bad password",
		"      step WHEN|a user logs in with a wrong one",
		"      step THEN|the user is not",
		"      text |- **NOTE** an unknown keyword stays text",
		"  ### Logout",
		"    text |The system SHALL let a user log out.",
		"    #### Logout",
		"      step WHEN|the user logs out",
		"      step THEN|the session ends",
		"## MODIFIED|MODIFIED Requirements",
		"  ### Sessions",
		"    text |A session SHALL last a day.",
		"## REMOVED|REMOVED Requirements",
		"  ### Remember me",
		"    text |**Reason**: nobody used it",
		"    text |**Migration**: none",
		"## RENAMED|RENAMED Requirements",
		"  text |- FROM: `### Requirement: Sign in`",
		"  text |- TO: `### Requirement: Login`",
	}
	sp := specOf(specText)
	if got := shape(sp); !reflect.DeepEqual(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if reqs, scenarios := sp.counts(); reqs != 4 || scenarios != 3 {
		t.Errorf("counts %d %d", reqs, scenarios)
	}
	if n := sp.sections[0].reqs[0].scenarios[1].stepCount(); n != 2 {
		t.Errorf("steps of Bad password: %d", n)
	}
	// Line endings do not matter.
	if crlf := parseSpec(strings.Split(strings.ReplaceAll(specText, "\n", "\r\n"), "\n"), true); !reflect.DeepEqual(shape(crlf), want) {
		t.Errorf("CRLF:\n%s", strings.Join(shape(crlf), "\n"))
	}
}

// A main spec's sections do nothing to its requirements, whatever their titles.
func TestParseSpecMain(t *testing.T) {
	sp := parseSpec(strings.Split("## Removed features\n### Requirement: A\n## ADDED Requirements", "\n"), false)
	if sp.delta || sp.sections[0].op != "" || sp.sections[1].op != "" {
		t.Errorf("ops %q %q", sp.sections[0].op, sp.sections[1].op)
	}
}

// Requirements and scenarios are kept by name; a name repeated gets its count, so that each folds
// alone.
func TestParseSpecKeys(t *testing.T) {
	sp := specOf("## ADDED Requirements\n### Requirement: A\n#### Scenario: S\n#### Scenario: S\n### Requirement: A\n#### Scenario: S\n## MODIFIED Requirements\n### Requirement: A")
	a, b, c := sp.sections[0].reqs[0], sp.sections[0].reqs[1], sp.sections[1].reqs[0]
	keys := []string{a.key, b.key, c.key, a.scenarios[0].key, a.scenarios[1].key, b.scenarios[0].key}
	want := []string{"ADDED Requirements\nA", "ADDED Requirements\nA\x00#2", "MODIFIED Requirements\nA",
		"ADDED Requirements\nA\nS", "ADDED Requirements\nA\nS\x00#2", "ADDED Requirements\nA\x00#2\nS"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys %q", keys)
	}
	// a scenario named as a count folds apart from the requirement repeated
	sp = specOf("## ADDED Requirements\n### Requirement: B\n#### Scenario: #2\n### Requirement: B")
	if r := sp.sections[0].reqs; r[0].scenarios[0].key == r[1].key {
		t.Errorf("scenario #2 and the second B share the key %q", r[1].key)
	}
}

// A main spec, and what the format does not structure: kept as text where it is, nothing dropped.
func TestParseSpecOther(t *testing.T) {
	for _, tt := range []struct {
		name, text string
		want       []string
	}{
		{"empty", "", nil},
		{"no requirements", "# Notes\n\nJust text.", []string{"heading |Notes", "text |Just text."}},
		{"main spec", "# auth Specification\n\n## Purpose\nLogs users in.\n\n## Requirements\n\n### Requirement: Login\nIt SHALL.", []string{
			"heading |auth Specification",
			"## |Purpose",
			"  text |Logs users in.",
			"## |Requirements",
			"  ### Login",
			"    text |It SHALL.",
		}},
		{"requirement without a section", "### Requirement: Login\nIt SHALL.", []string{"## |", "  ### Login", "    text |It SHALL."}},
		{"scenario without a requirement", "## ADDED Requirements\n#### Scenario: Lost\n- **WHEN** x", []string{
			"## ADDED|ADDED Requirements", "  heading |Scenario: Lost", "  step WHEN|x",
		}},
		{"no scenarios", "## ADDED Requirements\n### Requirement:   Bare  \n\n\nText.\n\n- one\n- two\n  more", []string{
			"## ADDED|ADDED Requirements", "  ### Bare", "    text |Text.", "    text |- one", "    text |- two more",
		}},
		{"fence", "## ADDED Requirements\n### Requirement: Code\n```go\nx := 1\n\n# not a heading\n```\nAfter.", []string{
			"## ADDED|ADDED Requirements", "  ### Code",
			"    code |```go", "    code |x := 1", "    code |", "    code |# not a heading", "    code |```", "    text |After.",
		}},
		{"quotes", "## ADDED Requirements\n### Requirement: Q\n> one\n> two\n>\n> three\nlazy", []string{
			"## ADDED|ADDED Requirements", "  ### Q", "    quote |one two", "    quote |three lazy",
		}},
		{"other headings", "## added requirements\n### Requirement: R\n### Notes\n##### Deep", []string{
			"## ADDED|added requirements", "  ### R", "    heading |Notes", "    heading |Deep",
		}},
		{"table and rule", "## ADDED Requirements\n### Requirement: T\nText\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\n* * *\nAfter", []string{
			"## ADDED|ADDED Requirements", "  ### T", "    text |Text",
			"    code || a | b |", "    code ||---|---|", "    code || 1 | 2 |", "    code |---", "    code |* * *", "    text |After",
		}},
		{"fences close as they open", "## ADDED Requirements\n### Requirement: F\n````md\n```\n~~~\n~~~~~\n## not a section\n````\nAfter.", []string{
			"## ADDED|ADDED Requirements", "  ### F",
			"    code |````md", "    code |```", "    code |~~~", "    code |~~~~~", "    code |## not a section", "    code |````", "    text |After.",
		}},
		{"indented fence inside", "## ADDED Requirements\n### Requirement: J\n```\n    ```\n## not a section\n```\nAfter.", []string{
			"## ADDED|ADDED Requirements", "  ### J",
			"    code |```", "    code |    ```", "    code |## not a section", "    code |```", "    text |After.",
		}},
		{"indented", "## ADDED Requirements\n### Requirement: I\n    ## not a section\n    ```\nAfter.", []string{
			"## ADDED|ADDED Requirements", "  ### I", "    text |## not a section ``` After.",
		}},
	} {
		if got := shape(specOf(tt.text)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got\n%s\nwant\n%s", tt.name, strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
		}
	}
}
