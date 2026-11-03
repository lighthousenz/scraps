// Package config loads and validates scraps projects: scraps.yaml, scenario.yaml, and actor.yaml files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lighthousenz/scraps/internal/lock"
	"github.com/lighthousenz/scraps/internal/model"
)

// Problem is a validation error with location information.
type Problem struct {
	Path    string
	Line    int
	Col     int
	Message string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d:%d: %s", p.Path, p.Line, p.Col, p.Message)
}

// LoadedProject is the result of loading a scraps project.
type LoadedProject struct {
	Project    model.Project
	ProjectDir string
	Scenarios  []LoadedScenario
	Actors     map[string]*model.ActorDefinition
	Checkers   map[string]*model.ActorDefinition
	LockFile   *lock.LockFile
}

// LoadedScenario holds a scenario and its source path.
type LoadedScenario struct {
	Scenario model.Scenario
	Path     string
}

// FindProjectFile walks up from dir looking for scraps.yaml.
func FindProjectFile(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "scraps.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("scraps.yaml not found in %s or any parent directory", dir)
		}
		dir = parent
	}
}

// LoadProject loads a complete project.
func LoadProject(scenarioPaths []string) (*LoadedProject, []Problem) {
	var problems []Problem

	// Find scraps.yaml
	wd, err := os.Getwd()
	if err != nil {
		return nil, []Problem{{Path: ".", Line: 1, Col: 1, Message: fmt.Sprintf("cannot get working directory: %s", err)}}
	}

	projectFile, err := FindProjectFile(wd)
	if err != nil {
		return nil, []Problem{{Path: ".", Line: 1, Col: 1, Message: err.Error()}}
	}
	projectDir := filepath.Dir(projectFile)

	// Load project config
	proj, probs := loadProjectFile(projectFile)
	problems = append(problems, probs...)
	if proj == nil {
		return nil, problems
	}

	result := &LoadedProject{
		Project:    *proj,
		ProjectDir: projectDir,
		Actors:     make(map[string]*model.ActorDefinition),
		Checkers:   make(map[string]*model.ActorDefinition),
	}

	// Load lock file if it exists
	lockPath := filepath.Join(projectDir, "scraps.lock")
	if _, err := os.Stat(lockPath); err == nil {
		lf, err := lock.Load(lockPath)
		if err != nil {
			problems = append(problems, Problem{Path: lockPath, Line: 1, Col: 1, Message: err.Error()})
		} else {
			result.LockFile = lf
		}
	}

	// Load actor definitions
	for name, source := range proj.Actors {
		actorDef, probs := loadActorSource(source, name, projectDir, result.LockFile)
		problems = append(problems, probs...)
		if actorDef != nil {
			result.Actors[name] = actorDef
		}
	}

	// Load checker definitions
	for _, source := range proj.Checkers {
		checkerDef, probs := loadActorSource(source, "", projectDir, result.LockFile)
		problems = append(problems, probs...)
		if checkerDef != nil {
			if _, dup := result.Checkers[checkerDef.Name]; dup {
				problems = append(problems, Problem{
					Path: filepath.Join(projectDir, "scraps.yaml"), Line: 1, Col: 1,
					Message: fmt.Sprintf("duplicate checker name %q (from %s)", checkerDef.Name, source),
				})
			}
			result.Checkers[checkerDef.Name] = checkerDef
		}
	}

	// Find and load scenarios
	defaulted := len(scenarioPaths) == 0
	if defaulted {
		scenarioPaths = []string{filepath.Join(projectDir, "scenarios")}
	}

	for _, sp := range scenarioPaths {
		if !filepath.IsAbs(sp) {
			sp = filepath.Join(wd, sp)
		}
		scenarios, probs := findAndLoadScenarios(sp, projectDir, defaulted)
		problems = append(problems, probs...)
		result.Scenarios = append(result.Scenarios, scenarios...)
	}

	return result, problems
}

