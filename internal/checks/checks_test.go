package checks_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lighthousenz/scraps/internal/checks"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file path")
	}
	return filepath.Join(filepath.Dir(file), "testdata", "artefacts")
}

func TestFileExists(t *testing.T) {
	dir := testdataDir(t)
	art := checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}}

	tests := []struct {
		name      string
		params    any
		wantMatch bool
		wantErr   bool
	}{
		{
			name:      "existing file matches",
			params:    "snapshot.json",
			wantMatch: true,
		},
		{
			name:      "missing file does not match",
			params:    "nonexistent.json",
			wantMatch: false,
		},
		{
			name:    "bad params type is error",
			params:  42,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := checks.Run("file-exists", art, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Match != tt.wantMatch {
				t.Errorf("match: got %v, want %v (reason: %s)", result.Match, tt.wantMatch, result.Reason)
			}
		})
	}
}

func TestJSON(t *testing.T) {
	dir := testdataDir(t)

	tests := []struct {
		name      string
		art       checks.Artefact
		params    any
		wantMatch bool
		wantErr   bool
	}{
		{
			name: "path to key equals value",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.count",
				"equals": float64(2),
			},
			wantMatch: true,
		},
		{
			name: "path to key does not equal value",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.count",
				"equals": float64(3),
			},
			wantMatch: false,
		},
		{
			name: "path to nested key",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.nested.key",
				"equals": "value",
			},
			wantMatch: true,
		},
		{
			name: "path to array index",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.paragraphs[0]",
				"equals": "alpha",
			},
			wantMatch: true,
		},
		{
			name: "path to array length",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.paragraphs.length",
				"equals": float64(2),
			},
			wantMatch: true,
		},
		{
			name: "explicit file param",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json", "other.json"}},
			params: map[string]any{
				"file":   "snapshot.json",
				"path":   "$.count",
				"equals": float64(2),
			},
			wantMatch: true,
		},
		{
			name: "int equals float64 (YAML int vs JSON float64)",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.count",
				"equals": 2,
			},
			wantMatch: true,
		},
		{
			name: "missing file is no match not error",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"nonexistent.json"}},
			params: map[string]any{
				"path":   "$.count",
				"equals": float64(2),
			},
			wantMatch: false,
		},
		{
			name: "malformed JSON is no match not error",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"malformed.json"}},
			params: map[string]any{
				"path":   "$.count",
				"equals": float64(2),
			},
			wantMatch: false,
		},
		{
			name: "error: ambiguous json files without file param",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json", "other.json"}},
			params: map[string]any{
				"path":   "$.count",
				"equals": float64(2),
			},
			wantErr: true,
		},
		{
			name: "error: no json files without file param",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"output.txt"}},
			params: map[string]any{
				"path":   "$.count",
				"equals": float64(2),
			},
			wantErr: true,
		},
		{
			name:    "error: bad params type",
			art:     checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params:  "not a map",
			wantErr: true,
		},
		{
			name: "error: missing path param",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"equals": float64(2),
			},
			wantErr: true,
		},
		{
			name: "error: missing equals param",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path": "$.count",
			},
			wantErr: true,
		},
		{
			name: "error: bad path syntax",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "count",
				"equals": float64(2),
			},
			wantErr: true,
		},
		{
			name: "path key not found is no match",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.nonexistent",
				"equals": float64(2),
			},
			wantMatch: false,
		},
		{
			name: "array index out of range is no match",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.paragraphs[99]",
				"equals": "alpha",
			},
			wantMatch: false,
		},
		{
			name: "string length",
			art:  checks.Artefact{Name: "test", Dir: dir, Files: []string{"snapshot.json"}},
			params: map[string]any{
				"path":   "$.paragraphs[0].length",
				"equals": float64(5), // "alpha" has length 5
			},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := checks.Run("json", tt.art, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got result: match=%v reason=%q", result.Match, result.Reason)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Match != tt.wantMatch {
				t.Errorf("match: got %v, want %v (reason: %s)", result.Match, tt.wantMatch, result.Reason)
			}
		})
	}
}

