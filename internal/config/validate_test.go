package config

import (
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/lighthousenz/scraps/internal/model"
)

// helper to build a minimal valid loaded project from parts.
func minimalProject() *LoadedProject {
	return &LoadedProject{
		Project: model.Project{
			Version: 1,
			Subjects: map[string]model.Subject{
				"docengine": {Scheme: "semver", Resolve: model.SubjectSource{Env: "DOCENGINE_VERSION"}},
			},
		},
		ProjectDir: "/project",
		Actors: map[string]*model.ActorDefinition{
			"server": {
				Name:     "server",
				Kind:     "server",
				Protocol: 1,
				Inputs: map[string]model.InputDef{
					"doc": {Type: "string"},
				},
				Outputs: map[string]model.OutputDef{
					"url": {Type: "string"},
				},
				Lifecycle: model.Lifecycle{
					Ready: "listening",
				},
				Conditions: map[string]model.ConditionDef{
					"listening": {Call: "status", Until: map[string]any{"listening": true}, Every: "50ms"},
				},
				Operations: map[string]model.OperationDef{
					"status": {Returns: map[string]any{"listening": map[string]any{"type": "boolean"}}},
					"snapshot": {
						Params:   map[string]any{"as": map[string]any{"type": "string"}},
						Artefact: true,
					},
				},
			},
			"editor": {
				Name:     "editor",
				Kind:     "editor",
				Protocol: 1,
				Inputs: map[string]model.InputDef{
					"srv": {Type: "actor", Kind: "server"},
				},
				Lifecycle: model.Lifecycle{
					Ready: "synced",
				},
				Conditions: map[string]model.ConditionDef{
					"synced":    {Call: "get-status", Until: map[string]any{"synced": true}, Every: "50ms"},
					"converged": {Call: "digest", AllEqual: true, Every: "50ms"},
				},
				Operations: map[string]model.OperationDef{
					"get-status":       {Returns: map[string]any{"synced": map[string]any{"type": "boolean"}}},
					"digest":           {Returns: map[string]any{"value": map[string]any{"type": "string"}}},
					"pause-sync":       {},
					"resume-sync":      {},
					"insert-paragraph": {Params: map[string]any{"at": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}},
				},
			},
		},
		Checkers: map[string]*model.ActorDefinition{},
	}
}

func minimalScenario(path string) LoadedScenario {
	return LoadedScenario{
		Scenario: model.Scenario{
			ID:    "TEST-001",
			Title: "test scenario",
			Actors: map[string]model.ScenarioActor{
				"srv":   {Uses: "server", With: map[string]any{"doc": "/tmp/doc"}},
				"alice": {Uses: "editor", With: map[string]any{"srv": "srv"}},
			},
			Steps: []model.Step{
				{Targets: []string{"alice"}, Operation: "pause-sync"},
				{Targets: []string{"alice"}, Operation: "resume-sync"},
				{Targets: []string{"srv"}, Operation: "snapshot", Params: map[string]any{"as": "final"}},
			},
			Outcomes: map[string]model.Outcome{
				"correct": {{Artefact: "final", CheckName: "file-exists", Value: "final.json"}},
			},
			History: []model.HistoryEntry{
				{When: map[string]string{"docengine": ">=1.0.0"}, Expect: "correct"},
			},
		},
		Path: path,
	}
}

func containsProblem(problems []Problem, substr string) bool {
	for _, p := range problems {
		if strings.Contains(p.String(), substr) {
			return true
		}
	}
	return false
}

func requireNoProblem(t *testing.T, problems []Problem) {
	t.Helper()
	if len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("unexpected problem: %s", p.String())
		}
	}
}

// --- Actor references ---

func TestValidate_ActorRefUnknown(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["ghost"] = model.ScenarioActor{Uses: "nonexistent"}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `uses unknown definition "nonexistent"`) {
		t.Errorf("expected unknown actor def problem, got %v", problems)
	}
}

