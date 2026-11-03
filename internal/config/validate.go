package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/lighthousenz/scraps/internal/expr"
	"github.com/lighthousenz/scraps/internal/graph"
	"github.com/lighthousenz/scraps/internal/lock"
	"github.com/lighthousenz/scraps/internal/model"
)

// builtinOps are operations that actors cannot define.
var builtinOps = map[string]bool{
	"start": true,
	"stop":  true,
	"await": true,
}

// builtinChecks are checks available without a checker.
var builtinChecks = map[string]bool{
	"file-exists": true,
	"json":        true,
	"text":        true,
}

// Validate performs full static validation of a loaded project.
func Validate(lp *LoadedProject) []Problem {
	var problems []Problem

	// Validate expressions inside actor definitions.
	for name, def := range lp.Actors {
		actorScope := buildActorScope(def, lp)
		actorPath := actorPathForDef(name, lp)
		problems = append(problems, validateActorExprs(def, actorPath, actorScope)...)
	}

	for i := range lp.Scenarios {
		ls := &lp.Scenarios[i]
		probs := validateScenario(ls, lp)
		problems = append(problems, probs...)
	}

	return problems
}

// actorPathForDef returns the actor.yaml path for a named actor definition.
func actorPathForDef(name string, lp *LoadedProject) string {
	source := lp.Project.Actors[name]
	if source != "" && lock.IsLocalSource(source) {
		return filepath.Join(lp.ProjectDir, source, "actor.yaml")
	}
	return fmt.Sprintf("actors/%s/actor.yaml", name)
}

