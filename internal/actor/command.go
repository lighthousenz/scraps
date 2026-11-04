package actor

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
)

// ErrMiseNotFound indicates mise is not on PATH when an actor needs it.
var ErrMiseNotFound = errors.New("mise not found on PATH: actors with tools require mise")

// BuildCommand returns the exec.Cmd arguments for running an actor.
// If the definition has tools, it wraps with mise exec.
// The returned command is not yet started; callers set Dir, Env, etc.
func BuildCommand(tools map[string]string, run string) (name string, args []string, err error) {
	if len(tools) == 0 {
		return "sh", []string{"-c", run}, nil
	}

	misePath, err := exec.LookPath("mise")
	if err != nil {
		return "", nil, ErrMiseNotFound
	}

	// Sort tools by name for deterministic command lines.
	type toolEntry struct {
		name    string
		version string
	}
	sorted := make([]toolEntry, 0, len(tools))
	for name, version := range tools {
		sorted = append(sorted, toolEntry{name, version})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].name < sorted[j].name
	})

	args = []string{"exec"}
	for _, t := range sorted {
		args = append(args, fmt.Sprintf("%s@%s", t.name, t.version))
	}
	args = append(args, "--", "sh", "-c", run)

	return misePath, args, nil
}