func TestValidate_ActorRefValid(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, "unknown definition") {
		t.Errorf("did not expect unknown definition problem, got %v", problems)
	}
}

// --- Operations ---

func TestValidate_UnknownOperation(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Steps = append(ls.Scenario.Steps, model.Step{
		Targets: []string{"alice"}, Operation: "does-not-exist",
	})
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `does not define operation "does-not-exist"`) {
		t.Errorf("expected unknown operation problem, got %v", problems)
	}
}

func TestValidate_BuiltinOpAllowed(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Steps = append(ls.Scenario.Steps, model.Step{
		Targets: []string{"srv"}, Operation: "start",
	})
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, `does not define operation "start"`) {
		t.Errorf("start should be allowed as builtin, got %v", problems)
	}
}

// --- Step target validation ---

func TestValidate_StepTargetNotDeclared(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Steps = append(ls.Scenario.Steps, model.Step{
		Targets: []string{"nobody"}, Operation: "pause-sync",
	})
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `target "nobody" is not declared`) {
		t.Errorf("expected undeclared target problem, got %v", problems)
	}
}

// --- Reserved operation names ---

func TestValidate_ReservedOpName(t *testing.T) {
	lp := minimalProject()
	// Actor defines a reserved operation "start"
	lp.Actors["bad-actor"] = &model.ActorDefinition{
		Name: "bad-actor",
		Kind: "bad-actor",
		Operations: map[string]model.OperationDef{
			"start": {},
		},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["bad"] = model.ScenarioActor{Uses: "bad-actor"}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `reserved operation "start"`) {
		t.Errorf("expected reserved op problem, got %v", problems)
	}
}

func TestValidate_ScrapsSlashPrefix(t *testing.T) {
	lp := minimalProject()
	lp.Actors["bad-actor"] = &model.ActorDefinition{
		Name: "bad-actor",
		Kind: "bad-actor",
		Operations: map[string]model.OperationDef{
			"scraps/custom": {},
		},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["bad"] = model.ScenarioActor{Uses: "bad-actor"}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `reserved operation "scraps/custom"`) {
		t.Errorf("expected scraps/ prefix problem, got %v", problems)
	}
}

// --- Inputs ---

func TestValidate_UnknownInput(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["alice"] = model.ScenarioActor{
		Uses: "editor",
		With: map[string]any{"srv": "srv", "unknown-input": "val"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `with key "unknown-input" is not a declared input`) {
		t.Errorf("expected unknown input problem, got %v", problems)
	}
}

func TestValidate_RequiredInputMissing(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["alice"] = model.ScenarioActor{
		Uses: "editor",
		With: map[string]any{}, // missing required "srv"
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `required input "srv" is missing`) {
		t.Errorf("expected missing required input problem, got %v", problems)
	}
}

func TestValidate_InputKindMismatch(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	// alice's "srv" input expects kind: server, but we point it to itself (kind: editor)
	ls.Scenario.Actors["alice"] = model.ScenarioActor{
		Uses: "editor",
		With: map[string]any{"srv": "alice"}, // alice is an editor, not a server
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `expects kind "server"`) {
		t.Errorf("expected kind mismatch problem, got %v", problems)
	}
}

func TestValidate_InputActorRefUnknown(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["alice"] = model.ScenarioActor{
		Uses: "editor",
		With: map[string]any{"srv": "nonexistent"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `references unknown actor "nonexistent"`) {
		t.Errorf("expected unknown actor ref problem, got %v", problems)
	}
}

// --- Conditions ---

func TestValidate_LifecycleReadyUnknownCondition(t *testing.T) {
	lp := minimalProject()
	lp.Actors["bad"] = &model.ActorDefinition{
		Name: "bad",
		Kind: "bad",
		Lifecycle: model.Lifecycle{
			Ready: "doesnotexist",
		},
		Conditions: map[string]model.ConditionDef{},
		Operations: map[string]model.OperationDef{},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["badguy"] = model.ScenarioActor{Uses: "bad"}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `lifecycle.ready names undefined condition "doesnotexist"`) {
		t.Errorf("expected undefined condition problem, got %v", problems)
	}
}

func TestValidate_ConditionCallsUndefinedOp(t *testing.T) {
	lp := minimalProject()
	lp.Actors["bad"] = &model.ActorDefinition{
		Name: "bad",
		Kind: "bad",
		Conditions: map[string]model.ConditionDef{
			"ready": {Call: "no-such-op"},
		},
		Operations: map[string]model.OperationDef{},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["badguy"] = model.ScenarioActor{Uses: "bad"}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `condition "ready" calls undefined operation "no-such-op"`) {
		t.Errorf("expected undefined op problem, got %v", problems)
	}
}

func TestValidate_AllEqualOnSingleTarget(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	// await converged on a single target, but converged uses all-equal
	ls.Scenario.Steps = append(ls.Scenario.Steps, model.Step{
		Targets:   []string{"alice"},
		Operation: "await",
		Params:    map[string]any{"condition": "converged"},
	})
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, "all-equal on a single target") {
		t.Errorf("expected all-equal warning, got %v", problems)
	}
}

func TestValidate_UntilOnGroup(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	// Make two editors so we can form a group
	ls.Scenario.Actors["bob"] = model.ScenarioActor{Uses: "editor", With: map[string]any{"srv": "srv"}}
	ls.Scenario.Steps = append(ls.Scenario.Steps, model.Step{
		Targets:   []string{"alice", "bob"},
		Operation: "await",
		Params:    map[string]any{"condition": "synced"},
	})
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, "until on a group") {
		t.Errorf("expected until on group warning, got %v", problems)
	}
}

