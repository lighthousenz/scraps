//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// scraps is the absolute path to the built scraps binary.
var scraps string

// repoRoot is the repository root, used to locate testdata and examples.
var repoRoot string

func TestMain(m *testing.M) {
	// Determine repository root (the e2e/ directory is one level below).
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "getwd: %v\n", err)
		os.Exit(1)
	}
	repoRoot = filepath.Dir(wd)

	scraps = filepath.Join(repoRoot, "dist", "scraps")
	if _, err := os.Stat(scraps); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "scraps binary not found at %s; run `mise run build` first\n", scraps)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// runReport is the minimal structure we decode from report.json.
type runReport struct {
	Schema   int               `json:"schema"`
	Scraps   string            `json:"scraps"`
	Seed     int64             `json:"seed"`
	Subjects map[string]string `json:"subjects"`
	Results  []scenarioResult  `json:"results"`
}

type scenarioResult struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Expected    string   `json:"expected"`
	Actual      string   `json:"actual"`
	Verdict     string   `json:"verdict"`
	Reason      string   `json:"reason,omitempty"`
	Overlapping []string `json:"overlapping,omitempty"`
	Artefacts   string   `json:"artefacts,omitempty"`
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// copyE2EProject copies testdata/e2e/ and the toy actor into a temp dir,
// preserving the relative path from testdata/e2e/ to examples/actors/toy/.
// Returns the path to the copied project directory.
func copyE2EProject(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	projectDir := filepath.Join(base, "testdata", "e2e")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(filepath.Join(repoRoot, "testdata", "e2e"), projectDir); err != nil {
		t.Fatalf("copy e2e project: %v", err)
	}
	toyDst := filepath.Join(base, "examples", "actors", "toy")
	if err := os.MkdirAll(toyDst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(filepath.Join(repoRoot, "examples", "actors", "toy"), toyDst); err != nil {
		t.Fatalf("copy toy actor: %v", err)
	}
	return projectDir
}

// copyCollabProject copies examples/ into a temp dir preserving relative paths.
// Returns the path to the copied collab project directory.
func copyCollabProject(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	examplesDst := filepath.Join(base, "examples")
	if err := os.MkdirAll(examplesDst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(filepath.Join(repoRoot, "examples"), examplesDst); err != nil {
		t.Fatalf("copy examples: %v", err)
	}
	return filepath.Join(examplesDst, "collab")
}

// runScraps runs the scraps binary with the given args in the given dir.
// It returns stdout, stderr, and the exit code.
func runScraps(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120_000_000_000) // 2 min
	defer cancel()
	cmd := exec.CommandContext(ctx, scraps, args...)
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("exec scraps: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
		}
	}
	return stdout, stderr, exitCode
}

// readReport reads and parses report.json from the latest run directory.
func readReport(t *testing.T, projectDir string) runReport {
	t.Helper()
	runsDir := filepath.Join(projectDir, ".scraps", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("read runs dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no run directories found")
	}
	latest := entries[len(entries)-1].Name()
	reportPath := filepath.Join(runsDir, latest, "report.json")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	var rpt runReport
	if err := json.Unmarshal(data, &rpt); err != nil {
		t.Fatalf("parse report.json: %v\ndata: %s", err, data)
	}
	return rpt
}

// findResult finds a scenario result by ID.
func findResult(rpt runReport, id string) (scenarioResult, bool) {
	for _, r := range rpt.Results {
		if r.ID == id {
			return r, true
		}
	}
	return scenarioResult{}, false
}

// --------------------------------------------------------------------------
// Verdict coverage tests (testdata/e2e/)
// --------------------------------------------------------------------------

