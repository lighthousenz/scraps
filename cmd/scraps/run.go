package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/lighthousenz/scraps/internal/actor"
	"github.com/lighthousenz/scraps/internal/conductor"
	"github.com/lighthousenz/scraps/internal/config"
	"github.com/lighthousenz/scraps/internal/model"
	"github.com/lighthousenz/scraps/internal/report"
	"github.com/lighthousenz/scraps/internal/verdict"
)

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("scraps run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var subjects multiFlag
	fs.Var(&subjects, "subject", "Set a subject version (name=version); repeatable")

	var only multiFlag
	fs.Var(&only, "only", "Select scenarios by id; repeatable")

	var tags multiFlag
	fs.Var(&tags, "tag", "Select scenarios by tag; repeatable")

	failOnStr := fs.String("fail-on", "", "Comma-separated verdicts that exit non-zero")

	var reports multiFlag
	fs.Var(&reports, "report", "Write a report (format=path); repeatable")

	seed := fs.Int64("seed", 0, "Reuse the seed from an earlier run")

	parallel := fs.Int("parallel", 1, "Run up to n scenarios at once")

	keep := fs.Bool("keep", false, "Keep workdirs for passing scenarios too")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	// Load project.
	paths := fs.Args()
	lp, loadProblems := config.LoadProject(paths)
	if lp == nil {
		for _, p := range loadProblems {
			fmt.Fprintln(os.Stderr, p)
		}
		return 2
	}
	if len(loadProblems) > 0 {
		for _, p := range loadProblems {
			fmt.Fprintln(os.Stderr, p)
		}
		return 2
	}

	// Validate.
	validateProblems := config.Validate(lp)
	if len(validateProblems) > 0 {
		for _, p := range validateProblems {
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

	// Filter scenarios.
	scenarios := filterScenarios(lp.Scenarios, only, tags)
	if len(scenarios) == 0 {
		fmt.Fprintln(os.Stderr, "no scenarios matched filters")
		return 2
	}

	// Parse and validate fail-on.
	var failOn []string
	if *failOnStr != "" {
		failOn = strings.Split(*failOnStr, ",")
		for i := range failOn {
			failOn[i] = strings.TrimSpace(failOn[i])
		}
	} else if len(lp.Project.Defaults.FailOn) > 0 {
		failOn = lp.Project.Defaults.FailOn
	}
	if len(failOn) > 0 {
		valid := make(map[string]bool)
		for _, v := range verdict.AllVerdicts() {
			valid[string(v)] = true
		}
		for _, v := range failOn {
			if v == "" {
				continue
			}
			if !valid[v] {
				fmt.Fprintf(os.Stderr, "error: unknown verdict %q in --fail-on\n", v)
				return 2
			}
		}
	}

	// Resolve seed.
	runSeed := *seed
	if runSeed == 0 {
		runSeed = rand.Int64()
	}

	// Build run directory with a unique name per invocation.
	runsDir := filepath.Join(lp.ProjectDir, ".scraps", "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: create runs directory: %s\n", err)
		return 3
	}
	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
	runDir := filepath.Join(runsDir, timestamp)
	if err := os.Mkdir(runDir, 0o755); err != nil {
		// Collision — append a random suffix to make it unique.
		runDir = filepath.Join(runsDir, fmt.Sprintf("%s-%04x", timestamp, rand.IntN(0x10000)))
		if err := os.Mkdir(runDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error: create run directory: %s\n", err)
			return 3
		}
	}

	cacheDir := filepath.Join(lp.ProjectDir, ".scraps", "cache", "prepared")

	// Parse step and ready timeouts from project defaults.
	stepTimeout := parseTimeout(lp.Project.Defaults.StepTimeout)
	readyTimeout := parseTimeout(lp.Project.Defaults.ReadyTimeout)

	cfg := &conductor.RunConfig{
		Project:      lp,
		Scenarios:    scenarios,
		Subjects:     resolvedSubjects,
		Seed:         runSeed,
		StepTimeout:  stepTimeout,
		ReadyTimeout: readyTimeout,
		FailOn:       failOn,
		Keep:         *keep,
		RunDir:       runDir,
		Version:      version,
		CacheDir:     cacheDir,
	}

	// Set up context with SIGINT handling.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Run scenarios.
	var result *conductor.RunResult
	if *parallel > 1 {
		result, err = conductor.RunParallel(ctx, cfg, *parallel)
	} else {
		result, err = conductor.Run(ctx, cfg)
	}

	if err != nil {
		if errors.Is(err, actor.ErrMiseNotFound) {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			return 3
		}
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 3
	}

	// Always write report.json in the run directory.
	reportPath := filepath.Join(runDir, "report.json")
	if err := writeJSONReport(reportPath, &result.Report); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write report.json: %s\n", err)
	}

	// Write requested reports.
	for _, spec := range reports {
		if err := writeReport(spec, &result.Report, result.FailOn); err != nil {
			fmt.Fprintf(os.Stderr, "warning: write report %q: %s\n", spec, err)
		}
	}

	// Print terminal summary.
	failOnMap := result.FailOn
	if err := report.Terminal(os.Stdout, &result.Report, failOnMap, report.TerminalOpts{Color: true}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: terminal output: %s\n", err)
	}

	// Clean up workdirs for passing scenarios if --keep is not set.
	if !*keep {
		cleanWorkdirs(&result.Report, failOnMap, runDir)
	}

	if result.HasFail {
		return 1
	}
	return 0
}

