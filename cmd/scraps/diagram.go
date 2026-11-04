package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/lighthousenz/scraps/internal/config"
	"github.com/lighthousenz/scraps/internal/graph"
	"github.com/lighthousenz/scraps/internal/report"
)

func cmdDiagram(args []string) int {
	fs := flag.NewFlagSet("scraps diagram", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: scraps diagram <scenario>")
		return 2
	}
	scenarioPath := fs.Arg(0)

	// Load project.
	lp, loadProblems := config.LoadProject([]string{scenarioPath})
	if lp == nil {
		for _, p := range loadProblems {
			fmt.Fprintln(os.Stderr, p)
		}
		return 2
	}

	if len(lp.Scenarios) == 0 {
		fmt.Fprintln(os.Stderr, "no scenarios found")
		return 2
	}

	scenario := lp.Scenarios[0].Scenario

	// Build the dependency graph to compute cascade orders for stop/start steps.
	actorNames := make([]string, 0, len(scenario.Actors))
	deps := make(map[string][]string)
	for name := range scenario.Actors {
		actorNames = append(actorNames, name)
	}
	sort.Strings(actorNames)

	for name, sa := range scenario.Actors {
		actorDef := lp.Actors[sa.Uses]
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

	g := graph.New(actorNames, deps)

	// Compute cascade orders for stop steps.
	cascadeOrders := make(map[int][]string)
	for i, step := range scenario.Steps {
		if step.Operation == "stop" {
			var cascade []string
			for _, target := range step.Targets {
				c := g.StopCascade(target)
				cascade = append(cascade, c...)
				cascade = append(cascade, target)
			}
			if len(cascade) > 1 {
				cascadeOrders[i] = cascade
			}
		}
	}

	// Build actor order from scenario declaration.
	opts := report.MermaidOpts{
		ActorOrder:    actorNames,
		CascadeOrders: cascadeOrders,
	}

	if err := report.Mermaid(os.Stdout, &scenario, opts); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		return 3
	}

	return 0
}