func TestValidate_AwaitUnknownCondition(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Steps = append(ls.Scenario.Steps, model.Step{
		Targets:   []string{"alice"},
		Operation: "await",
		Params:    map[string]any{"condition": "nonexistent"},
	})
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `does not define condition "nonexistent"`) {
		t.Errorf("expected unknown condition problem, got %v", problems)
	}
}

// --- Graph: cycle detection ---

func TestValidate_CycleDetection(t *testing.T) {
	lp := minimalProject()
	// A depends on B depends on A
	lp.Actors["a"] = &model.ActorDefinition{
		Name: "a", Kind: "a",
		Inputs:     map[string]model.InputDef{"dep": {Type: "actor"}},
		Operations: map[string]model.OperationDef{},
	}
	lp.Actors["b"] = &model.ActorDefinition{
		Name: "b", Kind: "b",
		Inputs:     map[string]model.InputDef{"dep": {Type: "actor"}},
		Operations: map[string]model.OperationDef{},
	}

	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["a"] = model.ScenarioActor{Uses: "a", With: map[string]any{"dep": "b"}}
	ls.Scenario.Actors["b"] = model.ScenarioActor{Uses: "b", With: map[string]any{"dep": "a"}}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, "dependency cycle") {
		t.Errorf("expected cycle problem, got %v", problems)
	}
}

func TestValidate_NoCycle(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, "dependency cycle") {
		t.Errorf("did not expect cycle problem, got %v", problems)
	}
}

// --- Fixtures ---

func TestValidate_FixturePathMissing(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Fixtures = map[string]string{
		"doc": "fixtures/does-not-exist",
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `fixture "doc": path "fixtures/does-not-exist" does not exist`) {
		t.Errorf("expected missing fixture problem, got %v", problems)
	}
}

func TestValidate_FixturePathExists(t *testing.T) {
	lp := minimalProject()
	scenDir := t.TempDir()
	scenPath := filepath.Join(scenDir, "scenario.yaml")
	ls := minimalScenario(scenPath)
	// The fixture path references scenDir itself, which exists
	ls.Scenario.Fixtures = map[string]string{
		"doc": ".",
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, `fixture "doc"`) {
		t.Errorf("did not expect fixture problem, got %v", problems)
	}
}