func TestText(t *testing.T) {
	dir := testdataDir(t)
	art := checks.Artefact{Name: "test", Dir: dir, Files: []string{"output.txt"}}

	tests := []struct {
		name      string
		params    any
		wantMatch bool
		wantErr   bool
	}{
		{
			name: "regex matches",
			params: map[string]any{
				"file":    "output.txt",
				"matches": "^alpha",
			},
			wantMatch: true,
		},
		{
			name: "regex does not match",
			params: map[string]any{
				"file":    "output.txt",
				"matches": "^gamma",
			},
			wantMatch: false,
		},
		{
			name: "regex matches multiline content",
			params: map[string]any{
				"file":    "output.txt",
				"matches": "beta paragraph",
			},
			wantMatch: true,
		},
		{
			name: "missing file is no match not error",
			params: map[string]any{
				"file":    "nonexistent.txt",
				"matches": "anything",
			},
			wantMatch: false,
		},
		{
			name:    "bad params type is error",
			params:  "not a map",
			wantErr: true,
		},
		{
			name: "missing file param is error",
			params: map[string]any{
				"matches": "anything",
			},
			wantErr: true,
		},
		{
			name: "missing matches param is error",
			params: map[string]any{
				"file": "output.txt",
			},
			wantErr: true,
		},
		{
			name: "invalid regex is error",
			params: map[string]any{
				"file":    "output.txt",
				"matches": "[invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := checks.Run("text", art, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Match != tt.wantMatch {
				t.Errorf("match: got %v, want %v (reason: %s)", result.Match, tt.wantMatch, result.Reason)
			}
		})
	}
}

func TestUnknownCheck(t *testing.T) {
	art := checks.Artefact{Name: "test", Dir: t.TempDir(), Files: nil}
	_, err := checks.Run("nonexistent-check", art, nil)
	if err == nil {
		t.Fatal("expected error for unknown check")
	}
}

func TestFileExistsInTempDir(t *testing.T) {
	// Test with a dynamically created temporary directory.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	art := checks.Artefact{Name: "test", Dir: dir, Files: []string{"hello.txt"}}

	result, err := checks.Run("file-exists", art, "hello.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Match {
		t.Errorf("expected match for existing file")
	}

	result, err = checks.Run("file-exists", art, "missing.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Match {
		t.Errorf("expected no match for missing file")
	}
}

func TestJSONWithDynamicFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"text":"hello world"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	art := checks.Artefact{Name: "test", Dir: dir, Files: []string{"data.json"}}

	// Single json file auto-resolved.
	result, err := checks.Run("json", art, map[string]any{
		"path":   "$.text",
		"equals": "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Match {
		t.Errorf("expected match, got: %s", result.Reason)
	}
}

func TestJSONDeepEqualArrays(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"items":["a","b","c"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	art := checks.Artefact{Name: "test", Dir: dir, Files: []string{"data.json"}}

	result, err := checks.Run("json", art, map[string]any{
		"path":   "$.items",
		"equals": []any{"a", "b", "c"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Match {
		t.Errorf("expected match for array deep-equals, got: %s", result.Reason)
	}

	result, err = checks.Run("json", art, map[string]any{
		"path":   "$.items",
		"equals": []any{"a", "b"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Match {
		t.Errorf("expected no match for different-length array")
	}
}

func TestJSONDeepEqualObjects(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"obj":{"x":1,"y":"z"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	art := checks.Artefact{Name: "test", Dir: dir, Files: []string{"data.json"}}

	result, err := checks.Run("json", art, map[string]any{
		"path":   "$.obj",
		"equals": map[string]any{"x": float64(1), "y": "z"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Match {
		t.Errorf("expected match for object deep-equals, got: %s", result.Reason)
	}
}

func TestJSONBooleanEquality(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"flag":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	art := checks.Artefact{Name: "test", Dir: dir, Files: []string{"data.json"}}

	result, err := checks.Run("json", art, map[string]any{
		"path":   "$.flag",
		"equals": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Match {
		t.Errorf("expected match for boolean, got: %s", result.Reason)
	}
}

func TestJSONNullEquality(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"val":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	art := checks.Artefact{Name: "test", Dir: dir, Files: []string{"data.json"}}

	result, err := checks.Run("json", art, map[string]any{
		"path":   "$.val",
		"equals": nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Match {
		t.Errorf("expected match for null, got: %s", result.Reason)
	}
}