func TestVerdictCoverage(t *testing.T) {
	// Verify ready-timeout is set in the source project.
	data, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "e2e", "scraps.yaml"))
	if err != nil {
		t.Fatalf("read scraps.yaml: %v", err)
	}
	if !strings.Contains(string(data), "ready-timeout:") {
		t.Fatal("testdata/e2e/scraps.yaml does not set ready-timeout")
	}

	type expectation struct {
		verdict     string
		reason      string   // substring match for broken
		overlapping []string // expected overlapping outcomes
	}

	tests := []struct {
		version  string
		expected map[string]expectation
	}{
		{
			version: "1.0.0",
			expected: map[string]expectation{
				"E2E-0001": {verdict: "as-expected"},
				"E2E-0002": {verdict: "fixed"},
				"E2E-0003": {verdict: "unrecognised", overlapping: []string{"correct", "shouts"}},
			},
		},
		{
			version: "2.0.0",
			expected: map[string]expectation{
				"E2E-0001": {verdict: "as-expected"},
				"E2E-0002": {verdict: "as-expected"},
				"E2E-0003": {verdict: "unrecognised"},
			},
		},
		{
			version: "3.0.0",
			expected: map[string]expectation{
				"E2E-0001": {verdict: "regressed"},
				"E2E-0002": {verdict: "changed"},
				"E2E-0003": {verdict: "unrecognised"},
			},
		},
		{
			// toy=4.0.0: error code 1000 on write → step-error matches
			// write-fails in E2E-0001; E2E-0002 and E2E-0003 lack that
			// outcome so the actual is empty → unrecognised.
			version: "4.0.0",
			expected: map[string]expectation{
				"E2E-0001": {verdict: "regressed"},
				"E2E-0002": {verdict: "unrecognised"},
				"E2E-0003": {verdict: "unrecognised"},
			},
		},
		{
			// toy=5.0.0: protocol violation (invalid JSON on stdout).
			version: "5.0.0",
			expected: map[string]expectation{
				"E2E-0001": {verdict: "broken", reason: "invalid"},
				"E2E-0002": {verdict: "broken", reason: "invalid"},
				"E2E-0003": {verdict: "broken", reason: "invalid"},
			},
		},
		{
			version: "6.0.0",
			expected: map[string]expectation{
				"E2E-0001": {verdict: "broken", reason: "ready"},
				"E2E-0002": {verdict: "broken", reason: "ready"},
				"E2E-0003": {verdict: "broken", reason: "ready"},
			},
		},
	}

	for _, tc := range tests {
		t.Run("toy="+tc.version, func(t *testing.T) {
			tmp := copyE2EProject(t)
			_, stderr, exitCode := runScraps(t, tmp,
				"run", "--subject", "toy="+tc.version)

			// Determine expected exit code.
			allAsExpected := true
			for _, exp := range tc.expected {
				if exp.verdict != "as-expected" {
					allAsExpected = false
					break
				}
			}
			wantExit := 1
			if allAsExpected {
				wantExit = 0
			}
			if exitCode != wantExit {
				t.Errorf("exit code: got %d, want %d\nstderr: %s", exitCode, wantExit, stderr)
			}

			rpt := readReport(t, tmp)
			for id, exp := range tc.expected {
				r, ok := findResult(rpt, id)
				if !ok {
					t.Errorf("scenario %s not found in report", id)
					continue
				}
				if r.Verdict != exp.verdict {
					t.Errorf("%s: verdict = %q, want %q", id, r.Verdict, exp.verdict)
				}
				if exp.reason != "" && !strings.Contains(strings.ToLower(r.Reason), exp.reason) {
					t.Errorf("%s: reason = %q, want substring %q", id, r.Reason, exp.reason)
				}
				if len(exp.overlapping) > 0 {
					got := slices.Clone(r.Overlapping)
					want := slices.Clone(exp.overlapping)
					slices.Sort(got)
					slices.Sort(want)
					if !slices.Equal(got, want) {
						t.Errorf("%s: overlapping = %v, want %v", id, r.Overlapping, exp.overlapping)
					}
				}
			}
		})
	}
}