// --- Links ---

func TestValidate_RequiredLinkMissing(t *testing.T) {
	lp := minimalProject()
	lp.Project.Links = map[string]model.Link{
		"ticket": {Pattern: "^ENG-\\d+$", Required: true},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Links = nil
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `required link "ticket" is missing`) {
		t.Errorf("expected required link problem, got %v", problems)
	}
}

func TestValidate_LinkPatternMismatch(t *testing.T) {
	lp := minimalProject()
	lp.Project.Links = map[string]model.Link{
		"ticket": {Pattern: "^ENG-\\d+$"},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Links = map[string]string{"ticket": "BAD-VALUE"}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `does not match pattern`) {
		t.Errorf("expected pattern mismatch problem, got %v", problems)
	}
}

func TestValidate_LinkPatternMatch(t *testing.T) {
	lp := minimalProject()
	lp.Project.Links = map[string]model.Link{
		"ticket": {Pattern: "^ENG-\\d+$"},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Links = map[string]string{"ticket": "ENG-1234"}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, "does not match pattern") {
		t.Errorf("did not expect pattern problem, got %v", problems)
	}
}

func TestValidate_UndeclaredLink(t *testing.T) {
	lp := minimalProject()
	lp.Project.Links = map[string]model.Link{}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Links = map[string]string{"mystery": "value"}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `link "mystery" is not declared in scraps.yaml`) {
		t.Errorf("expected undeclared link problem, got %v", problems)
	}
}

func TestValidate_RequiredWhenLink(t *testing.T) {
	lp := minimalProject()
	lp.Project.Links = map[string]model.Link{
		"upstream": {RequiredWhen: &model.RequiredWhen{ExpectsNot: "correct"}},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Links = nil
	// History entry expects something other than "correct"
	ls.Scenario.History = []model.HistoryEntry{
		{When: map[string]string{"docengine": ">=1.0.0"}, Expect: "broken-outcome"},
	}
	ls.Scenario.Outcomes["broken-outcome"] = model.Outcome{{Artefact: "final", CheckName: "file-exists", Value: "x"}}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `link "upstream" is required when history expects something other than "correct"`) {
		t.Errorf("expected required-when link problem, got %v", problems)
	}
}

// --- Outcomes ---

func TestValidate_MissingCorrectOutcome(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"broken": {{Artefact: "final", CheckName: "file-exists", Value: "x"}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `outcomes must include "correct"`) {
		t.Errorf("expected missing correct outcome problem, got %v", problems)
	}
}

func TestValidate_UnknownCheckName(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"correct": {{Artefact: "final", CheckName: "nonexistent-check", Value: "x"}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `unknown check "nonexistent-check"`) {
		t.Errorf("expected unknown check problem, got %v", problems)
	}
}

func TestValidate_BuiltinCheckAllowed(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"correct": {{Artefact: "final", CheckName: "file-exists", Value: "final.json"}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, `unknown check "file-exists"`) {
		t.Errorf("file-exists should be allowed as builtin, got %v", problems)
	}
}

func TestValidate_ArtefactNotProduced(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"correct": {{Artefact: "no-such-artefact", CheckName: "file-exists", Value: "x"}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `artefact "no-such-artefact" is not produced`) {
		t.Errorf("expected artefact not produced problem, got %v", problems)
	}
}

// --- Step-error ---

func TestValidate_StepErrorEmpty(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"correct": {{Artefact: "final", CheckName: "file-exists", Value: "x"}},
		"throws":  {{StepError: &model.StepErrorMatcher{}}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, "must set at least one of actor, operation, or message") {
		t.Errorf("expected step-error empty problem, got %v", problems)
	}
}

func TestValidate_StepErrorActorNotInSteps(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"correct": {{Artefact: "final", CheckName: "file-exists", Value: "x"}},
		"throws": {{StepError: &model.StepErrorMatcher{
			Actor: "nonexistent",
		}}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `step-error: actor "nonexistent" is not used in steps`) {
		t.Errorf("expected step-error actor problem, got %v", problems)
	}
}

