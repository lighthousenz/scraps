package schema

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/lighthousenz/scraps/internal/config"
)

var update = flag.Bool("update", false, "update golden files")

// testdataDir returns the absolute path to the testdata directory.
// Call this before any chdir.
func testdataDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func loadTestProject(t *testing.T) *config.LoadedProject {
	t.Helper()

	projectDir, err := filepath.Abs(filepath.Join("testdata", "project"))
	if err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
	})

	lp, problems := config.LoadProject(nil)
	if lp == nil {
		for _, p := range problems {
			t.Log(p)
		}
		t.Fatal("failed to load test project")
	}
	return lp
}

func TestGoldenSchema(t *testing.T) {
	td := testdataDir(t)
	lp := loadTestProject(t)

	data, err := Generate(lp)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join(td, "golden.json")
	if *update {
		if err := os.WriteFile(golden, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("updated golden file")
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden file not found; run with -update to create it: %s", err)
	}

	if diff := cmp.Diff(string(want), string(data)); diff != "" {
		t.Errorf("schema mismatch (-want +got):\n%s", diff)
	}
}

func compileSchema(t *testing.T, schemaData []byte) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(schemaData)))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiler.AddResource(".scraps/schema.json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := compiler.Compile(".scraps/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return sch
}

func TestSchemaValidatesValidScenario(t *testing.T) {
	lp := loadTestProject(t)

	schemaData, err := Generate(lp)
	if err != nil {
		t.Fatal(err)
	}

	sch := compileSchema(t, schemaData)

	// CWD is testdata/project after loadTestProject.
	scenarioData, err := os.ReadFile(filepath.Join("scenarios", "TEST-0001", "scenario.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	jsonData, err := yamlToJSON(scenarioData)
	if err != nil {
		t.Fatal(err)
	}

	var doc any
	if err := json.Unmarshal(jsonData, &doc); err != nil {
		t.Fatal(err)
	}

	if err := sch.Validate(doc); err != nil {
		t.Errorf("valid scenario failed validation: %s", err)
	}
}

func TestSchemaRejectsBrokenScenarios(t *testing.T) {
	td := testdataDir(t)
	lp := loadTestProject(t)

	schemaData, err := Generate(lp)
	if err != nil {
		t.Fatal(err)
	}

	sch := compileSchema(t, schemaData)

	brokenFiles, err := filepath.Glob(filepath.Join(td, "broken", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(brokenFiles) == 0 {
		t.Fatal("no broken test files found")
	}

	for _, f := range brokenFiles {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			jsonData, err := yamlToJSON(data)
			if err != nil {
				t.Logf("YAML parse error (expected): %s", err)
				return
			}

			var doc any
			if err := json.Unmarshal(jsonData, &doc); err != nil {
				t.Logf("JSON parse error (expected): %s", err)
				return
			}

			if err := sch.Validate(doc); err == nil {
				t.Errorf("expected validation to fail for %s", name)
			} else {
				t.Logf("validation failed as expected: %s", truncate(err.Error(), 200))
			}
		})
	}
}

func TestSchemaValidatesREADMEExample(t *testing.T) {
	lp := loadTestProject(t)

	schemaData, err := Generate(lp)
	if err != nil {
		t.Fatal(err)
	}

	sch := compileSchema(t, schemaData)

	readmeScenario := `{
		"id": "TEST-README",
		"title": "README example adapted",
		"links": { "ticket": "PROJ-42" },
		"tags": ["test"],
		"actors": {
			"s": { "uses": "server" },
			"c1": { "uses": "client", "with": { "server": "s" } },
			"c2": { "uses": "client", "with": { "server": "s" } }
		},
		"steps": [
			{ "c1": "disconnect" },
			{ "c1": { "write": { "text": "hello" } } },
			{ "s": "stop" },
			{ "s": "start" }
		],
		"outcomes": {
			"correct": { "final": { "has-text": ["hello"] } }
		},
		"history": [
			{ "when": { "mylib": ">=2.0.0" }, "expect": "correct" }
		]
	}`

	var doc any
	if err := json.Unmarshal([]byte(readmeScenario), &doc); err != nil {
		t.Fatal(err)
	}

	if err := sch.Validate(doc); err != nil {
		t.Errorf("README-style scenario failed validation: %s", err)
	}
}

func yamlToJSON(yamlData []byte) ([]byte, error) {
	var raw any
	if err := yaml.Unmarshal(yamlData, &raw); err != nil {
		return nil, err
	}
	return json.Marshal(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