func TestVerdictCoverageFailOn(t *testing.T) {
	// toy=1.0.0: E2E-0001 as-expected, E2E-0002 fixed, E2E-0003 unrecognised.
	// Default --fail-on includes everything except as-expected → exit 1.
	t.Run("default-fail-on-exits-1", func(t *testing.T) {
		tmp := copyE2EProject(t)
		_, _, exitCode := runScraps(t, tmp,
			"run", "--subject", "toy=1.0.0")
		if exitCode != 1 {
			t.Errorf("exit code: got %d, want 1", exitCode)
		}
	})

	// With --fail-on regressed,broken, toy=1.0.0 should exit 0
	// because the only non-as-expected verdicts are fixed and unrecognised.
	t.Run("fail-on-regressed-broken-exits-0-for-fixed", func(t *testing.T) {
		tmp := copyE2EProject(t)
		_, stderr, exitCode := runScraps(t, tmp,
			"run", "--subject", "toy=1.0.0", "--fail-on", "regressed,broken")
		if exitCode != 0 {
			t.Errorf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}
	})
}

// --------------------------------------------------------------------------
// Real collaboration tests (examples/collab/)
// --------------------------------------------------------------------------

func TestCollabBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping collab tests in short mode")
	}

	t.Run("yjs=13.6.27", func(t *testing.T) {
		tmp := copyCollabProject(t)
		_, stderr, exitCode := runScraps(t, tmp,
			"run", "--subject", "yjs=13.6.27")
		if exitCode != 0 {
			t.Fatalf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}
		rpt := readReport(t, tmp)
		for _, r := range rpt.Results {
			if r.Verdict != "as-expected" {
				t.Errorf("%s: verdict = %q, want as-expected", r.ID, r.Verdict)
			}
		}
	})

	// Newest yjs 13.6.x release published for at least 14 days: 13.6.33.
	t.Run("yjs=13.6.33", func(t *testing.T) {
		tmp := copyCollabProject(t)
		_, stderr, exitCode := runScraps(t, tmp,
			"run", "--subject", "yjs=13.6.33")
		if exitCode != 0 {
			t.Fatalf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}
		rpt := readReport(t, tmp)
		for _, r := range rpt.Results {
			if r.Verdict != "as-expected" {
				t.Errorf("%s: verdict = %q, want as-expected", r.ID, r.Verdict)
			}
		}
	})
}

func TestCollabDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping collab determinism tests in short mode")
	}

	seeds := []string{"11111", "22222", "33333", "44444", "55555"}

	// Run the first seed, then keep the report for comparison.
	firstTmp := copyCollabProject(t)
	_, stderr, exitCode := runScraps(t, firstTmp,
		"run", "--subject", "yjs=13.6.27", "--seed", seeds[0], "--keep")
	if exitCode != 0 {
		t.Fatalf("first run (seed=%s) exit code: got %d, want 0\nstderr: %s",
			seeds[0], exitCode, stderr)
	}
	firstReport := readReport(t, firstTmp)

	for _, seed := range seeds[1:] {
		t.Run("seed="+seed, func(t *testing.T) {
			tmp := copyCollabProject(t)
			_, stderr, exitCode := runScraps(t, tmp,
				"run", "--subject", "yjs=13.6.27", "--seed", seed, "--keep")
			if exitCode != 0 {
				t.Fatalf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
			}

			rpt := readReport(t, tmp)
			for _, r := range rpt.Results {
				fr, ok := findResult(firstReport, r.ID)
				if !ok {
					t.Errorf("scenario %s not in first run", r.ID)
					continue
				}
				if r.Verdict != fr.Verdict {
					t.Errorf("%s: verdict seed=%s: %q, seed=%s: %q",
						r.ID, seeds[0], fr.Verdict, seed, r.Verdict)
				}
			}

			// Compare snapshot artefacts byte-for-byte.
			compareSnapshotArtefacts(t, firstTmp, firstReport, tmp, rpt)
		})
	}
}

