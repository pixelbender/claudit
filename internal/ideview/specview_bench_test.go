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
	for name, folds := range map[string]Specs{"folded": nil, "open": all} {
		b.Run(name, func(b *testing.B) {
			st := specState()
			st.Tabs[2].Buf.SetText(text)
			o := Options{Now: now, Specs: folds, Scroll: Scroll{}, Terms: TermCache{}, SpecRows: SpecCache{}}
			b.ReportAllocs()
			for b.Loop() {
				Prune(st, o, 120, 40)
				RenderWithScrollbars(st, o, 120, 40)
				Wheel(st, o, 120, 40, 60, 10, 1)
			}
		})
	}
}
