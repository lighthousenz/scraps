package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lighthousenz/scraps/internal/config"
	"github.com/lighthousenz/scraps/internal/verdict"
)

func cmdExplain(args []string) int {
	fs := flag.NewFlagSet("scraps explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var subjects multiFlag
	fs.Var(&subjects, "subject", "Set a subject version (name=version); repeatable")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: scraps explain [--subject name=version] <scenario>")
		return 2
	}
	scenarioPath := fs.Arg(0)

	// Load project.
	lp, loadProblems := config.LoadProject([]string{scenarioPath})
	if lp == nil {
		for _, p := range loadProblems {
			fmt.Fprintln(os.Stderr, p)
		}
		return 2
	}

	if len(lp.Scenarios) == 0 {
		fmt.Fprintln(os.Stderr, "no scenarios found")
		return 2
	}

	// Resolve subjects.
	resolvedSubjects, err := resolveSubjects(lp, subjects)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 2
	}

	scenario := lp.Scenarios[0].Scenario

	// Print scenario info.
	fmt.Printf("Scenario: %s\n", scenario.ID)
	if scenario.Title != "" {
		fmt.Printf("Title:    %s\n", scenario.Title)
	}
	fmt.Println()

	// Print subjects.
	fmt.Println("Subjects:")
	for name, ver := range resolvedSubjects {
		subj := lp.Project.Subjects[name]
		fmt.Printf("  %s = %s (scheme: %s)\n", name, ver, subj.Scheme)
	}
	fmt.Println()

	// Print history entries and which one matches.
	fmt.Println("History:")
	histResult, err := verdict.EvalHistory(
		scenario.History,
		lp.Project.Subjects,
		resolvedSubjects,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: evaluate history: %s\n", err)
		return 2
	}

	if len(scenario.History) == 0 {
		fmt.Println("  (no history entries; implicit expect: correct)")
	}
	for i, entry := range scenario.History {
		marker := "  "
		if i == histResult.Index {
			marker = "→ "
		}
		when := formatWhen(entry.When)
		fmt.Printf("  %s[%d] when: %s → expect: %s\n", marker, i, when, entry.Expect)
	}
	fmt.Println()

	// Print the expected outcome.
	fmt.Printf("Expected: %s", histResult.Expected)
	if histResult.Index >= 0 {
		fmt.Printf(" (matched history[%d])", histResult.Index)
	} else {
		fmt.Print(" (no history entry matched; default)")
	}
	fmt.Println()

	// Print outcomes.
	if len(scenario.Outcomes) > 0 {
		fmt.Println()
		fmt.Println("Outcomes:")
		for name, checks := range scenario.Outcomes {
			fmt.Printf("  %s: %d check(s)\n", name, len(checks))
		}
	}

	return 0
}

func formatWhen(when map[string]string) string {
	if len(when) == 0 {
		return "(always)"
	}
	var parts []string
	for name, constraint := range when {
		parts = append(parts, fmt.Sprintf("%s %s", name, constraint))
	}
	return strings.Join(parts, ", ")
}
