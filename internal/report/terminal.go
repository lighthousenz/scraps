package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

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

	// Pad the verdict to a fixed visible width before wrapping in colour
	// codes, so ANSI escape bytes do not misalign columns.
	verdict = fmt.Sprintf("%-14s", verdict)

	if opts.Color {
		plain := strings.TrimRight(verdict, " ")
		pad := verdict[len(plain):]
		switch {
		case r.Verdict == model.VerdictAsExpected:
			verdict = colGreen + plain + colReset + pad
		case r.Verdict == model.VerdictBroken || r.Verdict == model.VerdictRegressed:
			verdict = colRed + plain + colReset + pad
		case failOn[r.Verdict]:
			verdict = colYellow + plain + colReset + pad
		}
	}

	// Title truncation — cap at 40 runes to avoid cutting mid-character.
	title := r.Title
	const maxTitle = 40
	if utf8.RuneCountInString(title) > maxTitle {
		runes := []rune(title)
		title = string(runes[:maxTitle-1]) + "…"
	}

	// Outcome: "expected → actual" when they differ.
	outcome := r.Actual
	if r.Expected != r.Actual && r.Verdict != model.VerdictBroken {
		outcome = fmt.Sprintf("%s → %s", r.Expected, r.Actual)
	}
	if len(r.Overlapping) > 0 {
		outcome = fmt.Sprintf("%s → overlapping: %s", r.Expected, strings.Join(r.Overlapping, ", "))
	}
	if r.Verdict == model.VerdictBroken {
		outcome = ""
	}

	// Duration.
	duration := fmt.Sprintf("%.1fs", float64(r.DurationMs)/1000)

	// Format the line to match the sample output.
	if outcome != "" {
		return fmt.Sprintf("  %-10s %-42s %s %-20s %s", r.ID, title, verdict, outcome, duration)
	}
	return fmt.Sprintf("  %-10s %-42s %s %s", r.ID, title, verdict, duration)
}

func commonArtefactsDir(results []model.ScenarioResult) string {
	if len(results) == 0 {
		return ""
	}

	// parentDir returns the parent of a per-scenario artefacts path,
	// which is the run directory.
	parentDir := func(p string) string {
		trimmed := strings.TrimRight(p, "/")
		idx := strings.LastIndex(trimmed, "/")
		if idx < 0 {
			return ""
		}
		return trimmed[:idx+1]
	}

	// Start from the parent of the first result's artefacts directory.
	prefix := parentDir(results[0].Artefacts)
	if prefix == "" {
		return ""
	}
	for _, r := range results[1:] {
		for !strings.HasPrefix(r.Artefacts, prefix) {
			prefix = parentDir(prefix)
			if prefix == "" {
				return ""
			}
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
