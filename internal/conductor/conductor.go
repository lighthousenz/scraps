// Package conductor orchestrates a single scenario run: prepare, start, steps, stop and collect.
package conductor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lighthousenz/scraps/internal/actor"
	"github.com/lighthousenz/scraps/internal/checks"
	"github.com/lighthousenz/scraps/internal/config"
	"github.com/lighthousenz/scraps/internal/expr"
	"github.com/lighthousenz/scraps/internal/graph"
	"github.com/lighthousenz/scraps/internal/lock"
	"github.com/lighthousenz/scraps/internal/model"
	"github.com/lighthousenz/scraps/internal/protocol"
	"github.com/lighthousenz/scraps/internal/verdict"
)

const (
	defaultStepTimeout  = 10 * time.Second
	defaultReadyTimeout = 30 * time.Second
	defaultPollInterval = 50 * time.Millisecond
	shutdownTimeout     = 5 * time.Second
	termTimeout         = 2 * time.Second
)

// RunConfig holds the configuration for running scenarios.
type RunConfig struct {
	Project      *config.LoadedProject
	Scenarios    []config.LoadedScenario
	Subjects     map[string]string
	Seed         int64
	StepTimeout  time.Duration
	ReadyTimeout time.Duration
	FailOn       []string
	Keep         bool
	RunDir       string
	Version      string
	CacheDir     string

	// Checkers holds prepared checker processes shared across scenarios.
	// If nil, the runner will prepare and manage them.
	Checkers map[string]*checkerHandle
}

// checkerHandle holds a checker process and its definition.
type checkerHandle struct {
	process *actor.Process
	def     *model.ActorDefinition
}

// actorHandle holds a running actor process and its state.
type actorHandle struct {
	process    *actor.Process
	def        *model.ActorDefinition
	outputs    map[string]any
	running    bool
	workdir    string
	artefacts  string
	logPath    string
	prepareDir string
}

// scenarioRunner runs a single scenario.
type scenarioRunner struct {
	cfg       *RunConfig
	scenario  model.Scenario
	path      string
	scenDir   string
	actors    map[string]*actorHandle
	depGraph  *graph.Graph
	exprCtx   *expr.Context
	stepError *verdict.StepFailure
	broken    bool
	brokenMsg string
	start     time.Time

	// mu guards exprCtx.Actors, artefactFiles, and artefactActor during
	// concurrent actor startup within a dependency layer.
	mu sync.Mutex

	// artefactFiles maps artefact name -> files returned by artefact operations.
	artefactFiles map[string][]string
	// artefactActor maps artefact name -> actor name that produced it.
	artefactActor map[string]string

	// checkers holds shared checker handles.
	checkers map[string]*checkerHandle
}

// RunResult holds the results of running all scenarios.
type RunResult struct {
	Report  model.RunReport
	FailOn  map[model.Verdict]bool
	HasFail bool
}

// Run executes all scenarios and returns results.
func Run(ctx context.Context, cfg *RunConfig) (*RunResult, error) {
	report := model.RunReport{
		Schema:   1,
		Scraps:   cfg.Version,
		Seed:     cfg.Seed,
		Subjects: cfg.Subjects,
	}

	failOn, err := buildFailOnSet(cfg.FailOn)
	if err != nil {
		return nil, err
	}

	results := make([]model.ScenarioResult, len(cfg.Scenarios))

	// Prepare checkers once for the whole run.
	checkers, err := prepareCheckers(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("prepare checkers: %w", err)
	}
	defer shutdownCheckers(ctx, checkers)

	for i, ls := range cfg.Scenarios {
		select {
		case <-ctx.Done():
			// Mark remaining scenarios as broken and return partial results.
			for j := i; j < len(cfg.Scenarios); j++ {
				results[j] = brokenResult(cfg.Scenarios[j].Scenario, cfg, time.Now(), "cancelled")
			}
			report.Results = results
			hasFail := false
			for _, r := range results {
				if failOn[r.Verdict] {
					hasFail = true
					break
				}
			}
			return &RunResult{
				Report:  report,
				FailOn:  failOn,
				HasFail: hasFail,
			}, nil
		default:
		}

		// Restart any checker whose client latched broken from a previous
		// scenario, so each scenario gets a clean checker state.
		if err := restartBrokenCheckers(ctx, cfg, checkers); err != nil {
			return nil, fmt.Errorf("restart checkers: %w", err)
		}

		result, err := runScenario(ctx, cfg, ls, checkers)
		results[i] = result
		if err != nil {
			return nil, err
		}
	}

	report.Results = results

	hasFail := false
	for _, r := range results {
		if failOn[r.Verdict] {
			hasFail = true
			break
		}
	}

	return &RunResult{
		Report:  report,
		FailOn:  failOn,
		HasFail: hasFail,
	}, nil
}

// RunParallel executes scenarios with the given parallelism.
func RunParallel(ctx context.Context, cfg *RunConfig, parallel int) (*RunResult, error) {
	if parallel <= 1 {
		return Run(ctx, cfg)
	}

	report := model.RunReport{
		Schema:   1,
		Scraps:   cfg.Version,
		Seed:     cfg.Seed,
		Subjects: cfg.Subjects,
	}

	failOn, err := buildFailOnSet(cfg.FailOn)
	if err != nil {
		return nil, err
	}

	// Prepare checkers once per scenario when running in parallel.
	// Each scenario gets its own checker instances for isolation.
	results := make([]model.ScenarioResult, len(cfg.Scenarios))
	sem := make(chan struct{}, parallel)
	var mu sync.Mutex
	var wg sync.WaitGroup

	var runErr error

	for i, ls := range cfg.Scenarios {
		wg.Add(1)
		go func(idx int, ls config.LoadedScenario) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Each parallel scenario gets its own checkers for isolation.
			checkers, err := prepareCheckers(ctx, cfg)
			if err != nil {
				// A missing mise is a harness error, not a per-scenario broken.
				if errors.Is(err, actor.ErrMiseNotFound) {
					mu.Lock()
					if runErr == nil {
						runErr = fmt.Errorf("prepare checkers: %w", err)
					}
					mu.Unlock()
					return
				}
				mu.Lock()
				results[idx] = brokenResult(ls.Scenario, cfg, time.Now(), fmt.Sprintf("prepare checkers: %s", err))
				mu.Unlock()
				return
			}
			defer shutdownCheckers(ctx, checkers)

			result, err := runScenario(ctx, cfg, ls, checkers)
			mu.Lock()
			results[idx] = result
			if err != nil && runErr == nil {
				runErr = err
			}
			mu.Unlock()
		}(i, ls)
	}

	wg.Wait()

	if runErr != nil {
		return nil, runErr
	}
	report.Results = results

	hasFail := false
	for _, r := range results {
		if failOn[r.Verdict] {
			hasFail = true
			break
		}
	}

	return &RunResult{
		Report:  report,
		FailOn:  failOn,
		HasFail: hasFail,
	}, nil
}