func validateScenario(ls *LoadedScenario, lp *LoadedProject) []Problem {
	var problems []Problem
	s := &ls.Scenario
	path := ls.Path
	scenarioDir := filepath.Dir(path)

	// Build scope for this scenario
	scope := buildScope(s, lp)

	// Collect resolved actor definitions for this scenario
	scenarioActorDefs := make(map[string]*model.ActorDefinition)
	for actorName, sa := range s.Actors {
		if def, ok := lp.Actors[sa.Uses]; ok {
			scenarioActorDefs[actorName] = def
		}
	}

	// --- Actor references ---
	for actorName, sa := range s.Actors {
		if _, ok := lp.Actors[sa.Uses]; !ok {
			problems = append(problems, prob(path, sa.Node, fmt.Sprintf("actor %q uses unknown definition %q", actorName, sa.Uses)))
		}
	}

	// --- Step validation ---
	artefactOps := make(map[string]string) // as-value -> actor that produced it
	for si, step := range s.Steps {
		for _, target := range step.Targets {
			if _, ok := s.Actors[target]; !ok {
				problems = append(problems, prob(path, step.Node, fmt.Sprintf("step %d: target %q is not declared in actors", si+1, target)))
			}
		}

		// Builtin step validation
		switch step.Operation {
		case "await":
			condName, _ := step.Params["condition"].(string)
			if condName == "" {
				problems = append(problems, prob(path, step.Node, fmt.Sprintf("step %d: await requires a condition name", si+1)))
			}
		case "start", "stop":
			if len(step.Params) > 0 {
				problems = append(problems, prob(path, step.Node, fmt.Sprintf("step %d: %s does not accept params", si+1, step.Operation)))
			}
		}

		// Operation validation
		if !builtinOps[step.Operation] {
			for _, target := range step.Targets {
				def := scenarioActorDefs[target]
				if def != nil {
					if _, ok := def.Operations[step.Operation]; !ok {
						problems = append(problems, prob(path, step.Node, fmt.Sprintf("step %d: actor %q does not define operation %q", si+1, target, step.Operation)))
					}
				}
			}
		}

		// Params validation (run even when Params is nil to catch missing required params)
		if !builtinOps[step.Operation] {
			params := step.Params
			if params == nil {
				params = map[string]any{}
			}
			for _, target := range step.Targets {
				def := scenarioActorDefs[target]
				if def != nil {
					if opDef, ok := def.Operations[step.Operation]; ok {
						probs := validateParams(params, opDef.Params, path, step.Node, fmt.Sprintf("step %d", si+1))
						problems = append(problems, probs...)
					}
				}
			}
		}

		// Track artefact operations
		if step.Params != nil {
			if asVal, ok := step.Params["as"]; ok {
				if asStr, ok := asVal.(string); ok && !expr.ContainsExpr(asStr) {
					for _, target := range step.Targets {
						def := scenarioActorDefs[target]
						if def != nil {
							if opDef, ok := def.Operations[step.Operation]; ok && opDef.Artefact {
								if existing, dup := artefactOps[asStr]; dup {
									problems = append(problems, prob(path, step.Node, fmt.Sprintf("step %d: duplicate artefact name %q (also produced by %s)", si+1, asStr, existing)))
								} else {
									artefactOps[asStr] = fmt.Sprintf("step %d (%s)", si+1, target)
								}
							}
						}
					}
				}
			}
		}

		// Expression validation in params
		if step.Params != nil {
			problems = append(problems, validateExprsInValue(step.Params, path, step.Node, scope)...)
		}
	}

	// Also collect artefact names from actor collect phases.
	// Artefact names from collect are scoped per actor (artefacts land in per-actor dirs),
	// so different actors using the same definition do not collide.
	for actorName, sa := range s.Actors {
		def := lp.Actors[sa.Uses]
		if def == nil {
			continue
		}
		for _, collectOp := range def.Lifecycle.Collect {
			for opName, params := range collectOp {
				opDef, ok := def.Operations[opName]
				if !ok || !opDef.Artefact {
					continue
				}
				if paramsMap, ok := params.(map[string]any); ok {
					if asVal, ok := paramsMap["as"]; ok {
						if asStr, ok := asVal.(string); ok && !expr.ContainsExpr(asStr) {
							artefactOps[asStr] = fmt.Sprintf("collect (%s)", actorName)
						}
					}
				}
			}
		}
	}

	// --- Reserved names ---
	for actorName, sa := range s.Actors {
		def := lp.Actors[sa.Uses]
		if def == nil {
			continue
		}
		actorFile := actorPathForDef(sa.Uses, lp)
		for opName := range def.Operations {
			if builtinOps[opName] {
				problems = append(problems, prob(actorFile, def.Node, fmt.Sprintf("actor %q defines reserved operation %q", actorName, opName)))
			}
			if strings.HasPrefix(opName, "scraps/") {
				problems = append(problems, prob(actorFile, def.Node, fmt.Sprintf("actor %q defines reserved operation %q (scraps/ prefix)", actorName, opName)))
			}
		}
	}

	// Checker reserved param check
	for _, checkerDef := range lp.Checkers {
		for opName, opDef := range checkerDef.Operations {
			if opDef.Params != nil {
				if _, has := opDef.Params["artefact"]; has {
					problems = append(problems, prob(path, nil, fmt.Sprintf("checker %q operation %q declares reserved param \"artefact\"", checkerDef.Name, opName)))
				}
			}
		}
	}

	// --- Inputs ---
	for actorName, sa := range s.Actors {
		def := lp.Actors[sa.Uses]
		if def == nil {
			continue
		}
		// Check with keys are declared inputs
		for withKey := range sa.With {
			if _, ok := def.Inputs[withKey]; !ok {
				problems = append(problems, prob(path, sa.Node, fmt.Sprintf("actor %q: with key %q is not a declared input", actorName, withKey)))
			}
		}
		// Check required inputs are provided
		for inputName, inputDef := range def.Inputs {
			if inputDef.Default == nil {
				if _, ok := sa.With[inputName]; !ok {
					problems = append(problems, prob(path, sa.Node, fmt.Sprintf("actor %q: required input %q is missing", actorName, inputName)))
				}
			}
		}
		// Check actor-typed inputs
		for inputName, inputDef := range def.Inputs {
			if inputDef.Type != "actor" {
				continue
			}
			val, ok := sa.With[inputName]
			if !ok {
				continue
			}
			refName, ok := val.(string)
			if !ok {
				continue
			}
			// The referenced actor must exist in the scenario
			if _, ok := s.Actors[refName]; !ok {
				problems = append(problems, prob(path, sa.Node, fmt.Sprintf("actor %q: input %q references unknown actor %q", actorName, inputName, refName)))
				continue
			}
			// Check kind matches
			if inputDef.Kind != "" {
				refDef := scenarioActorDefs[refName]
				if refDef != nil {
					refKind := refDef.Kind
					if refKind == "" {
						refKind = refDef.Name
					}
					if refKind != inputDef.Kind {
						problems = append(problems, prob(path, sa.Node, fmt.Sprintf("actor %q: input %q expects kind %q but %q has kind %q", actorName, inputName, inputDef.Kind, refName, refKind)))
					}
				}
			}
		}
	}

	// --- Conditions ---
	for actorName, sa := range s.Actors {
		def := lp.Actors[sa.Uses]
		if def == nil {
			continue
		}
		// Check lifecycle.ready
		if def.Lifecycle.Ready != "" {
			if _, ok := def.Conditions[def.Lifecycle.Ready]; !ok {
				problems = append(problems, prob(path, def.Node, fmt.Sprintf("actor %q: lifecycle.ready names undefined condition %q", actorName, def.Lifecycle.Ready)))
			}
		}
		// Check conditions
		for condName, cond := range def.Conditions {
			if _, ok := def.Operations[cond.Call]; !ok {
				problems = append(problems, prob(path, def.Node, fmt.Sprintf("actor %q: condition %q calls undefined operation %q", actorName, condName, cond.Call)))
			}
		}
	}

	// Check await steps reference valid conditions, and condition type vs target count
	for si, step := range s.Steps {
		if step.Operation != "await" {
			continue
		}
		condName, _ := step.Params["condition"].(string)
		if condName == "" {
			continue
		}
		for _, target := range step.Targets {
			def := scenarioActorDefs[target]
			if def == nil {
				continue
			}
			cond, ok := def.Conditions[condName]
			if !ok {
				problems = append(problems, prob(path, step.Node, fmt.Sprintf("step %d: actor %q does not define condition %q", si+1, target, condName)))
				continue
			}
			// all-equal on single target
			if cond.AllEqual && len(step.Targets) == 1 {
				problems = append(problems, prob(path, step.Node, fmt.Sprintf("step %d: condition %q uses all-equal on a single target", si+1, condName)))
			}
			// until on group
			if cond.Until != nil && len(step.Targets) > 1 {
				problems = append(problems, prob(path, step.Node, fmt.Sprintf("step %d: condition %q uses until on a group", si+1, condName)))
			}
		}
	}

	// --- Graph: cycle detection ---
	actorNames := make([]string, 0, len(s.Actors))
	for name := range s.Actors {
		actorNames = append(actorNames, name)
	}
	sort.Strings(actorNames)

	deps := make(map[string][]string)
	for actorName, sa := range s.Actors {
		def := lp.Actors[sa.Uses]
		if def == nil {
			continue
		}
		for inputName, inputDef := range def.Inputs {
			if inputDef.Type != "actor" {
				continue
			}
			if val, ok := sa.With[inputName]; ok {
				if refName, ok := val.(string); ok {
					deps[actorName] = append(deps[actorName], refName)
				}
			}
		}
	}

	g := graph.New(actorNames, deps)
	if cycle := g.DetectCycle(); cycle != nil {
		problems = append(problems, prob(path, s.Node, graph.CycleError(cycle)))
	}

	// --- Fixtures ---
	for fixName, fixPath := range s.Fixtures {
		absPath := filepath.Join(scenarioDir, fixPath)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			problems = append(problems, prob(path, s.Node, fmt.Sprintf("fixture %q: path %q does not exist", fixName, fixPath)))
		}
	}

	// --- Links ---
	for linkName, linkDef := range lp.Project.Links {
		// Check required links
		if linkDef.Required {
			if _, ok := s.Links[linkName]; !ok {
				problems = append(problems, prob(path, s.Node, fmt.Sprintf("required link %q is missing", linkName)))
			}
		}
		// Check required-when
		if linkDef.RequiredWhen != nil {
			if _, ok := s.Links[linkName]; !ok {
				notOutcome := linkDef.RequiredWhen.ExpectsNot
				for _, entry := range s.History {
					if entry.Expect != notOutcome {
						problems = append(problems, prob(path, s.Node, fmt.Sprintf("link %q is required when history expects something other than %q", linkName, notOutcome)))
						break
					}
				}
			}
		}
	}
	// Check link values against patterns and that they're declared
	for linkName, linkVal := range s.Links {
		linkDef, ok := lp.Project.Links[linkName]
		if !ok {
			problems = append(problems, prob(path, s.Node, fmt.Sprintf("link %q is not declared in scraps.yaml", linkName)))
			continue
		}
		if linkDef.Pattern != "" {
			re, err := regexp.Compile(linkDef.Pattern)
			if err != nil {
				problems = append(problems, prob(path, s.Node, fmt.Sprintf("link %q: invalid pattern %q: %s", linkName, linkDef.Pattern, err)))
				continue
			}
			if !re.MatchString(linkVal) {
				problems = append(problems, prob(path, s.Node, fmt.Sprintf("link %q: value %q does not match pattern %q", linkName, linkVal, linkDef.Pattern)))
			}
		}
	}

	// --- Outcomes ---
	if _, ok := s.Outcomes["correct"]; !ok {
		problems = append(problems, prob(path, s.Node, "outcomes must include \"correct\""))
	}

	// Build set of checker operations and validate uniqueness
	checkerOps := make(map[string]string) // op name -> checker name
	for checkerName, checkerDef := range lp.Checkers {
		for opName := range checkerDef.Operations {
			if existing, dup := checkerOps[opName]; dup {
				problems = append(problems, prob(path, nil, fmt.Sprintf("checker operation %q is defined by both %q and %q", opName, existing, checkerName)))
			} else {
				checkerOps[opName] = checkerName
			}
		}
	}

	// Validate each outcome's checks
	for outName, checks := range s.Outcomes {
		for _, check := range checks {
			if check.StepError != nil {
				// step-error matcher validation
				problems = append(problems, validateStepErrorMatcher(check.StepError, s, path, check.Node)...)
				continue
			}

			// Check name must be a built-in or exactly one checker op
			if !builtinChecks[check.CheckName] {
				if _, ok := checkerOps[check.CheckName]; !ok {
					problems = append(problems, prob(path, check.Node, fmt.Sprintf("outcome %q: unknown check %q", outName, check.CheckName)))
				}
			}

			// Artefact must be produced by some artefact operation
			if check.Artefact != "" {
				if _, ok := artefactOps[check.Artefact]; !ok {
					problems = append(problems, prob(path, check.Node, fmt.Sprintf("outcome %q: artefact %q is not produced by any artefact operation", outName, check.Artefact)))
				}
			}
		}
	}

	// --- History ---
	for hi, entry := range s.History {
		// expect must name a declared outcome
		if _, ok := s.Outcomes[entry.Expect]; !ok {
			problems = append(problems, prob(path, entry.Node, fmt.Sprintf("history[%d]: expect names undefined outcome %q", hi, entry.Expect)))
		}
		// when must name declared subjects
		for subjName, constraint := range entry.When {
			subj, ok := lp.Project.Subjects[subjName]
			if !ok {
				problems = append(problems, prob(path, entry.Node, fmt.Sprintf("history[%d]: when names undeclared subject %q", hi, subjName)))
				continue
			}
			// Validate constraint parses
			switch subj.Scheme {
			case "semver":
				if _, err := semver.NewConstraint(constraint); err != nil {
					problems = append(problems, prob(path, entry.Node, fmt.Sprintf("history[%d]: invalid semver constraint %q for subject %q: %s", hi, constraint, subjName, err)))
				}
			case "exact":
				// any string is valid for exact
			default:
				problems = append(problems, prob(path, entry.Node, fmt.Sprintf("history[%d]: unknown scheme %q for subject %q", hi, subj.Scheme, subjName)))
			}
		}
	}

	// --- Expression checks on all string values ---
	// Check expressions in actor with values
	for actorName, sa := range s.Actors {
		for inputName, val := range sa.With {
			if str, ok := val.(string); ok {
				for _, errMsg := range expr.CheckAllExprs(str, scope) {
					problems = append(problems, prob(path, sa.Node, fmt.Sprintf("actor %q input %q: %s", actorName, inputName, errMsg)))
				}
			}
		}
	}

	return problems
}

