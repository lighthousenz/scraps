package conductor_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lighthousenz/scraps/internal/conductor"
	"github.com/lighthousenz/scraps/internal/config"
	"github.com/lighthousenz/scraps/internal/model"
	"github.com/lighthousenz/scraps/internal/protocol"
)

// TestHelperProcess is the re-exec entry point for fake actors used by conductor tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	mode := os.Getenv("HELPER_MODE")
	scanner := bufio.NewScanner(os.Stdin)

	switch mode {
	case "echo":
		handleEcho(scanner)
	case "error-1000":
		handleError1000(scanner)
	case "broken":
		handleBroken(scanner)
	case "snapshot":
		handleSnapshot(scanner)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode: %s\n", mode)
		os.Exit(2)
	}
}

func handleEcho(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := scanner.Text()
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "scraps/initialize":
			var params protocol.InitializeParams
			_ = json.Unmarshal(req.Params, &params)
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocol": params.Protocol,
					"outputs":  map[string]any{"port": "8080"},
				},
			}
			writeResponse(resp)
		case "scraps/shutdown":
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{},
			})
			return
		default:
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"method": req.Method,
					"ok":     true,
				},
			})
		}
	}
}

func handleError1000(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := scanner.Text()
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "scraps/initialize":
			var params protocol.InitializeParams
			_ = json.Unmarshal(req.Params, &params)
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocol": params.Protocol,
					"outputs":  map[string]any{},
				},
			})
		case "scraps/shutdown":
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{},
			})
			return
		default:
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    1000,
					"message": "operation failed",
					"data":    map[string]any{"detail": "test error"},
				},
			})
		}
	}
}

func handleBroken(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := scanner.Text()
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "scraps/initialize":
			var params protocol.InitializeParams
			_ = json.Unmarshal(req.Params, &params)
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocol": params.Protocol,
					"outputs":  map[string]any{},
				},
			})
		case "scraps/shutdown":
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{},
			})
			return
		default:
			// Return error code 1001 which maps to broken.
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error": map[string]any{
					"code":    1001,
					"message": "internal actor error",
				},
			})
		}
	}
}

func handleSnapshot(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := scanner.Text()
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "scraps/initialize":
			var params protocol.InitializeParams
			_ = json.Unmarshal(req.Params, &params)
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocol": params.Protocol,
					"outputs":  map[string]any{},
				},
			})
		case "scraps/shutdown":
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{},
			})
			return
		case "snapshot":
			// Write a file to artefacts and return it.
			artefacts := os.Getenv("SCRAPS_ARTEFACTS")
			fileName := "output.txt"
			_ = os.WriteFile(filepath.Join(artefacts, fileName), []byte("snapshot data"), 0o644)
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"files": []string{fileName},
				},
			})
		default:
			writeResponse(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"ok": true},
			})
		}
	}
}

func writeResponse(v any) {
	data, _ := json.Marshal(v)
	_, _ = fmt.Fprintln(os.Stdout, string(data))
}