// runScenario executes a single scenario. It returns a result and optionally
// an error for harness-level failures (e.g. missing toolchain) that should
// abort the entire run rather than just marking this scenario broken.
func runScenario(ctx context.Context, cfg *RunConfig, ls config.LoadedScenario, checkers map[string]*checkerHandle) (model.ScenarioResult, error) {
	start := time.Now()
	scenario := ls.Scenario

	// Build the scenario directory layout.
	scenDir := filepath.Join(cfg.RunDir, scenario.ID)

	// Build dependency graph.
	actors := make([]string, 0, len(scenario.Actors))
	deps := make(map[string][]string)
	for name := range scenario.Actors {
		actors = append(actors, name)
	}
	sort.Strings(actors)

	for name, sa := range scenario.Actors {
		actorDef := cfg.Project.Actors[sa.Uses]
		if actorDef == nil {
			continue
		}
		for inputName, inputDef := range actorDef.Inputs {
			if inputDef.Type == "actor" {
				if withVal, ok := sa.With[inputName]; ok {
					if depName, ok := withVal.(string); ok {
						deps[name] = append(deps[name], depName)
					}
				}
			}
		}
	}

	g := graph.New(actors, deps)

	sr := &scenarioRunner{
		cfg:           cfg,
		scenario:      scenario,
		path:          ls.Path,
		scenDir:       scenDir,
		actors:        make(map[string]*actorHandle),
		depGraph:      g,
		artefactFiles: make(map[string][]string),
		artefactActor: make(map[string]string),
		checkers:      checkers,
		start:         start,
	}

	// Initialize actor handles with directories.
	for name, sa := range scenario.Actors {
		actorDef := cfg.Project.Actors[sa.Uses]
		if actorDef == nil {
			sr.broken = true
			sr.brokenMsg = fmt.Sprintf("actor %q uses unknown definition %q", name, sa.Uses)
			return sr.buildResult(ctx), nil
		}

		workdir := filepath.Join(scenDir, "actors", name)
		artefactsDir := filepath.Join(scenDir, "artefacts", name)
		logPath := filepath.Join(scenDir, "logs", name+".log")

		// Prepare the actor.
		sourceDir := resolveActorSource(cfg.Project, sa.Uses)
		if sourceDir == "" {
			sr.broken = true
			sr.brokenMsg = fmt.Sprintf("cannot resolve source for actor %q", sa.Uses)
			return sr.buildResult(ctx), nil
		}

		prepareResult, err := actor.Prepare(ctx, &actor.PrepareConfig{
			SourceDir:        sourceDir,
			CacheDir:         cfg.CacheDir,
			Tools:            actorDef.Tools,
			PrepareRun:       prepareCmdFromDef(actorDef),
			CacheKeySubjects: prepareCacheKey(actorDef),
			Subjects:         cfg.Subjects,
			Resolver:         makeSubjectResolver(cfg.Subjects),
		})
		if err != nil {
			if errors.Is(err, actor.ErrMiseNotFound) {
				return sr.buildResult(ctx), fmt.Errorf("prepare %q: %w", name, err)
			}
			sr.broken = true
			sr.brokenMsg = fmt.Sprintf("prepare %q: %s", name, err)
			return sr.buildResult(ctx), nil
		}

		sr.actors[name] = &actorHandle{
			def:        actorDef,
			workdir:    workdir,
			artefacts:  artefactsDir,
			logPath:    logPath,
			prepareDir: prepareResult.Dir,
		}
	}

	// Build the expression context.
	sr.exprCtx = sr.buildExprContext()

	// Start auto actors.
	if !sr.broken {
		sr.startAutoActors(ctx)
	}

	// Run steps.
	if !sr.broken && sr.stepError == nil {
		sr.runSteps(ctx)
	}

	// Stop everything.
	sr.stopAll(ctx)

	return sr.buildResult(ctx), nil
}

func (sr *scenarioRunner) startAutoActors(ctx context.Context) {
	layers, err := sr.depGraph.StartLayers()
	if err != nil {
		sr.broken = true
		sr.brokenMsg = err.Error()
		return
	}

	for _, layer := range layers {
		// Filter to only auto-start actors in this layer.
		var autoActors []string
		for _, name := range layer {
			sa := sr.scenario.Actors[name]
			if sa.Start == "" || sa.Start == "auto" {
				autoActors = append(autoActors, name)
			}
		}

		if len(autoActors) == 0 {
			continue
		}

		// Start actors in this layer in parallel.
		var wg sync.WaitGroup
		var mu sync.Mutex
		var startErr error

		for _, name := range autoActors {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				if err := sr.startActor(ctx, name); err != nil {
					mu.Lock()
					if startErr == nil {
						startErr = fmt.Errorf("start %q: %w", name, err)
					}
					mu.Unlock()
				}
			}(name)
		}
		wg.Wait()

		if startErr != nil {
			sr.broken = true
			sr.brokenMsg = startErr.Error()
			return
		}
	}
}

