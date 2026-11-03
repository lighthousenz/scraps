package verdict

import (
	"fmt"

	"github.com/lighthousenz/scraps/internal/model"
)

// ClassifyInput holds the inputs for verdict classification.
type ClassifyInput struct {
	Expected string   // the expected outcome name
	Matched  []string // outcome names whose checks all matched
	Broken   bool     // whether the run was broken
	Reason   string   // reason for being broken
}

// ClassifyResult holds the output of verdict classification.
type ClassifyResult struct {
	Verdict     model.Verdict
	Reason      string   // set for broken
	Overlapping []string // set for unrecognised when multiple matched
}

// Classify determines the verdict for a run.
func Classify(input ClassifyInput) ClassifyResult {
	if input.Broken {
		return ClassifyResult{
			Verdict: model.VerdictBroken,
			Reason:  input.Reason,
		}
	}

	if len(input.Matched) == 0 || len(input.Matched) > 1 {
		return ClassifyResult{
			Verdict:     model.VerdictUnrecognised,
			Overlapping: input.Matched,
		}
	}

	actual := input.Matched[0]
	expected := input.Expected

	if actual == expected {
		return ClassifyResult{Verdict: model.VerdictAsExpected}
	}
	if expected != "correct" && actual == "correct" {
		return ClassifyResult{Verdict: model.VerdictFixed}
	}
	if expected == "correct" && actual != "correct" {
		return ClassifyResult{Verdict: model.VerdictRegressed}
	}
	// Both are non-correct and different.
	return ClassifyResult{Verdict: model.VerdictChanged}
}

// AllVerdicts returns all valid verdict values.
func AllVerdicts() []model.Verdict {
	return []model.Verdict{
		model.VerdictAsExpected,
		model.VerdictFixed,
		model.VerdictRegressed,
		model.VerdictChanged,
		model.VerdictUnrecognised,
		model.VerdictBroken,
	}
}

// DefaultFailOn returns the default set of verdicts that cause exit 1.
func DefaultFailOn() []model.Verdict {
	return []model.Verdict{
		model.VerdictFixed,
		model.VerdictRegressed,
		model.VerdictChanged,
		model.VerdictUnrecognised,
		model.VerdictBroken,
	}
}

// ShouldFail reports whether any of the given verdicts is in the fail-on list.
// An unknown verdict name in failOn is an error.
func ShouldFail(verdicts []model.Verdict, failOn []string) (bool, error) {
	valid := make(map[model.Verdict]bool)
	for _, v := range AllVerdicts() {
		valid[v] = true
	}

	failSet := make(map[model.Verdict]bool)
	for _, name := range failOn {
		v := model.Verdict(name)
		if !valid[v] {
			return false, fmt.Errorf("unknown verdict %q in fail-on list", name)
		}
		failSet[v] = true
	}

	for _, v := range verdicts {
		if failSet[v] {
			return true, nil
		}
	}
	return false, nil
}
