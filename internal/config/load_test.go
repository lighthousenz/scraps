package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectFile(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "scenarios", "test")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectFile := filepath.Join(dir, "scraps.yaml")
	if err := os.WriteFile(projectFile, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("from project root", func(t *testing.T) {
		found, err := FindProjectFile(dir)
		if err != nil {
			t.Fatal(err)
		}
		if found != projectFile {
			t.Errorf("FindProjectFile(%q) = %q, want %q", dir, found, projectFile)
		}
	})

	t.Run("from subdirectory", func(t *testing.T) {
		found, err := FindProjectFile(subDir)
		if err != nil {
			t.Fatal(err)
		}
		if found != projectFile {
			t.Errorf("FindProjectFile(%q) = %q, want %q", subDir, found, projectFile)
		}
	})

	t.Run("not found", func(t *testing.T) {
		noScraps := t.TempDir()
		_, err := FindProjectFile(noScraps)
		if err == nil {
			t.Fatal("expected error when scraps.yaml not found")
		}
	})
}

func TestStrictDecode_UnknownField(t *testing.T) {
	yamlContent := `version: 1
unknown_field: bad
`
	dir := t.TempDir()
	projectFile := filepath.Join(dir, "scraps.yaml")
	if err := os.WriteFile(projectFile, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, probs := loadProjectFile(projectFile)
	if len(probs) == 0 {
		t.Error("expected strict decode to reject unknown fields")
	}
}

func TestLoadProjectFile_Valid(t *testing.T) {
	dir := t.TempDir()
	projectFile := filepath.Join(dir, "scraps.yaml")
	content := `version: 1
subjects:
  docengine:
    scheme: semver
    resolve: { env: DOCENGINE_VERSION }
`
	if err := os.WriteFile(projectFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, probs := loadProjectFile(projectFile)
	if len(probs) > 0 {
		t.Errorf("unexpected problems: %v", probs)
	}
	if proj == nil {
		t.Fatal("expected project to be loaded")
	}
	if proj.Version != 1 {
		t.Errorf("version = %d, want 1", proj.Version)
	}
	if _, ok := proj.Subjects["docengine"]; !ok {
		t.Error("expected docengine subject")
	}
}

func TestLoadActorFile(t *testing.T) {
	dir := t.TempDir()
	actorFile := filepath.Join(dir, "actor.yaml")
	content := `name: test-actor
protocol: 1
run: node main.mjs
operations:
  do-thing: { params: { x: { type: string } } }
`
	if err := os.WriteFile(actorFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	def, probs := loadActorFile(actorFile, "")
	if len(probs) > 0 {
		t.Errorf("unexpected problems: %v", probs)
	}
	if def == nil {
		t.Fatal("expected actor definition")
	}
	if def.Name != "test-actor" {
		t.Errorf("name = %q, want test-actor", def.Name)
	}
	if _, ok := def.Operations["do-thing"]; !ok {
		t.Error("expected do-thing operation")
	}
	// Kind defaults to Name
	if def.Kind != "test-actor" {
		t.Errorf("kind = %q, want test-actor (default from name)", def.Kind)
	}
}

func TestLoadScenarioFile(t *testing.T) {
	dir := t.TempDir()
	scenFile := filepath.Join(dir, "scenario.yaml")
	content := `id: TEST-001
title: test scenario
actors:
  srv: { uses: server }
steps:
  - srv: start
outcomes:
  correct: { final: { file-exists: x } }
`
	if err := os.WriteFile(scenFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, probs := loadScenarioFile(scenFile, dir)
	if len(probs) > 0 {
		t.Errorf("unexpected problems: %v", probs)
	}
	if s == nil {
		t.Fatal("expected scenario")
	}
	if s.ID != "TEST-001" {
		t.Errorf("id = %q, want TEST-001", s.ID)
	}
	if len(s.Steps) != 1 {
		t.Errorf("steps count = %d, want 1", len(s.Steps))
	}
}