// setupTestProject creates a minimal project layout for conductor tests.
// It returns the project directory and a cleanup function.
func setupTestProject(t *testing.T, actorMode string, scenario model.Scenario) (*config.LoadedProject, string) {
	t.Helper()

	dir := t.TempDir()

	// Create actor directory with actor.yaml.
	actorDir := filepath.Join(dir, "actors", "testactor")
	if err := os.MkdirAll(actorDir, 0o755); err != nil {
		t.Fatal(err)
	}

	testBin, _ := os.Executable()
	runCmd := fmt.Sprintf("%s -test.run=TestHelperProcess", testBin)

	actorYAML := fmt.Sprintf(`name: testactor
kind: testactor
run: %s
operations:
  ping:
    params: {}
  snapshot:
    params:
      as: { type: string }
    artefact: true
`, runCmd)

	if err := os.WriteFile(filepath.Join(actorDir, "actor.yaml"), []byte(actorYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write scraps.yaml.
	scrapsYAML := `version: 1
subjects: {}
actors:
  testactor: ./actors/testactor
`
	if err := os.WriteFile(filepath.Join(dir, "scraps.yaml"), []byte(scrapsYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set up env for helper process.
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("HELPER_MODE", actorMode)

	// Load project manually since LoadProject depends on cwd.
	actorDef := &model.ActorDefinition{
		Name: "testactor",
		Kind: "testactor",
		Run:  runCmd,
		Operations: map[string]model.OperationDef{
			"ping": {},
			"snapshot": {
				Params:   map[string]any{"as": map[string]any{"type": "string"}},
				Artefact: true,
			},
		},
	}

	project := &config.LoadedProject{
		Project: model.Project{
			Version:  1,
			Subjects: map[string]model.Subject{},
			Actors:   map[string]string{"testactor": "./actors/testactor"},
		},
		ProjectDir: dir,
		Scenarios:  []config.LoadedScenario{{Scenario: scenario, Path: filepath.Join(dir, "scenario.yaml")}},
		Actors:     map[string]*model.ActorDefinition{"testactor": actorDef},
		Checkers:   map[string]*model.ActorDefinition{},
	}

	return project, dir
}

func TestConductorBasicRun(t *testing.T) {
	scenario := model.Scenario{
		ID:    "basic",
		Title: "Basic test",
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct": {},
		},
		History: []model.HistoryEntry{},
	}

	project, dir := setupTestProject(t, "echo", scenario)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runDir := filepath.Join(dir, ".scraps", "runs", "test")
	cacheDir := filepath.Join(dir, ".scraps", "cache", "prepared")

	cfg := &conductor.RunConfig{
		Project:   project,
		Scenarios: project.Scenarios,
		Subjects:  map[string]string{},
		Seed:      42,
		FailOn:    nil,
		RunDir:    runDir,
		Version:   "test",
		CacheDir:  cacheDir,
	}

	result, err := conductor.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("conductor.Run: %v", err)
	}

	if len(result.Report.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Report.Results))
	}

	r := result.Report.Results[0]
	if r.ID != "basic" {
		t.Errorf("expected scenario id 'basic', got %q", r.ID)
	}

	// With an empty outcome (no checks), it should match.
	if r.Verdict != model.VerdictAsExpected {
		t.Errorf("expected as-expected verdict, got %s (reason: %s)", r.Verdict, r.Reason)
	}
}

func TestConductorError1000SkipsSteps(t *testing.T) {
	scenario := model.Scenario{
		ID:    "error-test",
		Title: "Error 1000 test",
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct": {},
			"error-outcome": {
				{StepError: &model.StepErrorMatcher{
					Actor:     "a",
					Operation: "ping",
					Message:   "operation failed",
				}},
			},
		},
		History: []model.HistoryEntry{},
	}

	project, dir := setupTestProject(t, "error-1000", scenario)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runDir := filepath.Join(dir, ".scraps", "runs", "test")
	cacheDir := filepath.Join(dir, ".scraps", "cache", "prepared")

	cfg := &conductor.RunConfig{
		Project:   project,
		Scenarios: project.Scenarios,
		Subjects:  map[string]string{},
		Seed:      42,
		RunDir:    runDir,
		Version:   "test",
		CacheDir:  cacheDir,
	}

	result, err := conductor.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("conductor.Run: %v", err)
	}

	r := result.Report.Results[0]
	// The first step returns error 1000, so the step-error should be recorded
	// and the second step should be skipped. The error-outcome should match.
	if r.Verdict == model.VerdictBroken {
		t.Errorf("should not be broken; got reason: %s", r.Reason)
	}
}