// compareSnapshotArtefacts compares snapshot artefact files between two runs.
// Artefacts paths in the report are absolute, so they are used directly.
func compareSnapshotArtefacts(t *testing.T, _ string, rptA runReport, _ string, rptB runReport) {
	t.Helper()
	compared := 0
	for _, ra := range rptA.Results {
		rb, ok := findResult(rptB, ra.ID)
		if !ok || ra.Artefacts == "" || rb.Artefacts == "" {
			continue
		}
		aArtDir := ra.Artefacts
		bArtDir := rb.Artefacts
		if _, err := os.Stat(aArtDir); err != nil {
			t.Errorf("artefact dir for %s (run A) missing: %v", ra.ID, err)
			continue
		}
		if _, err := os.Stat(bArtDir); err != nil {
			t.Errorf("artefact dir for %s (run B) missing: %v", ra.ID, err)
			continue
		}
		err := filepath.Walk(aArtDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(aArtDir, path)
			if err != nil {
				return fmt.Errorf("rel %s: %w", path, err)
			}
			// Only compare snapshot JSON files.
			if !strings.HasSuffix(rel, ".json") {
				return nil
			}
			aData, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			bPath := filepath.Join(bArtDir, rel)
			bData, err := os.ReadFile(bPath)
			if err != nil {
				t.Errorf("artefact %s: exists in run A but not B: %v", rel, err)
				return nil
			}
			if !bytes.Equal(aData, bData) {
				t.Errorf("artefact %s: differs between runs", rel)
			}
			compared++
			return nil
		})
		if err != nil {
			t.Errorf("walking artefacts for %s: %v", ra.ID, err)
		}
	}
	if compared == 0 {
		t.Error("no artefact files were compared; expected at least one JSON snapshot")
	}
}

func TestCollabParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping collab parallel tests in short mode")
	}
	tmp := copyCollabProject(t)

	_, stderr, exitCode := runScraps(t, tmp,
		"run", "--subject", "yjs=13.6.27", "--parallel", "4")
	if exitCode != 0 {
		t.Fatalf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
	}

	rpt := readReport(t, tmp)
	for _, r := range rpt.Results {
		if r.Verdict != "as-expected" {
			t.Errorf("%s: verdict = %q, want as-expected", r.ID, r.Verdict)
		}
	}
}

// --------------------------------------------------------------------------
// Command tests
// --------------------------------------------------------------------------

func TestValidateExits0(t *testing.T) {
	t.Run("testdata/e2e", func(t *testing.T) {
		tmp := copyE2EProject(t)
		_, stderr, exitCode := runScraps(t, tmp, "validate")
		if exitCode != 0 {
			t.Errorf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}
	})

	t.Run("examples/collab", func(t *testing.T) {
		tmp := copyCollabProject(t)
		_, stderr, exitCode := runScraps(t, tmp, "validate")
		if exitCode != 0 {
			t.Errorf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}
	})
}

func TestValidateExits2OnBrokenStep(t *testing.T) {
	tmp := copyE2EProject(t)

	brokenScenario := `id: BROKEN-001
title: Broken scenario

actors:
  toy: { uses: toy }

steps:
  - nonexistent: write

outcomes:
  correct:
    result:
      json: { path: "$.text", equals: "hello" }

history: []
`
	brokenDir := filepath.Join(tmp, "scenarios", "broken")
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "scenario.yaml"), []byte(brokenScenario), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, exitCode := runScraps(t, tmp, "validate")
	if exitCode != 2 {
		t.Errorf("exit code: got %d, want 2\nstderr: %s", exitCode, stderr)
	}
	locPattern := regexp.MustCompile(`\.yaml:\d+:\d+`)
	if !locPattern.MatchString(stderr) {
		t.Errorf("expected path:line:col (.yaml:N:N) in error output, got: %s", stderr)
	}
}