func (sr *scenarioRunner) startActor(ctx context.Context, name string) error {
	ah := sr.actors[name]
	if ah.running {
		return nil
	}

	// Spawn the process.
	proc, err := actor.Start(ctx, &actor.StartConfig{
		Tools:     ah.def.Tools,
		Run:       ah.def.Run,
		Dir:       ah.prepareDir,
		Workdir:   ah.workdir,
		Artefacts: ah.artefacts,
		LogPath:   ah.logPath,
		OnLog:     nil,
	})
	if err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	ah.process = proc

	// On any error after spawn, stop the process so it does not leak.
	ok := false
	defer func() {
		if !ok {
			_ = proc.Stop(ctx, shutdownTimeout, termTimeout)
		}
	}()

	// Resolve inputs for this actor.
	inputs := sr.resolveInputs(name)
	sr.mu.Lock()
	isBroken := sr.broken
	sr.mu.Unlock()
	if isBroken {
		return fmt.Errorf("resolve inputs for %q: broken", name)
	}

	// Send scraps/initialize.
	initCtx, initCancel := context.WithTimeout(ctx, sr.readyTimeout())
	defer initCancel()

	initResult, err := proc.Client().Initialize(initCtx, &protocol.InitializeParams{
		Protocol:  protocol.ProtocolVersion,
		Name:      name,
		Kind:      ah.def.Kind,
		Inputs:    inputs,
		Subjects:  sr.cfg.Subjects,
		Seed:      sr.cfg.Seed,
		Workdir:   ah.workdir,
		Artefacts: ah.artefacts,
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	ah.outputs = initResult.Outputs
	if ah.outputs == nil {
		ah.outputs = make(map[string]any)
	}

	// Update expression context with outputs.
	sr.updateActorOutputs(name, ah.outputs)

	// Run lifecycle.start operations with actor-scoped inputs.
	actorCtx := sr.actorExprContext(name, inputs)
	for _, entry := range ah.def.Lifecycle.Start {
		for opName, rawParams := range entry {
			params, err := sr.resolveParamsWithCtx(rawParams, actorCtx)
			if err != nil {
				return fmt.Errorf("resolve start params for %s: %w", opName, err)
			}
			callCtx, callCancel := context.WithTimeout(ctx, sr.stepTimeout())
			raw, err := proc.Client().Call(callCtx, opName, params)
			callCancel()
			if err != nil {
				return fmt.Errorf("start operation %s: %w", opName, err)
			}
			// Track artefact operations in lifecycle.
			if opDef, ok := ah.def.Operations[opName]; ok && opDef.Artefact {
				sr.recordArtefactResult(name, params, raw)
			}
		}
	}

	// Poll ready condition.
	if ah.def.Lifecycle.Ready != "" {
		condDef := ah.def.Conditions[ah.def.Lifecycle.Ready]
		if err := sr.pollCondition(ctx, name, condDef, []string{name}, sr.readyTimeout()); err != nil {
			return fmt.Errorf("ready: %w", err)
		}
	}

	ok = true
	ah.running = true
	return nil
}

func (sr *scenarioRunner) runSteps(ctx context.Context) {
	for _, step := range sr.scenario.Steps {
		if sr.broken || sr.stepError != nil {
			break
		}
		sr.runStep(ctx, step)
	}
}

func (sr *scenarioRunner) runStep(ctx context.Context, step model.Step) {
	targets := step.Targets
	op := step.Operation

	switch op {
	case "start":
		for _, t := range targets {
			if sr.broken {
				return
			}
			sr.handleStart(ctx, t)
		}
	case "stop":
		for _, t := range targets {
			if sr.broken {
				return
			}
			sr.handleStop(ctx, t)
		}
	case "await":
		sr.handleAwait(ctx, step)
	default:
		// Regular operation — run on each target in order.
		for _, t := range targets {
			if sr.broken || sr.stepError != nil {
				return
			}
			sr.handleOperation(ctx, t, op, step.Params)
		}
	}
}

func (sr *scenarioRunner) handleStart(ctx context.Context, name string) {
	// Start cascade: start stopped dependencies first.
	cascade := sr.depGraph.StartCascade(name)
	for _, dep := range cascade {
		ah := sr.actors[dep]
		if ah != nil && !ah.running {
			if err := sr.startActor(ctx, dep); err != nil {
				sr.setBroken(fmt.Sprintf("start cascade %q: %s", dep, err))
				return
			}
		}
	}

	if err := sr.startActor(ctx, name); err != nil {
		sr.setBroken(fmt.Sprintf("start %q: %s", name, err))
	}
}

func (sr *scenarioRunner) handleStop(ctx context.Context, name string) {
	// Stop cascade: stop running dependents first, in reverse order.
	cascade := sr.depGraph.StopCascade(name)
	for _, dep := range cascade {
		ah := sr.actors[dep]
		if ah != nil && ah.running {
			sr.stopActor(ctx, dep)
		}
	}

	ah := sr.actors[name]
	if ah != nil && ah.running {
		sr.stopActor(ctx, name)
	}
}

func (sr *scenarioRunner) stopActor(ctx context.Context, name string) {
	ah := sr.actors[name]
	if ah == nil || !ah.running || ah.process == nil {
		return
	}

	// Build a per-actor expression context with inputs for lifecycle resolution.
	actorInputs := sr.resolveInputs(name)
	actorCtx := sr.actorExprContext(name, actorInputs)

	// Run lifecycle.stop operations.
	for _, entry := range ah.def.Lifecycle.Stop {
		for opName, rawParams := range entry {
			params, err := sr.resolveParamsWithCtx(rawParams, actorCtx)
			if err != nil {
				// Log but don't break on stop errors.
				continue
			}
			callCtx, callCancel := context.WithTimeout(ctx, sr.stepTimeout())
			_, _ = ah.process.Client().Call(callCtx, opName, params)
			callCancel()
		}
	}

	// Run lifecycle.collect operations.
	for _, entry := range ah.def.Lifecycle.Collect {
		for opName, rawParams := range entry {
			params, err := sr.resolveParamsWithCtx(rawParams, actorCtx)
			if err != nil {
				continue
			}
			callCtx, callCancel := context.WithTimeout(ctx, sr.stepTimeout())
			raw, callErr := ah.process.Client().Call(callCtx, opName, params)
			callCancel()
			if callErr == nil {
				if opDef, ok := ah.def.Operations[opName]; ok && opDef.Artefact {
					sr.recordArtefactResult(name, params, raw)
				}
			}
		}
	}

	// Send shutdown and stop the process.
	_ = ah.process.Stop(ctx, shutdownTimeout, termTimeout)
	ah.running = false
}

func (sr *scenarioRunner) handleAwait(ctx context.Context, step model.Step) {
	condName := ""
	if step.Params != nil {
		if c, ok := step.Params["condition"]; ok {
			condName, _ = c.(string)
		}
	}
	if condName == "" {
		sr.setBroken("await step missing condition name")
		return
	}

	// Get the condition definition from the first target's actor definition.
	firstTarget := step.Targets[0]
	ah := sr.actors[firstTarget]
	if ah == nil {
		sr.setBroken(fmt.Sprintf("await: unknown actor %q", firstTarget))
		return
	}

	condDef, ok := ah.def.Conditions[condName]
	if !ok {
		sr.setBroken(fmt.Sprintf("await: actor %q has no condition %q", firstTarget, condName))
		return
	}

	if err := sr.pollCondition(ctx, firstTarget, condDef, step.Targets, sr.stepTimeout()); err != nil {
		// A protocol BrokenError means the actor is genuinely broken.
		// A timeout or context cancellation is a step failure that outcomes
		// can match, per the README: await "fails the step at the timeout".
		var be *protocol.BrokenError
		if errors.As(err, &be) {
			sr.setBroken(fmt.Sprintf("await %q: %s", condName, err))
		} else {
			sr.stepError = &verdict.StepFailure{
				Actor:     firstTarget,
				Operation: "await:" + condName,
				Message:   err.Error(),
			}
		}
	}
}

func (sr *scenarioRunner) pollCondition(ctx context.Context, primaryTarget string, condDef model.ConditionDef, targets []string, timeout time.Duration) error {
	pollInterval := defaultPollInterval
	if condDef.Every != "" {
		d, err := time.ParseDuration(condDef.Every)
		if err == nil {
			pollInterval = d
		}
	}

	deadline := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout after %s", timeout)
		default:
		}

		held, err := sr.checkCondition(ctx, condDef, targets)
		if err != nil {
			var be *protocol.BrokenError
			if errors.As(err, &be) {
				return err
			}
			// Non-broken errors are retried.
		}
		if held {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timeout after %s", timeout)
		case <-time.After(pollInterval):
		}
	}
}

