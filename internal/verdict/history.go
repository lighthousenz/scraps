package verdict

import (
	"fmt"
	"regexp"

	"github.com/Masterminds/semver/v3"

	"github.com/lighthousenz/scraps/internal/model"
)

// HistoryResult is the output of evaluating a scenario's history.
type HistoryResult struct {
	Expected string // the expected outcome name
	Index    int    // index of the matching entry, or -1
}

// EvalHistory evaluates a scenario's history entries against the resolved
// subject versions and returns the expected outcome.
//
// subjects maps subject name to its declared scheme.
// versions maps subject name to its resolved version string.
func EvalHistory(
	history []model.HistoryEntry,
	subjects map[string]model.Subject,
	versions map[string]string,
) (HistoryResult, error) {
	for i, entry := range history {
		matched, err := matchEntry(entry, subjects, versions)
		if err != nil {
			return HistoryResult{}, fmt.Errorf("history[%d]: %w", i, err)
		}
		if matched {
			return HistoryResult{Expected: entry.Expect, Index: i}, nil
		}
	}
	return HistoryResult{Expected: "correct", Index: -1}, nil
}

func matchEntry(
	entry model.HistoryEntry,
	subjects map[string]model.Subject,
	versions map[string]string,
) (bool, error) {
	for name, constraint := range entry.When {
		subj, ok := subjects[name]
		if !ok {
			return false, fmt.Errorf("unknown subject %q in when", name)
		}
		ver, ok := versions[name]
		if !ok {
			return false, fmt.Errorf("subject %q has no resolved version", name)
		}
		matched, err := matchSubject(subj.Scheme, constraint, ver)
		if err != nil {
			return false, fmt.Errorf("subject %q: %w", name, err)
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func matchSubject(scheme, constraint, version string) (bool, error) {
	switch scheme {
	case "semver":
		return matchSemver(constraint, version)
	case "exact":
		return constraint == version, nil
	default:
		return false, fmt.Errorf("unknown scheme %q", scheme)
	}
}

func matchSemver(constraint, version string) (bool, error) {
	v, err := semver.NewVersion(version)
	if err != nil {
		return false, fmt.Errorf("invalid semver version %q: %w", version, err)
	}
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return false, fmt.Errorf("invalid semver constraint %q: %w", constraint, err)
	}
	return c.Check(v), nil
}

// StepFailure records a step failure during a run.
type StepFailure struct {
	Actor     string
	Operation string
	Message   string
	Data      any
}

// MatchStepError checks whether a step-error matcher matches a recorded failure.
// If failure is nil, the matcher does not match.
func MatchStepError(matcher *model.StepErrorMatcher, failure *StepFailure) (bool, error) {
	if failure == nil {
		return false, nil
	}
	if matcher.Actor != "" && matcher.Actor != failure.Actor {
		return false, nil
	}
	if matcher.Operation != "" && matcher.Operation != failure.Operation {
		return false, nil
	}
	if matcher.Message != nil {
		matched, err := matchMessage(matcher.Message, failure.Message)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func matchMessage(pattern any, message string) (bool, error) {
	switch v := pattern.(type) {
	case string:
		return v == message, nil
	case map[string]any:
		raw, ok := v["matches"]
		if !ok {
			return false, fmt.Errorf("step-error message: expected string or {matches: regex}")
		}
		regex, ok := raw.(string)
		if !ok {
			return false, fmt.Errorf("step-error message.matches: expected string, got %T", raw)
		}
		re, err := regexp.Compile(regex)
		if err != nil {
			return false, fmt.Errorf("step-error message.matches: invalid regex %q: %w", regex, err)
		}
		return re.MatchString(message), nil
	default:
		return false, fmt.Errorf("step-error message: expected string or {matches: regex}, got %T", pattern)
	}
}
