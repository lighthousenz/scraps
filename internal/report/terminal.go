package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lighthousenz/scraps/internal/model"
)

// ANSI colour codes used in terminal output.
const (
	colReset  = "\033[0m"
	colRed    = "\033[31m"
	colGreen  = "\033[32m"
	colYellow = "\033[33m"
)

// TerminalOpts configures the terminal summary output.
type TerminalOpts struct {
	Color bool // whether to use ANSI colours
}

// Terminal writes the run report as a terminal summary matching the
// Quick start sample in the README.
func Terminal(w io.Writer, report *model.RunReport, failOn map[model.Verdict]bool, opts TerminalOpts) error {
	var b strings.Builder

	// Header line: scraps <version> · <subject> <version> [, ...] · seed <seed>
	b.WriteString(formatHeader(report))
	b.WriteString("\n\n")

	// One line per scenario.
	for _, r := range report.Results {
		b.WriteString(formatResultLine(r, failOn, opts))
		b.WriteString("\n")
	}

	// Counts line.
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(markdownCounts(report.Results))
	b.WriteString("\n")

	// Artefacts path — use the common prefix of all artefacts paths.
	if artefactsDir := commonArtefactsDir(report.Results); artefactsDir != "" {
		fmt.Fprintf(&b, "  artefacts: %s\n", artefactsDir)
	}

	// Exit line.
	failing := failingVerdicts(report.Results, failOn)
	if len(failing) > 0 {
		fmt.Fprintf(&b, "  exit 1 (%s)\n", strings.Join(failing, ", "))
	} else {
		b.WriteString("  exit 0\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func formatHeader(report *model.RunReport) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("scraps %s", report.Scraps))

	// Sorted subjects for stability.
	names := make([]string, 0, len(report.Subjects))
	for name := range report.Subjects {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s %s", name, report.Subjects[name]))
	}

	parts = append(parts, fmt.Sprintf("seed %d", report.Seed))
	return strings.Join(parts, " · ")
}

func formatResultLine(r model.ScenarioResult, failOn map[model.Verdict]bool, opts TerminalOpts) string {
	verdict := string(r.Verdict)
	if failOn[r.Verdict] {
		verdict = strings.ToUpper(verdict)
	}

	// Colour the verdict.
	if opts.Color {
		switch {
		case r.Verdict == model.VerdictAsExpected:
			verdict = colGreen + verdict + colReset
		case r.Verdict == model.VerdictBroken || r.Verdict == model.VerdictRegressed:
			verdict = colRed + verdict + colReset
		case failOn[r.Verdict]:
			verdict = colYellow + verdict + colReset
		}
	}

	// Title truncation — cap at 40 chars.
	title := r.Title
	const maxTitle = 40
	if len(title) > maxTitle {
		title = title[:maxTitle-1] + "…"
	}

	// Outcome: "expected → actual" when they differ.
	outcome := r.Actual
	if r.Expected != r.Actual && r.Verdict != model.VerdictBroken {
		outcome = fmt.Sprintf("%s → %s", r.Expected, r.Actual)
	}
	if r.Verdict == model.VerdictBroken {
		outcome = ""
	}

	// Duration.
	duration := fmt.Sprintf("%.1fs", float64(r.DurationMs)/1000)

	// Format the line to match the sample output.
	if outcome != "" {
		return fmt.Sprintf("  %-10s %-42s %-14s %-20s %s", r.ID, title, verdict, outcome, duration)
	}
	return fmt.Sprintf("  %-10s %-42s %-14s %s", r.ID, title, verdict, duration)
}

func commonArtefactsDir(results []model.ScenarioResult) string {
	if len(results) == 0 {
		return ""
	}
	// Find the common directory prefix.
	prefix := results[0].Artefacts
	for _, r := range results[1:] {
		for !strings.HasPrefix(r.Artefacts, prefix) {
			// Trim trailing slash before looking for the parent directory.
			trimmed := strings.TrimRight(prefix, "/")
			idx := strings.LastIndex(trimmed, "/")
			if idx < 0 {
				return ""
			}
			prefix = trimmed[:idx+1]
		}
	}
	return prefix
}

func failingVerdicts(results []model.ScenarioResult, failOn map[model.Verdict]bool) []string {
	seen := make(map[model.Verdict]bool)
	var failing []string
	for _, r := range results {
		if failOn[r.Verdict] && !seen[r.Verdict] {
			seen[r.Verdict] = true
			failing = append(failing, fmt.Sprintf("%s is in --fail-on", r.Verdict))
		}
	}
	return failing
}