func (sr *scenarioRunner) checkCondition(ctx context.Context, condDef model.ConditionDef, targets []string) (bool, error) {
	if condDef.AllEqual {
		// Call the condition's operation on every target and check all results are equal.
		var results []json.RawMessage
		for _, t := range targets {
			ah := sr.actors[t]
			if ah == nil || ah.process == nil {
				return false, fmt.Errorf("actor %q not running", t)
			}
			callCtx, callCancel := context.WithTimeout(ctx, sr.stepTimeout())
			raw, err := ah.process.Client().Call(callCtx, condDef.Call, nil)
			callCancel()
			if err != nil {
				return false, err
			}
			results = append(results, raw)
		}
		if len(results) < 2 {
			return true, nil
		}
		var first any
		if err := json.Unmarshal(results[0], &first); err != nil {
			return false, nil
		}
		for _, r := range results[1:] {
			var v any
			if err := json.Unmarshal(r, &v); err != nil {
				return false, nil
			}
			if !jsonDeepEqual(first, v) {
				return false, nil
			}
		}
		return true, nil
	}

	// until: call on the first target and check the result contains the given values.
	ah := sr.actors[targets[0]]
	if ah == nil || ah.process == nil {
		return false, fmt.Errorf("actor %q not running", targets[0])
	}
	callCtx, callCancel := context.WithTimeout(ctx, sr.stepTimeout())
	raw, err := ah.process.Client().Call(callCtx, condDef.Call, nil)
	callCancel()
	if err != nil {
		return false, err
	}

	if condDef.Until == nil {
		return true, nil
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return false, nil
	}

	for k, expected := range condDef.Until {
		actual, ok := result[k]
		if !ok {
			return false, nil
		}
		if !jsonDeepEqual(actual, expected) {
			return false, nil
		}
	}
	return true, nil
}

func (sr *scenarioRunner) handleOperation(ctx context.Context, target, op string, params map[string]any) {
	ah := sr.actors[target]
	if ah == nil || ah.process == nil {
		sr.setBroken(fmt.Sprintf("operation %q on actor %q: actor not running", op, target))
		return
	}

	resolved, err := sr.resolveMapParams(params)
	if err != nil {
		sr.setBroken(fmt.Sprintf("resolve params for %s.%s: %s", target, op, err))
		return
	}

	callCtx, callCancel := context.WithTimeout(ctx, sr.stepTimeout())
	raw, err := ah.process.Client().Call(callCtx, op, resolved)
	callCancel()

	if err != nil {
		var opErr *protocol.OperationFailedError
		var brokenErr *protocol.BrokenError
		if errors.As(err, &opErr) {
			// Error 1000 — record the step failure.
			sr.stepError = &verdict.StepFailure{
				Actor:     target,
				Operation: op,
				Message:   opErr.Message,
				Data:      opErr.Data,
			}
			return
		}
		if errors.As(err, &brokenErr) {
			// If the process exited (stdout closed / crash), treat as a
			// step failure so outcomes can match on it. A 1001 or protocol
			// violation while the process is still alive stays broken.
			select {
			case <-ah.process.Exited():
				sr.stepError = &verdict.StepFailure{
					Actor:     target,
					Operation: op,
					Message:   fmt.Sprintf("actor crashed: %s", brokenErr.Reason),
				}
			default:
				sr.setBroken(fmt.Sprintf("operation %s.%s: %s", target, op, brokenErr.Reason))
			}
			return
		}
		sr.setBroken(fmt.Sprintf("operation %s.%s: %s", target, op, err))
		return
	}

	// Track artefact operations.
	if opDef, ok := ah.def.Operations[op]; ok && opDef.Artefact {
		sr.recordArtefactResult(target, resolved, raw)
	}
}