func loadProjectFile(path string) (*model.Project, []Problem) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: fmt.Sprintf("cannot read: %s", err)}}
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: fmt.Sprintf("YAML parse error: %s", err)}}
	}

	var proj model.Project
	if err := strictDecode(&node, &proj, path); err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: err.Error()}}
	}

	return &proj, nil
}

func loadActorSource(source, name, projectDir string, lf *lock.LockFile) (*model.ActorDefinition, []Problem) {
	var actorDir string

	if lock.IsLocalSource(source) {
		actorDir = filepath.Join(projectDir, source)
	} else if lock.IsGitHubSource(source) {
		_, cacheDir, err := lock.Lookup(lf, source, projectDir)
		if err != nil {
			return nil, []Problem{{
				Path: filepath.Join(projectDir, "scraps.yaml"), Line: 1, Col: 1,
				Message: err.Error(),
			}}
		}
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			return nil, []Problem{{
				Path: filepath.Join(projectDir, "scraps.yaml"), Line: 1, Col: 1,
				Message: fmt.Sprintf("cache directory for %q not found; run \"scraps lock\"", source),
			}}
		}
		actorDir = cacheDir
	} else {
		return nil, []Problem{{
			Path: filepath.Join(projectDir, "scraps.yaml"), Line: 1, Col: 1,
			Message: fmt.Sprintf("invalid actor source %q: must start with ./, ../ or github:", source),
		}}
	}

	actorFile := filepath.Join(actorDir, "actor.yaml")
	return loadActorFile(actorFile, name)
}

func loadActorFile(path, expectedName string) (*model.ActorDefinition, []Problem) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: fmt.Sprintf("cannot read: %s", err)}}
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: fmt.Sprintf("YAML parse error: %s", err)}}
	}

	var def model.ActorDefinition
	if err := strictDecode(&node, &def, path); err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: err.Error()}}
	}

	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		def.Node = node.Content[0]
	}

	// Default kind to name
	if def.Kind == "" {
		def.Kind = def.Name
	}

	return &def, nil
}

func findAndLoadScenarios(path, projectDir string, defaulted bool) ([]LoadedScenario, []Problem) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if defaulted {
				return nil, nil // missing scenarios/ is not an error when using the default path
			}
			return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: fmt.Sprintf("scenario path does not exist: %s", path)}}
		}
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: err.Error()}}
	}

	if !info.IsDir() {
		// Single scenario file
		if filepath.Base(path) == "scenario.yaml" {
			s, probs := loadScenarioFile(path, projectDir)
			if s != nil {
				return []LoadedScenario{{Scenario: *s, Path: path}}, probs
			}
			return nil, probs
		}
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: "expected scenario.yaml or a directory"}}
	}

	// Recurse directory for scenario.yaml files
	var scenarios []LoadedScenario
	var problems []Problem
	err = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(p) == "scenario.yaml" {
			s, probs := loadScenarioFile(p, projectDir)
			problems = append(problems, probs...)
			if s != nil {
				scenarios = append(scenarios, LoadedScenario{Scenario: *s, Path: p})
			}
		}
		return nil
	})
	if err != nil {
		problems = append(problems, Problem{Path: path, Line: 1, Col: 1, Message: err.Error()})
	}
	return scenarios, problems
}

func loadScenarioFile(path, projectDir string) (*model.Scenario, []Problem) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: fmt.Sprintf("cannot read: %s", err)}}
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: fmt.Sprintf("YAML parse error: %s", err)}}
	}

	var scenario model.Scenario
	if err := strictDecode(&node, &scenario, path); err != nil {
		return nil, []Problem{{Path: path, Line: 1, Col: 1, Message: err.Error()}}
	}

	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		scenario.Node = node.Content[0]
	}

	return &scenario, nil
}

// strictDecode decodes a yaml.Node into v, rejecting unknown fields.
func strictDecode(node *yaml.Node, v any, _ string) error {
	doc := node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	// Marshal back to bytes and use a strict decoder.
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	return dec.Decode(v)
}