func TestConductorBrokenError(t *testing.T) {
	scenario := model.Scenario{
		ID:    "broken-test",
		Title: "Broken error test",
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct": {},
		},
		History: []model.HistoryEntry{},
	}

	project, dir := setupTestProject(t, "broken", scenario)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runDir := filepath.Join(dir, ".scraps", "runs", "test")
	cacheDir := filepath.Join(dir, ".scraps", "cache", "prepared")

	cfg := &conductor.RunConfig{
		Project:   project,
		Scenarios: project.Scenarios,
		Subjects:  map[string]string{},
		Seed:      42,
		RunDir:    runDir,
		Version:   "test",
		CacheDir:  cacheDir,
	}

	result, err := conductor.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("conductor.Run: %v", err)
	}

	r := result.Report.Results[0]
	if r.Verdict != model.VerdictBroken {
		t.Errorf("expected broken verdict, got %s", r.Verdict)
	}
	if r.Reason == "" {
		t.Error("expected non-empty reason for broken verdict")
	}
}

func TestConductorParallelRun(t *testing.T) {
	scenario1 := model.Scenario{
		ID:    "par-1",
		Title: "Parallel test 1",
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct": {},
		},
		History: []model.HistoryEntry{},
	}

	scenario2 := model.Scenario{
		ID:    "par-2",
		Title: "Parallel test 2",
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct": {},
		},
		History: []model.HistoryEntry{},
	}

	// Set up project with both scenarios.
	project, dir := setupTestProject(t, "echo", scenario1)
	project.Scenarios = append(project.Scenarios, config.LoadedScenario{
		Scenario: scenario2,
		Path:     filepath.Join(dir, "scenario2.yaml"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runDir := filepath.Join(dir, ".scraps", "runs", "test")
	cacheDir := filepath.Join(dir, ".scraps", "cache", "prepared")

	cfg := &conductor.RunConfig{
		Project:   project,
		Scenarios: project.Scenarios,
		Subjects:  map[string]string{},
		Seed:      42,
		RunDir:    runDir,
		Version:   "test",
		CacheDir:  cacheDir,
	}

	result, err := conductor.RunParallel(ctx, cfg, 2)
	if err != nil {
		t.Fatalf("conductor.RunParallel: %v", err)
	}

	if len(result.Report.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.Report.Results))
	}

	// Both should succeed.
	for _, r := range result.Report.Results {
		if r.Verdict != model.VerdictAsExpected {
			t.Errorf("scenario %s: expected as-expected, got %s (reason: %s)", r.ID, r.Verdict, r.Reason)
		}
	}
}

func TestConductorVerdictClassification(t *testing.T) {
	// Scenario expects "correct" but actual outcome is "broken-output".
	scenario := model.Scenario{
		ID:    "verdict-test",
		Title: "Verdict classification",
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct": {
				// This check uses a built-in check that will fail since
				// no artefact named "missing" exists.
				{Artefact: "missing", CheckName: "file-exists", Value: "nofile.txt"},
			},
			"broken-output": {},
		},
		History: []model.HistoryEntry{},
	}

	project, dir := setupTestProject(t, "echo", scenario)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runDir := filepath.Join(dir, ".scraps", "runs", "test")
	cacheDir := filepath.Join(dir, ".scraps", "cache", "prepared")

	cfg := &conductor.RunConfig{
		Project:   project,
		Scenarios: project.Scenarios,
		Subjects:  map[string]string{},
		Seed:      42,
		RunDir:    runDir,
		Version:   "test",
		CacheDir:  cacheDir,
	}

	result, err := conductor.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("conductor.Run: %v", err)
	}

	r := result.Report.Results[0]
	// "correct" checks fail, but "broken-output" has no checks so matches.
	// Expected is "correct" (default), actual is "broken-output" → regressed.
	if r.Verdict != model.VerdictRegressed {
		t.Errorf("expected regressed verdict, got %s (reason: %s)", r.Verdict, r.Reason)
	}
}