func (sr *scenarioRunner) recordArtefactResult(actorName string, params map[string]any, raw json.RawMessage) {
	asName := ""
	if params != nil {
		if v, ok := params["as"]; ok {
			asName, _ = v.(string)
		}
	}
	if asName == "" {
		return
	}

	var result struct {
		Files []string `json:"files"`
	}
	if raw != nil {
		_ = json.Unmarshal(raw, &result)
	}

	sr.mu.Lock()
	if prev, exists := sr.artefactActor[asName]; exists && prev != actorName {
		sr.mu.Unlock()
		sr.setBroken(fmt.Sprintf("artefact %q produced by both %q and %q", asName, prev, actorName))
		return
	}
	sr.artefactFiles[asName] = result.Files
	sr.artefactActor[asName] = actorName
	sr.mu.Unlock()
}

func (sr *scenarioRunner) stopAll(ctx context.Context) {
	stopOrder, err := sr.depGraph.StopOrder()
	if err != nil {
		// If stop order fails, stop everything we can in any order.
		for name := range sr.actors {
			sr.stopActor(ctx, name)
		}
		return
	}

	for _, name := range stopOrder {
		ah := sr.actors[name]
		if ah != nil && ah.running {
			sr.stopActor(ctx, name)
		}
	}
}

func (sr *scenarioRunner) buildResult(ctx context.Context) model.ScenarioResult {
	duration := time.Since(sr.start)

	// Evaluate history.
	histResult, err := verdict.EvalHistory(
		sr.scenario.History,
		sr.cfg.Project.Project.Subjects,
		sr.cfg.Subjects,
	)
	if err != nil {
		sr.broken = true
		sr.brokenMsg = fmt.Sprintf("evaluate history: %s", err)
	}

	var classResult verdict.ClassifyResult
	actual := ""

	if sr.broken {
		classResult = verdict.Classify(verdict.ClassifyInput{
			Expected: histResult.Expected,
			Broken:   true,
			Reason:   sr.brokenMsg,
		})
	} else {
		// Evaluate outcomes.
		matched := sr.evaluateOutcomes(ctx)
		// Re-check broken: checkOutcome may set sr.broken during evaluation.
		if sr.broken {
			classResult = verdict.Classify(verdict.ClassifyInput{
				Expected: histResult.Expected,
				Broken:   true,
				Reason:   sr.brokenMsg,
			})
		} else {
			classResult = verdict.Classify(verdict.ClassifyInput{
				Expected: histResult.Expected,
				Matched:  matched,
			})
			if len(matched) == 1 {
				actual = matched[0]
			}
		}
	}

	// Resolve links.
	links := resolveLinks(sr.scenario.Links, sr.cfg.Project.Project.Links)

	return model.ScenarioResult{
		ID:             sr.scenario.ID,
		Title:          sr.scenario.Title,
		Links:          links,
		Tags:           sr.scenario.Tags,
		Expected:       histResult.Expected,
		Actual:         actual,
		Verdict:        classResult.Verdict,
		MatchedHistory: histResult.Index,
		DurationMs:     duration.Milliseconds(),
		Artefacts:      sr.scenDir,
		Reason:         classResult.Reason,
		Overlapping:    classResult.Overlapping,
	}
}

func (sr *scenarioRunner) evaluateOutcomes(ctx context.Context) []string {
	var matched []string
	for outcomeName, outcome := range sr.scenario.Outcomes {
		if sr.checkOutcome(ctx, outcomeName, outcome) {
			matched = append(matched, outcomeName)
		}
	}
	sort.Strings(matched)
	return matched
}

