package service

import "testing"

// TestFirstSummaryLine pins that the GetSummary markdown fallback skips the
// "# {title}" / "## Section" headings renderSummaryMD emits and returns the first
// PROSE line — so a meeting whose structured short-summary is missing shows the
// summary text, not its own title heading.
func TestFirstSummaryLine(t *testing.T) {
	cases := []struct{ name, md, want string }{
		{
			name: "skips title heading",
			md:   "# Weekly Sync\n\nWe agreed to ship v1 by Friday.\n\n## Decisions\n\n1. Ship v1.",
			want: "We agreed to ship v1 by Friday.",
		},
		{
			name: "leading blank lines then heading then prose",
			md:   "\n\n#   Spaced Title\n\nThe real summary.\n",
			want: "The real summary.",
		},
		{
			name: "no prose, only headings and list → first non-heading line",
			md:   "# Title\n\n## Decisions\n\n1. Do the thing.",
			want: "1. Do the thing.",
		},
		{
			name: "prose first (no heading) is returned verbatim",
			md:   "Just a plain summary.",
			want: "Just a plain summary.",
		},
		{
			name: "only headings → empty",
			md:   "# Title\n\n## Decisions\n## Risks",
			want: "",
		},
		{name: "empty", md: "", want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstSummaryLine(c.md); got != c.want {
				t.Errorf("firstSummaryLine(%q) = %q, want %q", c.md, got, c.want)
			}
		})
	}
}
