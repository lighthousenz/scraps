package report_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/lighthousenz/scraps/internal/model"
	"github.com/lighthousenz/scraps/internal/report"
)

var update = flag.Bool("update", false, "update golden files")

// testReport builds a RunReport with one result for each verdict,
// including broken with a reason and unrecognised with overlapping outcomes,
// and a result with no links.
func testReport() *model.RunReport {
	return &model.RunReport{
		Schema:   1,
		Scraps:   "1.4.0",
		Seed:     81723,
		Subjects: map[string]string{"docengine": "2.31.0"},
		Results: []model.ScenarioResult{
			{
				ID:    "DOC-0017",
				Title: "Table cell merge loses borders",
				Links: map[string]model.LinkResult{
					"ticket": {Value: "ENG-3901", URL: "https://linear.app/acme/issue/ENG-3901"},
				},
				Tags:           []string{"tables"},
				Expected:       "correct",
				Actual:         "correct",
				Verdict:        model.VerdictAsExpected,
				MatchedHistory: 0,
				DurationMs:     800,
				Artefacts:      ".scraps/runs/2026-11-02T09-14-03Z/DOC-0017/",
			},
			{
				ID:    "DOC-0042",
				Title: "Concurrent inserts drop a paragraph",
				Links: map[string]model.LinkResult{
					"ticket":   {Value: "ENG-3901", URL: "https://linear.app/acme/issue/ENG-3901"},
					"upstream": {Value: "acme/docengine#1234", URL: "https://github.com/acme/docengine/issues/1234"},
				},
				Tags:           []string{"collab", "lists"},
				Expected:       "drops-one",
				Actual:         "drops-one",
				Verdict:        model.VerdictAsExpected,
				MatchedHistory: 1,
				DurationMs:     1912,
				Artefacts:      ".scraps/runs/2026-11-02T09-14-03Z/DOC-0042/",
			},
			{
				ID:             "DOC-0051",
				Title:          "Tracked change survives reject-all",
				Links:          map[string]model.LinkResult{},
				Expected:       "correct",
				Actual:         "keeps-insertion",
				Verdict:        model.VerdictRegressed,
				MatchedHistory: -1,
				DurationMs:     1200,
				Artefacts:      ".scraps/runs/2026-11-02T09-14-03Z/DOC-0051/",
			},
			{
				ID:             "DOC-0060",
				Title:          "Fixed bug",
				Expected:       "drops-one",
				Actual:         "correct",
				Verdict:        model.VerdictFixed,
				MatchedHistory: 0,
				DurationMs:     500,
				Artefacts:      ".scraps/runs/2026-11-02T09-14-03Z/DOC-0060/",
			},
			{
				ID:             "DOC-0070",
				Title:          "Changed failure mode",
				Expected:       "drops-one",
				Actual:         "duplicates",
				Verdict:        model.VerdictChanged,
				MatchedHistory: 0,
				DurationMs:     600,
				Artefacts:      ".scraps/runs/2026-11-02T09-14-03Z/DOC-0070/",
			},
			{
				ID:          "DOC-0080",
				Title:       "Overlapping outcomes",
				Expected:    "correct",
				Actual:      "",
				Verdict:     model.VerdictUnrecognised,
				Overlapping: []string{"correct", "shouts"},
				DurationMs:  700,
				Artefacts:   ".scraps/runs/2026-11-02T09-14-03Z/DOC-0080/",
			},
			{
				ID:         "DOC-0090",
				Title:      "Actor crashed",
				Expected:   "correct",
				Actual:     "",
				Verdict:    model.VerdictBroken,
				Reason:     "actor alice: protocol violation: non-JSON on stdout",
				DurationMs: 300,
				Artefacts:  ".scraps/runs/2026-11-02T09-14-03Z/DOC-0090/",
			},
		},
	}
}