func resolveSubjects(lp *config.LoadedProject, cliSubjects []string) (map[string]string, error) {
	resolved := make(map[string]string)

	// First resolve from project config.
	for name, subj := range lp.Project.Subjects {
		if subj.Resolve.Value != "" {
			resolved[name] = subj.Resolve.Value
		} else if subj.Resolve.Env != "" {
			v := os.Getenv(subj.Resolve.Env)
			if v != "" {
				resolved[name] = v
			}
		}
	}

	// Override with CLI flags.
	for _, s := range cliSubjects {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --subject %q: expected name=version", s)
		}
		name, ver := parts[0], parts[1]
		if _, ok := lp.Project.Subjects[name]; !ok {
			return nil, fmt.Errorf("unknown subject %q", name)
		}
		resolved[name] = ver
	}

	// Check all subjects are resolved.
	for name := range lp.Project.Subjects {
		if _, ok := resolved[name]; !ok {
			return nil, fmt.Errorf("subject %q has no version: use --subject %s=<version> or set the resolve env", name, name)
		}
	}

	return resolved, nil
}

func filterScenarios(scenarios []config.LoadedScenario, only, tags multiFlag) []config.LoadedScenario {
	if len(only) == 0 && len(tags) == 0 {
		return scenarios
	}

	onlySet := make(map[string]bool, len(only))
	for _, id := range only {
		onlySet[id] = true
	}

	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	var filtered []config.LoadedScenario
	for _, ls := range scenarios {
		if len(onlySet) > 0 && !onlySet[ls.Scenario.ID] {
			continue
		}
		if len(tagSet) > 0 {
			hasTag := false
			for _, t := range ls.Scenario.Tags {
				if tagSet[t] {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
		}
		filtered = append(filtered, ls)
	}
	return filtered
}

func writeJSONReport(path string, rpt *model.RunReport) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := report.JSON(f, rpt); err != nil {
		return err
	}
	return f.Close()
}

func writeReport(spec string, rpt *model.RunReport, failOn map[model.Verdict]bool) error {
	parts := strings.SplitN(spec, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("expected format=path, got %q", spec)
	}
	format, path := parts[0], parts[1]

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	switch format {
	case "json":
		err = report.JSON(f, rpt)
	case "junit":
		err = report.JUnit(f, rpt, failOn)
	case "markdown":
		err = report.Markdown(f, rpt)
	default:
		err = fmt.Errorf("unknown report format %q", format)
	}
	if err != nil {
		return err
	}
	return f.Close()
}

func cleanWorkdirs(rpt *model.RunReport, failOn map[model.Verdict]bool, runDir string) {
	for _, r := range rpt.Results {
		if !failOn[r.Verdict] {
			scenDir := filepath.Join(runDir, r.ID)
			_ = os.RemoveAll(filepath.Join(scenDir, "actors"))
		}
	}
}

func parseTimeout(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