func TestValidate_StepErrorOperationNotInSteps(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"correct": {{Artefact: "final", CheckName: "file-exists", Value: "x"}},
		"throws": {{StepError: &model.StepErrorMatcher{
			Operation: "does-not-exist",
		}}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `step-error: operation "does-not-exist" is not used in steps`) {
		t.Errorf("expected step-error operation problem, got %v", problems)
	}
}

func TestValidate_StepErrorInvalidRegex(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"correct": {{Artefact: "final", CheckName: "file-exists", Value: "x"}},
		"throws": {{StepError: &model.StepErrorMatcher{
			Actor:   "alice",
			Message: map[string]any{"matches": "[invalid"},
		}}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, "invalid message regex") {
		t.Errorf("expected invalid regex problem, got %v", problems)
	}
}

func TestValidate_StepErrorValid(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Outcomes = map[string]model.Outcome{
		"correct": {{Artefact: "final", CheckName: "file-exists", Value: "x"}},
		"throws": {{StepError: &model.StepErrorMatcher{
			Actor:     "alice",
			Operation: "pause-sync",
		}}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, "step-error") {
		t.Errorf("did not expect step-error problem, got %v", problems)
	}
}

// --- History ---

func TestValidate_HistoryUnknownOutcome(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.History = []model.HistoryEntry{
		{When: map[string]string{"docengine": ">=1.0.0"}, Expect: "nonexistent"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `expect names undefined outcome "nonexistent"`) {
		t.Errorf("expected unknown outcome problem, got %v", problems)
	}
}

func TestValidate_HistoryUnknownSubject(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.History = []model.HistoryEntry{
		{When: map[string]string{"nosuchsubject": ">=1.0.0"}, Expect: "correct"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `when names undeclared subject "nosuchsubject"`) {
		t.Errorf("expected unknown subject problem, got %v", problems)
	}
}

func TestValidate_HistoryInvalidSemver(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.History = []model.HistoryEntry{
		{When: map[string]string{"docengine": "not a valid constraint!!!"}, Expect: "correct"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, "invalid semver constraint") {
		t.Errorf("expected invalid semver problem, got %v", problems)
	}
}

func TestValidate_HistoryValidSemver(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.History = []model.HistoryEntry{
		{When: map[string]string{"docengine": ">=2.31.0 <2.33.0"}, Expect: "correct"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, "invalid semver constraint") {
		t.Errorf("did not expect semver constraint problem, got %v", problems)
	}
}

func TestValidate_HistoryExactScheme(t *testing.T) {
	lp := minimalProject()
	lp.Project.Subjects["exact-subj"] = model.Subject{Scheme: "exact"}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.History = []model.HistoryEntry{
		{When: map[string]string{"exact-subj": "anything goes"}, Expect: "correct"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, "invalid") {
		t.Errorf("exact scheme should accept any string, got %v", problems)
	}
}

func TestValidate_HistoryUnknownScheme(t *testing.T) {
	lp := minimalProject()
	lp.Project.Subjects["weird"] = model.Subject{Scheme: "weird"}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.History = []model.HistoryEntry{
		{When: map[string]string{"weird": "v1"}, Expect: "correct"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `unknown scheme "weird"`) {
		t.Errorf("expected unknown scheme problem, got %v", problems)
	}
}

// --- Expressions ---

func TestValidate_ExprInParams(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Fixtures = map[string]string{"doc": "."}
	ls.Scenario.Steps = []model.Step{
		{Targets: []string{"srv"}, Operation: "snapshot", Params: map[string]any{"as": "${{ fixtures.doc }}"}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if containsProblem(problems, `unknown root`) {
		t.Errorf("valid fixture expression should not produce error, got %v", problems)
	}
}

func TestValidate_ExprInvalidRef(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Steps = []model.Step{
		{Targets: []string{"srv"}, Operation: "snapshot", Params: map[string]any{"as": "${{ nope.unknown }}"}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `unknown root "nope"`) {
		t.Errorf("expected invalid expr problem, got %v", problems)
	}
}

func TestValidate_ExprInActorWith(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Actors["srv"] = model.ScenarioActor{
		Uses: "server",
		With: map[string]any{"doc": "${{ nope.bad }}"},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `unknown root "nope"`) {
		t.Errorf("expected expr validation on with values, got %v", problems)
	}
}

// --- Checker reserved param ---

func TestValidate_CheckerReservedParam(t *testing.T) {
	lp := minimalProject()
	lp.Checkers["bad-checker"] = &model.ActorDefinition{
		Name: "bad-checker",
		Operations: map[string]model.OperationDef{
			"check-it": {Params: map[string]any{"artefact": map[string]any{"type": "string"}}},
		},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `declares reserved param "artefact"`) {
		t.Errorf("expected reserved param problem, got %v", problems)
	}
}

// --- Params ---

func TestValidate_UnknownParam(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Steps = []model.Step{
		{Targets: []string{"alice"}, Operation: "insert-paragraph", Params: map[string]any{
			"at": "start", "text": "hi", "extra": "bad",
		}},
		{Targets: []string{"srv"}, Operation: "snapshot", Params: map[string]any{"as": "final"}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `unknown param "extra"`) {
		t.Errorf("expected unknown param problem, got %v", problems)
	}
}

// --- Duplicate artefact names ---

func TestValidate_DuplicateArtefact(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	ls.Scenario.Steps = []model.Step{
		{Targets: []string{"srv"}, Operation: "snapshot", Params: map[string]any{"as": "final"}},
		{Targets: []string{"srv"}, Operation: "snapshot", Params: map[string]any{"as": "final"}},
	}
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, "duplicate artefact name") {
		t.Errorf("expected duplicate artefact problem, got %v", problems)
	}
}

// --- Checker operation uniqueness ---

func TestValidate_DuplicateCheckerOp(t *testing.T) {
	lp := minimalProject()
	lp.Checkers["checker-a"] = &model.ActorDefinition{
		Name:       "checker-a",
		Operations: map[string]model.OperationDef{"same-op": {}},
	}
	lp.Checkers["checker-b"] = &model.ActorDefinition{
		Name:       "checker-b",
		Operations: map[string]model.OperationDef{"same-op": {}},
	}
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	if !containsProblem(problems, `checker operation "same-op" is defined by both`) {
		t.Errorf("expected duplicate checker op problem, got %v", problems)
	}
}

// --- Full valid scenario ---

func TestValidate_ValidScenario(t *testing.T) {
	lp := minimalProject()
	ls := minimalScenario("/project/scenarios/test/scenario.yaml")
	lp.Scenarios = []LoadedScenario{ls}

	problems := Validate(lp)
	requireNoProblem(t, problems)
}

// --- Problem.String() ---

func TestProblem_String(t *testing.T) {
	p := Problem{Path: "scraps.yaml", Line: 5, Col: 3, Message: "bad field"}
	want := "scraps.yaml:5:3: bad field"
	if got := p.String(); got != want {
		t.Errorf("Problem.String() = %q, want %q", got, want)
	}
}

// --- prob() with nil node ---

func TestProb_NilNode(t *testing.T) {
	p := prob("test.yaml", nil, "test message")
	if p.Line != 1 || p.Col != 1 {
		t.Errorf("prob with nil node: line=%d col=%d, want 1:1", p.Line, p.Col)
	}
}

func TestProb_WithNode(t *testing.T) {
	node := &yaml.Node{Line: 10, Column: 5}
	p := prob("test.yaml", node, "test message")
	if p.Line != 10 || p.Col != 5 {
		t.Errorf("prob with node: line=%d col=%d, want 10:5", p.Line, p.Col)
	}
}
