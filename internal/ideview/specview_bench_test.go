package ideview

import (
	"fmt"
	"strings"
	"testing"
)

// benchSpec is a delta shaped as a large real one, and longer: two sections, 46 requirements with
// a paragraph each, 4 or 5 scenarios to a requirement, two steps to a scenario; 1517 lines.
func benchSpec() string {
	var b strings.Builder
	words := strings.Repeat("слово text ", 9)
	for s, op := range []string{"ADDED", "MODIFIED"} {
		fmt.Fprintf(&b, "## %s Requirements\n\n", op)
		for r := range 23 {
			fmt.Fprintf(&b, "### Requirement: Requirement %d.%d of the folders\n\n", s, r)
			fmt.Fprintf(&b, "The system SHALL %s `code`\n%s\nand MUST NOT %s.\n\n", words, words, words)
			for c := range 4 + r%2 {
				fmt.Fprintf(&b, "#### Scenario: Scenario %d of the requirement\n\n", c)
				fmt.Fprintf(&b, "- **WHEN** %s\n  %s\n- **THEN** %s\n\n", words, words, words)
			}
		}
	}
	return b.String()
}

// A frame of the spec view at 120×40, its requirements as they start and all open: what View
// asks of it, the wheel's scroll included.
func BenchmarkSpecView(b *testing.B) {
	text := benchSpec()
	all := Specs{specFile: {}}
	for _, s := range specOf(text).sections {
		for _, r := range s.reqs {
			all[specFile][r.key] = true
			for _, sc := range r.scenarios {
				all[specFile][sc.key] = true
			}
		}
	}
	// the main spec the MODIFIED requirements change: a word of each paragraph, a scenario fewer
	main := strings.Split(strings.NewReplacer("## ADDED Requirements", "## Other Requirements", "## MODIFIED Requirements", "## Requirements",
		"The system SHALL слово", "The system SHALL word", "#### Scenario: Scenario 3 of", "#### Scenario: Scenario 7 of").Replace(text), "\n")
	for name, c := range map[string]struct {
		folds Specs
		main  func(string) []string
	}{"folded": {}, "open": {folds: all}, "diff": {folds: all, main: func(string) []string { return main }}} {
		b.Run(name, func(b *testing.B) {
			st := specState()
			st.Tabs[2].Buf.SetText(text)
			o := Options{Now: now, Specs: c.folds, Main: c.main, Scroll: Scroll{}, Terms: TermCache{}, SpecRows: SpecCache{}}
			if Render(st, o, 120, 40); c.main != nil {
				if n := diffed(o.SpecRows[specFile].doc); n != 23 {
					b.Fatalf("%d requirements diffed", n)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				Prune(st, o, 120, 40)
				RenderWithScrollbars(st, o, 120, 40)
				Wheel(st, o, 120, 40, 60, 10, 1)
			}
		})
	}
}

// diffed is how many requirements of sp are compared to the main spec.
func diffed(sp *spec) int {
	n := 0
	for _, s := range sp.sections {
		for _, r := range s.reqs {
			if r.diff != nil {
				n++
			}
		}
	}
	return n
}