func TestSchemaGeneratesValidSchema(t *testing.T) {
	t.Run("testdata/e2e", func(t *testing.T) {
		tmp := copyE2EProject(t)
		_, stderr, exitCode := runScraps(t, tmp, "schema")
		if exitCode != 0 {
			t.Fatalf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}

		schemaPath := filepath.Join(tmp, ".scraps", "schema.json")
		data, err := os.ReadFile(schemaPath)
		if err != nil {
			t.Fatalf("read schema.json: %v", err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("schema.json is not valid JSON: %v", err)
		}
		if _, ok := schema["$schema"]; !ok {
			t.Error("schema.json missing $schema key")
		}
	})

	t.Run("examples/collab", func(t *testing.T) {
		tmp := copyCollabProject(t)
		_, stderr, exitCode := runScraps(t, tmp, "schema")
		if exitCode != 0 {
			t.Fatalf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}

		schemaPath := filepath.Join(tmp, ".scraps", "schema.json")
		data, err := os.ReadFile(schemaPath)
		if err != nil {
			t.Fatalf("read schema.json: %v", err)
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("schema.json is not valid JSON: %v", err)
		}
	})
}

func TestDiagramContainsCascadingStop(t *testing.T) {
	tmp := copyCollabProject(t)

	stdout, stderr, exitCode := runScraps(t, tmp,
		"diagram", "scenarios/EX-0002-persist-reload/scenario.yaml")
	if exitCode != 0 {
		t.Fatalf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
	}

	if !strings.Contains(stdout, "stop") {
		t.Errorf("diagram output does not mention 'stop':\n%s", stdout)
	}
	if !strings.Contains(stdout, "Note") {
		t.Errorf("diagram output does not contain a 'Note' (cascading stop):\n%s", stdout)
	}
}

func TestExplainNamesHistoryEntry(t *testing.T) {
	tmp := copyE2EProject(t)

	stdout, stderr, exitCode := runScraps(t, tmp,
		"explain", "--subject", "toy=2.0.0", "scenarios/E2E-0001/scenario.yaml")
	if exitCode != 0 {
		t.Fatalf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
	}

	if !strings.Contains(stdout, "empties") {
		t.Errorf("explain output does not name 'empties':\n%s", stdout)
	}
	// History entry [0] should match for toy=2.0.0.
	if !strings.Contains(stdout, "[0]") {
		t.Errorf("explain output does not name history entry index:\n%s", stdout)
	}
}

func TestInitProducesValidProject(t *testing.T) {
	tmp := t.TempDir()

	_, stderr, exitCode := runScraps(t, tmp, "init")
	if exitCode != 0 {
		t.Fatalf("init exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
	}

	// init should create scraps.yaml and at least one scenario file.
	if _, err := os.Stat(filepath.Join(tmp, "scraps.yaml")); os.IsNotExist(err) {
		t.Fatal("scraps.yaml not created by init")
	}
	scenarioPath := filepath.Join(tmp, "scenarios", "example", "scenario.yaml")
	if _, err := os.Stat(scenarioPath); os.IsNotExist(err) {
		t.Fatal("scenario.yaml not created by init")
	}
}

func TestLockExits0(t *testing.T) {
	// Both test projects use only local actor sources, so lock produces
	// "0 sources locked" and exits 0.
	t.Run("testdata/e2e", func(t *testing.T) {
		tmp := copyE2EProject(t)
		_, stderr, exitCode := runScraps(t, tmp, "lock")
		if exitCode != 0 {
			t.Errorf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}
	})

	t.Run("examples/collab", func(t *testing.T) {
		tmp := copyCollabProject(t)
		_, stderr, exitCode := runScraps(t, tmp, "lock")
		if exitCode != 0 {
			t.Errorf("exit code: got %d, want 0\nstderr: %s", exitCode, stderr)
		}
	})
}

func init() {
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		fmt.Fprintf(os.Stderr, "warning: e2e tests may not work on %s\n", runtime.GOOS)
	}
}