func validateStepErrorMatcher(matcher *model.StepErrorMatcher, s *model.Scenario, path string, node *yaml.Node) []Problem {
	var problems []Problem
	hasField := false
	if matcher.Actor != "" {
		hasField = true
		// actor must be used in steps
		found := false
		for _, step := range s.Steps {
			for _, t := range step.Targets {
				if t == matcher.Actor {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			problems = append(problems, prob(path, node, fmt.Sprintf("step-error: actor %q is not used in steps", matcher.Actor)))
		}
	}
	if matcher.Operation != "" {
		hasField = true
		// operation must be used in steps
		found := false
		for _, step := range s.Steps {
			if step.Operation == matcher.Operation {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, prob(path, node, fmt.Sprintf("step-error: operation %q is not used in steps", matcher.Operation)))
		}
	}
	if matcher.Message != nil {
		hasField = true
		switch m := matcher.Message.(type) {
		case string:
			// bare string is a valid exact-match message
		case map[string]any:
			if pattern, ok := m["matches"]; ok {
				if patStr, ok := pattern.(string); ok {
					if _, err := regexp.Compile(patStr); err != nil {
						problems = append(problems, prob(path, node, fmt.Sprintf("step-error: invalid message regex %q: %s", patStr, err)))
					}
				} else {
					problems = append(problems, prob(path, node, "step-error: message.matches must be a string"))
				}
				for k := range m {
					if k != "matches" {
						problems = append(problems, prob(path, node, fmt.Sprintf("step-error: message has unknown key %q (only \"matches\" is allowed)", k)))
					}
				}
			} else {
				problems = append(problems, prob(path, node, "step-error: message map must have a \"matches\" key"))
			}
		default:
			problems = append(problems, prob(path, node, "step-error: message must be a string or { matches: <regex> }"))
		}
	}
	if !hasField {
		problems = append(problems, prob(path, node, "step-error: must set at least one of actor, operation, or message"))
	}
	return problems
}

func validateParams(params map[string]any, declaredParams map[string]any, path string, node *yaml.Node, context string) []Problem {
	if declaredParams == nil {
		if len(params) > 0 {
			return []Problem{prob(path, node, fmt.Sprintf("%s: operation takes no params but got %d", context, len(params)))}
		}
		return nil
	}

	// Check that all params contain expressions or validate against schema
	// Params containing ${{ }} skip type checking but must still be declared
	var problems []Problem

	// Build JSON Schema for validation
	schemaProps := make(map[string]any)
	required := make([]string, 0)
	for pName, pSchema := range declaredParams {
		schemaMap, ok := pSchema.(map[string]any)
		if !ok {
			schemaProps[pName] = map[string]any{}
			continue
		}
		schemaProps[pName] = schemaMap
		// Required if no default
		if _, hasDefault := schemaMap["default"]; !hasDefault {
			required = append(required, pName)
		}
	}

	// Check for undeclared params
	for pName := range params {
		if _, ok := declaredParams[pName]; !ok {
			problems = append(problems, prob(path, node, fmt.Sprintf("%s: unknown param %q", context, pName)))
		}
	}

	// Check required params
	for _, rp := range required {
		if _, ok := params[rp]; !ok {
			problems = append(problems, prob(path, node, fmt.Sprintf("%s: required param %q is missing", context, rp)))
		}
	}

	// Skip type validation for params containing expressions
	paramsToValidate := make(map[string]any)
	for pName, pVal := range params {
		if _, ok := declaredParams[pName]; !ok {
			continue
		}
		if str, ok := pVal.(string); ok && expr.ContainsExpr(str) {
			continue
		}
		paramsToValidate[pName] = pVal
	}

	if len(paramsToValidate) > 0 && len(schemaProps) > 0 {
		// Build and validate against JSON Schema
		schemaObj := map[string]any{
			"type":                 "object",
			"properties":           schemaProps,
			"additionalProperties": false,
		}
		// Only include required params that we're actually validating
		var validatedRequired []string
		for _, rp := range required {
			if _, ok := paramsToValidate[rp]; ok {
				validatedRequired = append(validatedRequired, rp)
			}
		}
		if len(validatedRequired) > 0 {
			schemaObj["required"] = validatedRequired
		}

		schemaJSON, err := json.Marshal(schemaObj)
		if err != nil {
			problems = append(problems, prob(path, node, fmt.Sprintf("%s: cannot marshal param schema: %s", context, err)))
		} else {
			var decoded any
			if err := json.Unmarshal(schemaJSON, &decoded); err != nil {
				problems = append(problems, prob(path, node, fmt.Sprintf("%s: cannot decode param schema: %s", context, err)))
			} else {
				compiler := jsonschema.NewCompiler()
				// Prevent $ref / external schema resolution from reading the filesystem.
				// Param schemas are in-memory type/enum/items/default declarations and must
				// never trigger file I/O.
				compiler.UseLoader(denyLoader{})
				if err := compiler.AddResource("schema.json", decoded); err != nil {
					problems = append(problems, prob(path, node, fmt.Sprintf("%s: invalid param schema: %s", context, err)))
				} else {
					sch, err := compiler.Compile("schema.json")
					if err != nil {
						problems = append(problems, prob(path, node, fmt.Sprintf("%s: invalid param schema: %s", context, err)))
					} else {
						if err := sch.Validate(paramsToValidate); err != nil {
							problems = append(problems, prob(path, node, fmt.Sprintf("%s: param validation failed: %s", context, err)))
						}
					}
				}
			}
		}
	}

	return problems
}

func validateExprsInValue(v any, path string, node *yaml.Node, scope *expr.StaticScope) []Problem {
	var problems []Problem
	switch val := v.(type) {
	case string:
		for _, errMsg := range expr.CheckAllExprs(val, scope) {
			problems = append(problems, prob(path, node, errMsg))
		}
	case map[string]any:
		for _, child := range val {
			problems = append(problems, validateExprsInValue(child, path, node, scope)...)
		}
	case []any:
		for _, child := range val {
			problems = append(problems, validateExprsInValue(child, path, node, scope)...)
		}
	}
	return problems
}

func buildScope(s *model.Scenario, lp *LoadedProject) *expr.StaticScope {
	scope := &expr.StaticScope{
		Fixtures: make(map[string]bool),
		Subjects: make(map[string]bool),
		Actors:   make(map[string]map[string]bool),
	}
	for name := range s.Fixtures {
		scope.Fixtures[name] = true
	}
	for name := range lp.Project.Subjects {
		scope.Subjects[name] = true
	}
	for actorName, sa := range s.Actors {
		def := lp.Actors[sa.Uses]
		if def != nil {
			outputs := make(map[string]bool)
			for outName := range def.Outputs {
				outputs[outName] = true
			}
			scope.Actors[actorName] = outputs
		}
	}
	return scope
}

// buildActorScope builds a scope for checking expressions inside an actor definition.
func buildActorScope(def *model.ActorDefinition, lp *LoadedProject) *expr.StaticScope {
	scope := &expr.StaticScope{
		Subjects: make(map[string]bool),
		Inputs:   make(map[string]expr.InputInfo),
	}
	for name := range lp.Project.Subjects {
		scope.Subjects[name] = true
	}
	for inputName, inputDef := range def.Inputs {
		info := expr.InputInfo{IsActor: inputDef.Type == "actor"}
		if info.IsActor && inputDef.Kind != "" {
			// Intersect outputs from ALL definitions matching this kind.
			// A reference is accepted only when every implementation that
			// can satisfy this input declares that output. This avoids
			// false-ok validation that would fail at runtime when wired to
			// an implementation that lacks the output.
			first := true
			for _, actorDef := range lp.Actors {
				kind := actorDef.Kind
				if kind == "" {
					kind = actorDef.Name
				}
				if kind != inputDef.Kind {
					continue
				}
				if first {
					info.Outputs = make(map[string]bool)
					for outName := range actorDef.Outputs {
						info.Outputs[outName] = true
					}
					first = false
				} else {
					// Keep only outputs present in this definition too.
					for outName := range info.Outputs {
						if _, ok := actorDef.Outputs[outName]; !ok {
							delete(info.Outputs, outName)
						}
					}
				}
			}
		}
		scope.Inputs[inputName] = info
	}
	return scope
}

// validateActorExprs checks all expressions found in an actor definition.
func validateActorExprs(def *model.ActorDefinition, actorPath string, scope *expr.StaticScope) []Problem {
	var problems []Problem
	walkLifecycleExprs := func(entries []model.LifecycleEntry) {
		for _, entry := range entries {
			for _, params := range entry {
				problems = append(problems, validateExprsInValue(params, actorPath, def.Node, scope)...)
			}
		}
	}
	walkLifecycleExprs(def.Lifecycle.Start)
	walkLifecycleExprs(def.Lifecycle.Stop)
	walkLifecycleExprs(def.Lifecycle.Collect)
	return problems
}

// denyLoader is a jsonschema.URLLoader that refuses to load any external resource.
// It prevents $ref, $dynamicRef, and $schema from reading the filesystem or network
// during param schema compilation.
type denyLoader struct{}

func (denyLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema references are not allowed in param schemas: %s", url)
}

func prob(path string, node *yaml.Node, msg string) Problem {
	line, col := 1, 1
	if node != nil {
		line = node.Line
		col = node.Column
	}
	return Problem{
		Path:    path,
		Line:    line,
		Col:     col,
		Message: msg,
	}
}