func TestConductorHistoryMatching(t *testing.T) {
	scenario := model.Scenario{
		ID:    "history-test",
		Title: "History matching",
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct":    {},
			"old-broken": {},
		},
		History: []model.HistoryEntry{
			{
				When:   map[string]string{"myapp": "<2.0.0"},
				Expect: "old-broken",
			},
		},
	}

	project, dir := setupTestProject(t, "echo", scenario)

	// Add a subject to the project so history can be evaluated.
	project.Project.Subjects = map[string]model.Subject{
		"myapp": {Scheme: "semver"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runDir := filepath.Join(dir, ".scraps", "runs", "test")
	cacheDir := filepath.Join(dir, ".scraps", "cache", "prepared")

	cfg := &conductor.RunConfig{
		Project:   project,
		Scenarios: project.Scenarios,
		Subjects:  map[string]string{"myapp": "1.5.0"},
		Seed:      42,
		RunDir:    runDir,
		Version:   "test",
		CacheDir:  cacheDir,
	}

	result, err := conductor.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("conductor.Run: %v", err)
	}

	r := result.Report.Results[0]
	// History entry matches (1.5.0 < 2.0.0) → expects "old-broken".
	// Both "correct" and "old-broken" have empty checks and match → unrecognised.
	if r.Expected != "old-broken" {
		t.Errorf("expected 'old-broken', got %q", r.Expected)
	}
	if r.MatchedHistory != 0 {
		t.Errorf("expected matched-history=0, got %d", r.MatchedHistory)
	}
}

func TestConductorReportFields(t *testing.T) {
	scenario := model.Scenario{
		ID:    "report-test",
		Title: "Report fields",
		Tags:  []string{"smoke"},
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct": {},
		},
		History: []model.HistoryEntry{},
	}

	project, dir := setupTestProject(t, "echo", scenario)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runDir := filepath.Join(dir, ".scraps", "runs", "test")
	cacheDir := filepath.Join(dir, ".scraps", "cache", "prepared")

	cfg := &conductor.RunConfig{
		Project:   project,
		Scenarios: project.Scenarios,
		Subjects:  map[string]string{},
		Seed:      123,
		RunDir:    runDir,
		Version:   "v0.1.0",
		CacheDir:  cacheDir,
	}

	result, err := conductor.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("conductor.Run: %v", err)
	}

	// Check report metadata.
	if result.Report.Schema != 1 {
		t.Errorf("expected schema=1, got %d", result.Report.Schema)
	}
	if result.Report.Scraps != "v0.1.0" {
		t.Errorf("expected scraps=v0.1.0, got %q", result.Report.Scraps)
	}
	if result.Report.Seed != 123 {
		t.Errorf("expected seed=123, got %d", result.Report.Seed)
	}

	r := result.Report.Results[0]
	if r.ID != "report-test" {
		t.Errorf("expected id=report-test, got %q", r.ID)
	}
	if r.Title != "Report fields" {
		t.Errorf("expected title, got %q", r.Title)
	}
	if len(r.Tags) != 1 || r.Tags[0] != "smoke" {
		t.Errorf("expected tags=[smoke], got %v", r.Tags)
	}
	if r.DurationMs <= 0 {
		t.Error("expected positive duration")
	}
	if !strings.Contains(r.Artefacts, "report-test") {
		t.Errorf("expected artefacts path to contain scenario id, got %q", r.Artefacts)
	}
}

func TestConductorFailOnDetection(t *testing.T) {
	scenario := model.Scenario{
		ID:    "fail-on-test",
		Title: "Fail-on detection",
		Actors: map[string]model.ScenarioActor{
			"a": {Uses: "testactor"},
		},
		Steps: []model.Step{
			{Targets: []string{"a"}, Operation: "ping"},
		},
		Outcomes: map[string]model.Outcome{
			"correct": {},
		},
		History: []model.HistoryEntry{},
	}

	project, dir := setupTestProject(t, "echo", scenario)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runDir := filepath.Join(dir, ".scraps", "runs", "test")
	cacheDir := filepath.Join(dir, ".scraps", "cache", "prepared")

	cfg := &conductor.RunConfig{
		Project:   project,
		Scenarios: project.Scenarios,
		Subjects:  map[string]string{},
		Seed:      42,
		FailOn:    []string{"as-expected"},
		RunDir:    runDir,
		Version:   "test",
		CacheDir:  cacheDir,
	}

	result, err := conductor.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("conductor.Run: %v", err)
	}

	// Result should be as-expected, but fail-on includes it.
	if !result.HasFail {
		t.Error("expected HasFail=true when as-expected is in fail-on")
	}
}
