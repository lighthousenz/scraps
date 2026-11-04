package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lighthousenz/scraps/internal/model"
)

// MermaidOpts configures the Mermaid diagram output.
type MermaidOpts struct {
	// ActorOrder is the declaration-order list of actor names for the
	// participant lines. The caller provides this because Go maps do
	// not preserve YAML declaration order.
	ActorOrder []string

	// CascadeOrders maps a step index to the ordered list of actors
	// that must be stopped/started as part of a cascade. The caller
	// computes this from the dependency graph; the renderer just uses it.
	CascadeOrders map[int][]string
}

// Mermaid writes a scenario as a Mermaid sequence diagram.
func Mermaid(w io.Writer, scenario *model.Scenario, opts MermaidOpts) error {
	var b strings.Builder

	b.WriteString("sequenceDiagram\n")

	// Use the caller-provided actor order, or fall back to step-discovery order.
	actorOrder := opts.ActorOrder
	if len(actorOrder) == 0 {
		actorOrder = actorOrderFromSteps(scenario)
	}

	// Build a position index for Note over range computation.
	actorPos := make(map[string]int, len(actorOrder))
	for i, name := range actorOrder {
		actorPos[name] = i
	}

	for _, name := range actorOrder {
		fmt.Fprintf(&b, "  participant %s\n", name)
	}

	for i, step := range scenario.Steps {
		if len(step.Targets) == 1 {
			b.WriteString(renderSingleTarget(step, i, opts, actorPos))
		} else {
			b.WriteString(renderGroupTarget(step, i, opts, actorPos))
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func actorOrderFromSteps(scenario *model.Scenario) []string {
	seen := make(map[string]bool)
	var order []string
	for _, step := range scenario.Steps {
		for _, target := range step.Targets {
			if !seen[target] {
				seen[target] = true
				order = append(order, target)
			}
		}
	}
	var leftover []string
	for name := range scenario.Actors {
		if !seen[name] {
			leftover = append(leftover, name)
		}
	}
	sort.Strings(leftover)
	order = append(order, leftover...)
	return order
}

func renderSingleTarget(step model.Step, idx int, opts MermaidOpts, actorPos map[string]int) string {
	target := step.Targets[0]
	op := step.Operation

	switch op {
	case "start":
		return fmt.Sprintf("  %s->>%s: start\n", target, target)
	case "stop":
		if cascade, ok := opts.CascadeOrders[idx]; ok && len(cascade) > 0 {
			first, last := noteRange(cascade, actorPos)
			return fmt.Sprintf("  Note over %s,%s: stop (%s)\n", first, last,
				joinCascadeNames(cascade))
		}
		return fmt.Sprintf("  %s->>%s: stop\n", target, target)
	case "await":
		cond := conditionName(step)
		return fmt.Sprintf("  %s->>%s: await %s\n", target, target, cond)
	default:
		return fmt.Sprintf("  %s->>%s: %s\n", target, target, renderCall(step))
	}
}

func renderGroupTarget(step model.Step, idx int, opts MermaidOpts, actorPos map[string]int) string {
	first, last := noteRange(step.Targets, actorPos)
	op := step.Operation

	switch op {
	case "start":
		var lines strings.Builder
		for _, target := range step.Targets {
			fmt.Fprintf(&lines, "  %s->>%s: start\n", target, target)
		}
		return lines.String()
	case "stop":
		if cascade, ok := opts.CascadeOrders[idx]; ok && len(cascade) > 0 {
			nf, nl := noteRange(cascade, actorPos)
			return fmt.Sprintf("  Note over %s,%s: stop (%s)\n", nf, nl,
				joinCascadeNames(cascade))
		}
		return fmt.Sprintf("  Note over %s,%s: stop\n", first, last)
	case "await":
		cond := conditionName(step)
		return fmt.Sprintf("  Note over %s,%s: await %s\n", first, last, cond)
	default:
		return fmt.Sprintf("  Note over %s,%s: %s\n", first, last, renderCall(step))
	}
}

func renderCall(step model.Step) string {
	op := step.Operation

	// snapshot { as: x } renders as "snapshot → x".
	if op == "snapshot" {
		if asVal, ok := step.Params["as"]; ok {
			return fmt.Sprintf("snapshot → %v", asVal)
		}
	}

	if len(step.Params) == 0 {
		return op
	}

	// Render params as comma-separated values in sorted key order
	// (declaration order isn't available from a map).
	keys := sortedMapKeys(step.Params)
	vals := make([]string, 0, len(keys))
	for _, k := range keys {
		vals = append(vals, fmtValue(step.Params[k]))
	}
	return fmt.Sprintf("%s(%s)", op, strings.Join(vals, ", "))
}

func fmtValue(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%v", v)
}

func conditionName(step model.Step) string {
	if c, ok := step.Params["condition"]; ok {
		return fmt.Sprintf("%v", c)
	}
	return ""
}

// noteRange returns the leftmost and rightmost participant names (by
// declaration position) for a Mermaid "Note over" spanning a set of actors.
func noteRange(actors []string, actorPos map[string]int) (string, string) {
	if len(actors) == 0 {
		return "", ""
	}
	minName, maxName := actors[0], actors[0]
	minPos, maxPos := actorPos[actors[0]], actorPos[actors[0]]
	for _, a := range actors[1:] {
		p := actorPos[a]
		if p < minPos {
			minPos = p
			minName = a
		}
		if p > maxPos {
			maxPos = p
			maxName = a
		}
	}
	return minName, maxName
}

func joinCascadeNames(actors []string) string {
	if len(actors) <= 2 {
		return strings.Join(actors, ", ")
	}
	return strings.Join(actors[:len(actors)-1], ", ") + ", then " + actors[len(actors)-1]
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
