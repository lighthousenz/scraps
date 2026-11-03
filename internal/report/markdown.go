package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lighthousenz/scraps/internal/model"
)

// Markdown writes the run report as a Markdown summary.
// Non-as-expected rows come first, then everything else, in input order.
func Markdown(w io.Writer, report *model.RunReport) error {
	var b strings.Builder

	// Heading
	b.WriteString("# Scraps Report\n\n")

	// Counts line
	b.WriteString(markdownCounts(report.Results))
	b.WriteString("\n\n")

	// Table header
	b.WriteString("| Scenario | Verdict | Expected → Actual | Links |\n")
	b.WriteString("|---|---|---|---|\n")

	// Non-as-expected rows first, then as-expected, both in input order.
	for _, r := range report.Results {
		if r.Verdict != model.VerdictAsExpected {
			b.WriteString(markdownRow(r))
			b.WriteString("\n")
		}
	}
	for _, r := range report.Results {
		if r.Verdict == model.VerdictAsExpected {
			b.WriteString(markdownRow(r))
			b.WriteString("\n")
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func markdownCounts(results []model.ScenarioResult) string {
	counts := make(map[model.Verdict]int)
	for _, r := range results {
		counts[r.Verdict]++
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("%d scenarios", len(results)))

	// List only non-zero verdicts, in a stable order.
	verdictOrder := []model.Verdict{
		model.VerdictAsExpected,
		model.VerdictFixed,
		model.VerdictRegressed,
		model.VerdictChanged,
		model.VerdictUnrecognised,
		model.VerdictBroken,
	}
	for _, v := range verdictOrder {
		if n := counts[v]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, v))
		}
	}

	return strings.Join(parts, " · ")
}

func markdownRow(r model.ScenarioResult) string {
	scenario := fmt.Sprintf("[%s](%s)", r.ID, r.Artefacts)

	outcome := fmt.Sprintf("%s → %s", r.Expected, r.Actual)
	if r.Expected == r.Actual {
		outcome = r.Actual
	}
	if len(r.Overlapping) > 0 {
		outcome = fmt.Sprintf("%s → overlapping: %s", r.Expected, strings.Join(r.Overlapping, ", "))
	}
	if r.Verdict == model.VerdictBroken {
		outcome = r.Reason
	}

	links := formatMarkdownLinks(r.Links)

	return fmt.Sprintf("| %s | %s | %s | %s |", scenario, r.Verdict, escapeMarkdownCell(outcome), links)
}

// escapeMarkdownCell escapes pipe characters and newlines so the value
// does not break a GFM table row.
func escapeMarkdownCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

func formatMarkdownLinks(links map[string]model.LinkResult) string {
	if len(links) == 0 {
		return ""
	}

	// Sort link names for stable output.
	names := make([]string, 0, len(links))
	for name := range links {
		names = append(names, name)
	}
	sort.Strings(names)

	var parts []string
	for _, name := range names {
		link := links[name]
		if link.URL != "" {
			parts = append(parts, fmt.Sprintf("[%s](%s)", name, link.URL))
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}