func (sr *scenarioRunner) checkOutcome(ctx context.Context, name string, outcome model.Outcome) bool {
	for _, check := range outcome {
		if check.StepError != nil {
			m, err := verdict.MatchStepError(check.StepError, sr.stepError)
			if err != nil || !m {
				return false
			}
			continue
		}

		// Determine the artefact.
		artName := check.Artefact
		actorName, recorded := sr.artefactActor[artName]
		if !recorded {
			// The artefact was never produced (e.g. a step failed before the
			// artefact operation ran). Treat as not matched rather than
			// resolving paths against the process's cwd.
			return false
		}
		files := sr.artefactFiles[artName]

		ah := sr.actors[actorName]
		artDir := ""
		if ah != nil {
			artDir = ah.artefacts
		}

		checkName := check.CheckName
		checkValue := check.Value

		// Try built-in checks first.
		if checkName == "file-exists" || checkName == "json" || checkName == "text" {
			art := checks.Artefact{
				Name:  artName,
				Dir:   artDir,
				Files: files,
			}
			result, err := checks.Run(checkName, art, checkValue)
			if err != nil {
				// Bad params → broken, not a check failure.
				sr.broken = true
				sr.brokenMsg = fmt.Sprintf("check %s.%s: %s", artName, checkName, err)
				return false
			}
			if !result.Match {
				return false
			}
			continue
		}

		// Checker check — find the checker for this operation.
		checkerDef := sr.findCheckerForOp(checkName)
		if checkerDef == nil {
			sr.broken = true
			sr.brokenMsg = fmt.Sprintf("no checker defines operation %q", checkName)
			return false
		}

		ch := sr.checkers[checkerDef.Name]
		if ch == nil {
			sr.broken = true
			sr.brokenMsg = fmt.Sprintf("checker %q not started", checkerDef.Name)
			return false
		}

		// Build checker params.
		checkerParams := sr.buildCheckerParams(checkName, checkValue, checkerDef, artName, artDir, files)

		callCtx, callCancel := context.WithTimeout(ctx, sr.stepTimeout())
		raw, err := ch.process.Client().Call(callCtx, checkName, checkerParams)
		callCancel()
		if err != nil {
			var be *protocol.BrokenError
			if errors.As(err, &be) {
				sr.broken = true
				sr.brokenMsg = fmt.Sprintf("checker %q broken: %s", checkerDef.Name, be.Reason)
				return false
			}
			sr.broken = true
			sr.brokenMsg = fmt.Sprintf("checker call %s: %s", checkName, err)
			return false
		}

		var result struct {
			Match  bool   `json:"match"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			sr.broken = true
			sr.brokenMsg = fmt.Sprintf("checker result parse: %s", err)
			return false
		}
		if !result.Match {
			return false
		}
	}
	return true
}

func (sr *scenarioRunner) findCheckerForOp(opName string) *model.ActorDefinition {
	for _, def := range sr.cfg.Project.Checkers {
		if _, ok := def.Operations[opName]; ok {
			return def
		}
	}
	return nil
}

func (sr *scenarioRunner) buildCheckerParams(opName string, value any, def *model.ActorDefinition, artName, artDir string, files []string) map[string]any {
	params := make(map[string]any)

	opDef := def.Operations[opName]

	// If value is a mapping, use it directly.
	if m, ok := value.(map[string]any); ok {
		for k, v := range m {
			params[k] = v
		}
	} else {
		// Single-param form: bind to the operation's only declared param.
		if len(opDef.Params) == 1 {
			for paramName := range opDef.Params {
				params[paramName] = value
			}
		}
	}

	// Add the reserved artefact key.
	params["artefact"] = map[string]any{
		"name":  artName,
		"dir":   artDir,
		"files": files,
	}

	return params
}

func (sr *scenarioRunner) setBroken(msg string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if !sr.broken {
		sr.broken = true
		sr.brokenMsg = msg
	}
}

func (sr *scenarioRunner) stepTimeout() time.Duration {
	if sr.cfg.StepTimeout > 0 {
		return sr.cfg.StepTimeout
	}
	return defaultStepTimeout
}

func (sr *scenarioRunner) readyTimeout() time.Duration {
	if sr.cfg.ReadyTimeout > 0 {
		return sr.cfg.ReadyTimeout
	}
	return defaultReadyTimeout
}

func (sr *scenarioRunner) buildExprContext() *expr.Context {
	// Resolve fixture paths.
	fixtures := make(map[string]any)
	scenarioDir := filepath.Dir(sr.path)
	for name, relPath := range sr.scenario.Fixtures {
		absPath := filepath.Join(scenarioDir, relPath)
		absPath, _ = filepath.Abs(absPath)
		fixtures[name] = absPath
	}

	actorOutputs := make(map[string]map[string]any)
	for name := range sr.scenario.Actors {
		actorOutputs[name] = make(map[string]any)
	}

	return &expr.Context{
		Fixtures: fixtures,
		Subjects: sr.cfg.Subjects,
		Actors:   actorOutputs,
		Run: map[string]any{
			"id":        sr.scenario.ID,
			"seed":      sr.cfg.Seed,
			"workdir":   sr.scenDir,
			"artefacts": sr.scenDir,
		},
	}
}

func (sr *scenarioRunner) updateActorOutputs(name string, outputs map[string]any) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.exprCtx.Actors == nil {
		sr.exprCtx.Actors = make(map[string]map[string]any)
	}
	sr.exprCtx.Actors[name] = outputs
}

func (sr *scenarioRunner) resolveInputs(name string) map[string]any {
	sa := sr.scenario.Actors[name]
	ah := sr.actors[name]
	inputs := make(map[string]any)

	// Snapshot the expression context under the lock so concurrent goroutines
	// in the same dependency layer don't race on map reads/writes.
	ectx := sr.actorExprContext(name, nil)

	for inputName, inputDef := range ah.def.Inputs {
		rawVal, ok := sa.With[inputName]
		if !ok {
			if inputDef.Default != nil {
				inputs[inputName] = inputDef.Default
			}
			continue
		}

		if inputDef.Type == "actor" {
			// Actor-typed input: provide the dependency's {name, outputs}.
			depName, _ := rawVal.(string)
			depAh := sr.actors[depName]
			depOutputs := make(map[string]any)
			if depAh != nil {
				depOutputs = depAh.outputs
			}
			inputs[inputName] = map[string]any{
				"name":    depName,
				"outputs": depOutputs,
			}
		} else {
			// Resolve expressions in string values.
			if s, ok := rawVal.(string); ok {
				resolved, err := expr.ResolveStringStrict(s, ectx)
				if err != nil {
					sr.setBroken(fmt.Sprintf("resolve input %q for actor %q: %s", inputName, name, err))
					return inputs
				}
				inputs[inputName] = resolved
			} else {
				inputs[inputName] = rawVal
			}
		}
	}
	return inputs
}

func (sr *scenarioRunner) resolveMapParams(params map[string]any) (map[string]any, error) {
	if params == nil {
		return nil, nil
	}
	resolved := make(map[string]any, len(params))
	for k, v := range params {
		rv, err := sr.resolveValue(v)
		if err != nil {
			return nil, fmt.Errorf("param %q: %w", k, err)
		}
		resolved[k] = rv
	}
	return resolved, nil
}

func (sr *scenarioRunner) resolveValue(v any) (any, error) {
	switch val := v.(type) {
	case string:
		return expr.ResolveStringStrict(val, sr.exprCtx)
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			rv, err := sr.resolveValue(v)
			if err != nil {
				return nil, err
			}
			result[k] = rv
		}
		return result, nil
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			rv, err := sr.resolveValue(v)
			if err != nil {
				return nil, err
			}
			result[i] = rv
		}
		return result, nil
	default:
		return v, nil
	}
}

// actorExprContext returns a snapshot of the shared expression context with
// per-actor Inputs and Run paths set. This is safe for concurrent use because
// it copies the shared maps rather than mutating sr.exprCtx.
// If actorName is non-empty, run.workdir and run.artefacts are set to the
// actor's per-process paths (matching SCRAPS_WORKDIR/SCRAPS_ARTEFACTS).
func (sr *scenarioRunner) actorExprContext(actorName string, inputs map[string]any) *expr.Context {
	sr.mu.Lock()
	// Snapshot the shared actors map so concurrent writes don't race.
	actorsCopy := make(map[string]map[string]any, len(sr.exprCtx.Actors))
	for k, v := range sr.exprCtx.Actors {
		actorsCopy[k] = v
	}
	sr.mu.Unlock()

	// Copy Run so per-actor overrides don't mutate the shared map.
	runCopy := make(map[string]any, len(sr.exprCtx.Run))
	for k, v := range sr.exprCtx.Run {
		runCopy[k] = v
	}
	if actorName != "" {
		if ah := sr.actors[actorName]; ah != nil {
			runCopy["workdir"] = ah.workdir
			runCopy["artefacts"] = ah.artefacts
		}
	}

	return &expr.Context{
		Fixtures: sr.exprCtx.Fixtures,
		Subjects: sr.exprCtx.Subjects,
		Actors:   actorsCopy,
		Inputs:   inputs,
		Run:      runCopy,
	}
}

func (sr *scenarioRunner) resolveParamsWithCtx(rawParams any, ectx *expr.Context) (map[string]any, error) {
	if rawParams == nil {
		return nil, nil
	}
	m, ok := rawParams.(map[string]any)
	if !ok {
		return nil, nil
	}
	return resolveMapParamsWithCtx(m, ectx)
}

func resolveMapParamsWithCtx(params map[string]any, ectx *expr.Context) (map[string]any, error) {
	if params == nil {
		return nil, nil
	}
	resolved := make(map[string]any, len(params))
	for k, v := range params {
		rv, err := resolveValueWithCtx(v, ectx)
		if err != nil {
			return nil, fmt.Errorf("param %q: %w", k, err)
		}
		resolved[k] = rv
	}
	return resolved, nil
}

func resolveValueWithCtx(v any, ectx *expr.Context) (any, error) {
	switch val := v.(type) {
	case string:
		return expr.ResolveStringStrict(val, ectx)
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			rv, err := resolveValueWithCtx(v, ectx)
			if err != nil {
				return nil, err
			}
			result[k] = rv
		}
		return result, nil
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			rv, err := resolveValueWithCtx(v, ectx)
			if err != nil {
				return nil, err
			}
			result[i] = rv
		}
		return result, nil
	default:
		return v, nil
	}
}

// Helper functions.

func buildFailOnSet(failOn []string) (map[model.Verdict]bool, error) {
	if len(failOn) == 0 {
		defaults := verdict.DefaultFailOn()
		m := make(map[model.Verdict]bool, len(defaults))
		for _, v := range defaults {
			m[v] = true
		}
		return m, nil
	}

	valid := make(map[model.Verdict]bool)
	for _, v := range verdict.AllVerdicts() {
		valid[v] = true
	}

	m := make(map[model.Verdict]bool, len(failOn))
	for _, v := range failOn {
		if v == "" {
			continue
		}
		vv := model.Verdict(v)
		if !valid[vv] {
			return nil, fmt.Errorf("unknown verdict %q in --fail-on", v)
		}
		m[vv] = true
	}
	return m, nil
}

func resolveActorSource(project *config.LoadedProject, actorName string) string {
	source := project.Project.Actors[actorName]
	if source == "" {
		return ""
	}
	if lock.IsLocalSource(source) {
		return filepath.Join(project.ProjectDir, source)
	}
	if lock.IsGitHubSource(source) && project.LockFile != nil {
		_, cacheDir, err := lock.Lookup(project.LockFile, source, project.ProjectDir)
		if err == nil {
			return cacheDir
		}
	}
	return ""
}

func prepareCmdFromDef(def *model.ActorDefinition) string {
	if def.Prepare == nil {
		return ""
	}
	return def.Prepare.Run
}

func prepareCacheKey(def *model.ActorDefinition) []string {
	if def.Prepare == nil {
		return nil
	}
	return def.Prepare.CacheKey
}

func makeSubjectResolver(subjects map[string]string) actor.SubjectResolver {
	return func(exprStr string) (string, error) {
		parts := strings.Split(exprStr, ".")
		if len(parts) == 2 && parts[0] == "subjects" {
			v, ok := subjects[parts[1]]
			if !ok {
				return "", fmt.Errorf("unknown subject %q", parts[1])
			}
			return v, nil
		}
		return "", fmt.Errorf("unsupported expression %q in prepare.run", exprStr)
	}
}

func resolveLinks(scenarioLinks map[string]string, projectLinks map[string]model.Link) map[string]model.LinkResult {
	if len(scenarioLinks) == 0 {
		return nil
	}
	result := make(map[string]model.LinkResult, len(scenarioLinks))
	for name, value := range scenarioLinks {
		lr := model.LinkResult{Value: value}
		if linkDef, ok := projectLinks[name]; ok && linkDef.URL != "" {
			lr.URL = resolveURLTemplate(linkDef.URL, value, linkDef.Pattern)
		}
		result[name] = lr
	}
	return result
}

func resolveURLTemplate(urlTemplate, value, pattern string) string {
	url := strings.ReplaceAll(urlTemplate, "{value}", value)

	// Try to extract named groups from the pattern.
	if pattern != "" {
		re, err := compilePattern(pattern)
		if err == nil {
			matches := re.FindStringSubmatch(value)
			if matches != nil {
				for i, name := range re.SubexpNames() {
					if name != "" && i < len(matches) {
						url = strings.ReplaceAll(url, "{"+name+"}", matches[i])
					}
				}
			}
		}
	}
	return url
}

func compilePattern(pattern string) (*regexp.Regexp, error) {
	// Convert (?<name>...) to Go's (?P<name>...) syntax.
	goPattern := strings.ReplaceAll(pattern, "(?<", "(?P<")
	return regexp.Compile(goPattern)
}

func prepareCheckers(ctx context.Context, cfg *RunConfig) (map[string]*checkerHandle, error) {
	checkers := make(map[string]*checkerHandle)

	// On any error, shut down checkers already started in this call.
	ok := false
	defer func() {
		if !ok {
			shutdownCheckers(ctx, checkers)
		}
	}()

	for name, def := range cfg.Project.Checkers {
		source := cfg.Project.CheckerSources[name]
		sourceDir := resolveCheckerSource(cfg.Project, source)
		if sourceDir == "" {
			continue
		}

		prepResult, err := actor.Prepare(ctx, &actor.PrepareConfig{
			SourceDir:        sourceDir,
			CacheDir:         cfg.CacheDir,
			Tools:            def.Tools,
			PrepareRun:       prepareCmdFromDef(def),
			CacheKeySubjects: prepareCacheKey(def),
			Subjects:         cfg.Subjects,
			Resolver:         makeSubjectResolver(cfg.Subjects),
		})
		if err != nil {
			return nil, fmt.Errorf("prepare checker %q: %w", name, err)
		}

		// Start the checker process.
		tmpDir, err := os.MkdirTemp("", "scraps-checker-*")
		if err != nil {
			return nil, fmt.Errorf("checker tmpdir: %w", err)
		}
		logPath := filepath.Join(tmpDir, name+".log")

		proc, err := actor.Start(ctx, &actor.StartConfig{
			Tools:     def.Tools,
			Run:       def.Run,
			Dir:       prepResult.Dir,
			Workdir:   tmpDir,
			Artefacts: tmpDir,
			LogPath:   logPath,
		})
		if err != nil {
			return nil, fmt.Errorf("start checker %q: %w", name, err)
		}

		// Initialize the checker.
		initCtx, initCancel := context.WithTimeout(ctx, defaultStepTimeout)
		_, err = proc.Client().Initialize(initCtx, &protocol.InitializeParams{
			Protocol:  protocol.ProtocolVersion,
			Name:      name,
			Kind:      def.Kind,
			Inputs:    make(map[string]any),
			Subjects:  cfg.Subjects,
			Seed:      cfg.Seed,
			Workdir:   tmpDir,
			Artefacts: tmpDir,
		})
		initCancel()
		if err != nil {
			_ = proc.Stop(ctx, shutdownTimeout, termTimeout)
			return nil, fmt.Errorf("initialize checker %q: %w", name, err)
		}

		checkers[name] = &checkerHandle{
			process: proc,
			def:     def,
		}
	}

	ok = true
	return checkers, nil
}

func resolveCheckerSource(project *config.LoadedProject, source string) string {
	if source == "" {
		return ""
	}
	if lock.IsLocalSource(source) {
		return filepath.Join(project.ProjectDir, source)
	}
	if lock.IsGitHubSource(source) && project.LockFile != nil {
		_, cacheDir, err := lock.Lookup(project.LockFile, source, project.ProjectDir)
		if err == nil {
			return cacheDir
		}
	}
	return ""
}

func shutdownCheckers(ctx context.Context, checkers map[string]*checkerHandle) {
	for _, ch := range checkers {
		if ch.process != nil {
			_ = ch.process.Stop(ctx, shutdownTimeout, termTimeout)
		}
	}
}

// restartBrokenCheckers replaces any checker whose client has latched broken
// with a fresh process, so a failure in one scenario does not infect the next.
func restartBrokenCheckers(ctx context.Context, cfg *RunConfig, checkers map[string]*checkerHandle) error {
	for name, ch := range checkers {
		if ch.process == nil || ch.process.Client().IsBroken() == nil {
			continue
		}

		// Stop the broken process.
		_ = ch.process.Stop(ctx, shutdownTimeout, termTimeout)

		source := cfg.Project.CheckerSources[name]
		sourceDir := resolveCheckerSource(cfg.Project, source)
		if sourceDir == "" {
			return fmt.Errorf("checker %q: cannot resolve source", name)
		}

		prepResult, err := actor.Prepare(ctx, &actor.PrepareConfig{
			SourceDir:        sourceDir,
			CacheDir:         cfg.CacheDir,
			Tools:            ch.def.Tools,
			PrepareRun:       prepareCmdFromDef(ch.def),
			CacheKeySubjects: prepareCacheKey(ch.def),
			Subjects:         cfg.Subjects,
			Resolver:         makeSubjectResolver(cfg.Subjects),
		})
		if err != nil {
			return fmt.Errorf("prepare checker %q: %w", name, err)
		}

		tmpDir, err := os.MkdirTemp("", "scraps-checker-*")
		if err != nil {
			return fmt.Errorf("checker tmpdir: %w", err)
		}
		logPath := filepath.Join(tmpDir, name+".log")

		proc, err := actor.Start(ctx, &actor.StartConfig{
			Tools:     ch.def.Tools,
			Run:       ch.def.Run,
			Dir:       prepResult.Dir,
			Workdir:   tmpDir,
			Artefacts: tmpDir,
			LogPath:   logPath,
		})
		if err != nil {
			return fmt.Errorf("start checker %q: %w", name, err)
		}

		initCtx, initCancel := context.WithTimeout(ctx, defaultStepTimeout)
		_, err = proc.Client().Initialize(initCtx, &protocol.InitializeParams{
			Protocol:  protocol.ProtocolVersion,
			Name:      name,
			Kind:      ch.def.Kind,
			Inputs:    make(map[string]any),
			Subjects:  cfg.Subjects,
			Seed:      cfg.Seed,
			Workdir:   tmpDir,
			Artefacts: tmpDir,
		})
		initCancel()
		if err != nil {
			_ = proc.Stop(ctx, shutdownTimeout, termTimeout)
			return fmt.Errorf("initialize checker %q: %w", name, err)
		}

		ch.process = proc
	}
	return nil
}

func brokenResult(scenario model.Scenario, cfg *RunConfig, start time.Time, reason string) model.ScenarioResult {
	histResult, _ := verdict.EvalHistory(
		scenario.History,
		cfg.Project.Project.Subjects,
		cfg.Subjects,
	)
	return model.ScenarioResult{
		ID:             scenario.ID,
		Title:          scenario.Title,
		Expected:       histResult.Expected,
		Verdict:        model.VerdictBroken,
		MatchedHistory: histResult.Index,
		DurationMs:     time.Since(start).Milliseconds(),
		Reason:         reason,
	}
}

func jsonDeepEqual(a, b any) bool {
	// Normalize numeric types for comparison.
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}