// testScenario builds a scenario with group steps, a cascading stop, and an await.
// This matches the README's diagram example.
func testScenario() *model.Scenario {
	return &model.Scenario{
		ID:    "DOC-0042",
		Title: "Concurrent inserts at document start drop a paragraph",
		Actors: map[string]model.ScenarioActor{
			"server": {Uses: "collab-server"},
			"alice":  {Uses: "editor"},
			"bob":    {Uses: "editor"},
			"carol":  {Uses: "editor", Start: "on-demand"},
		},
		Steps: []model.Step{
			{Targets: []string{"alice", "bob"}, Operation: "pause-sync"},
			{Targets: []string{"alice"}, Operation: "insert-paragraph", Params: map[string]any{"at": "start", "text": "alpha"}},
			{Targets: []string{"bob"}, Operation: "insert-paragraph", Params: map[string]any{"at": "start", "text": "beta"}},
			{Targets: []string{"alice", "bob"}, Operation: "resume-sync"},
			{Targets: []string{"alice", "bob"}, Operation: "await", Params: map[string]any{"condition": "converged"}},
			{Targets: []string{"server"}, Operation: "stop"},
			{Targets: []string{"server"}, Operation: "start"},
			{Targets: []string{"carol"}, Operation: "start"},
			{Targets: []string{"carol"}, Operation: "snapshot", Params: map[string]any{"as": "final"}},
		},
	}
}

func goldenTest(t *testing.T, name string, generate func(buf *bytes.Buffer)) {
	t.Helper()

	var buf bytes.Buffer
	generate(&buf)
	got := buf.Bytes()

	golden := filepath.Join("testdata", name)

	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file %s not found; run with -update to create it: %v", golden, err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("output differs from golden file %s.\n\nGot:\n%s\n\nWant:\n%s", golden, got, want)
	}
}

func TestJSON(t *testing.T) {
	goldenTest(t, "report.json", func(buf *bytes.Buffer) {
		if err := report.JSON(buf, testReport()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestJUnit(t *testing.T) {
	failOn := map[model.Verdict]bool{
		model.VerdictRegressed:    true,
		model.VerdictChanged:      true,
		model.VerdictUnrecognised: true,
		model.VerdictBroken:       true,
		model.VerdictFixed:        true,
	}
	goldenTest(t, "report.xml", func(buf *bytes.Buffer) {
		if err := report.JUnit(buf, testReport(), failOn); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMarkdown(t *testing.T) {
	goldenTest(t, "report.md", func(buf *bytes.Buffer) {
		if err := report.Markdown(buf, testReport()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTerminal(t *testing.T) {
	failOn := map[model.Verdict]bool{
		model.VerdictRegressed:    true,
		model.VerdictChanged:      true,
		model.VerdictUnrecognised: true,
		model.VerdictBroken:       true,
		model.VerdictFixed:        true,
	}
	goldenTest(t, "report.terminal", func(buf *bytes.Buffer) {
		// No colour for golden test.
		if err := report.Terminal(buf, testReport(), failOn, report.TerminalOpts{Color: false}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestTerminalColor(t *testing.T) {
	failOn := map[model.Verdict]bool{
		model.VerdictRegressed: true,
		model.VerdictBroken:    true,
	}
	goldenTest(t, "report.terminal.color", func(buf *bytes.Buffer) {
		if err := report.Terminal(buf, testReport(), failOn, report.TerminalOpts{Color: true}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMermaid(t *testing.T) {
	scenario := testScenario()
	opts := report.MermaidOpts{
		// Declaration order from the README's diagram example.
		ActorOrder: []string{"server", "alice", "bob", "carol"},
		CascadeOrders: map[int][]string{
			// The stop at step index 5 cascades: alice, bob, then server.
			5: {"alice", "bob", "server"},
		},
	}
	goldenTest(t, "diagram.mermaid", func(buf *bytes.Buffer) {
		if err := report.Mermaid(buf, scenario, opts); err != nil {
			t.Fatal(err)
		}
	})
}
