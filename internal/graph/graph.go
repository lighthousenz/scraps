// Package graph computes the actor dependency graph from actor-typed inputs.
package graph

import (
	"fmt"
	"sort"
	"strings"
)

// Graph holds the dependency edges between actors. Edges run from dependent to dependency.
type Graph struct {
	nodes []string
	edges map[string][]string // dependent -> dependencies
	rev   map[string][]string // dependency -> dependents
}

// New builds a graph from actors and their dependencies.
// deps maps actor name -> list of actor names it depends on.
func New(actors []string, deps map[string][]string) *Graph {
	g := &Graph{
		nodes: actors,
		edges: make(map[string][]string, len(actors)),
		rev:   make(map[string][]string, len(actors)),
	}
	for _, a := range actors {
		if d, ok := deps[a]; ok {
			g.edges[a] = d
			for _, dep := range d {
				g.rev[dep] = append(g.rev[dep], a)
			}
		}
	}
	return g
}

// DetectCycle returns the names of actors in a cycle, or nil if acyclic.
func (g *Graph) DetectCycle() []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(g.nodes))
	parent := make(map[string]string, len(g.nodes))

	// Sort nodes for deterministic output.
	sorted := make([]string, len(g.nodes))
	copy(sorted, g.nodes)
	sort.Strings(sorted)

	var cycle []string
	var dfs func(u string) bool
	dfs = func(u string) bool {
		color[u] = gray
		deps := make([]string, len(g.edges[u]))
		copy(deps, g.edges[u])
		sort.Strings(deps)
		for _, v := range deps {
			if color[v] == gray {
				// Found cycle: trace back from u to v.
				cycle = []string{v, u}
				cur := u
				for cur != v {
					cur = parent[cur]
					if cur == v {
						break
					}
					cycle = append(cycle, cur)
				}
				// Reverse to get the cycle in order.
				for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				return true
			}
			if color[v] == white {
				parent[v] = u
				if dfs(v) {
					return true
				}
			}
		}
		color[u] = black
		return false
	}

	for _, n := range sorted {
		if color[n] == white {
			if dfs(n) {
				return cycle
			}
		}
	}
	return nil
}

// CycleError formats a cycle as a human-readable error.
func CycleError(cycle []string) string {
	return fmt.Sprintf("dependency cycle: %s", strings.Join(append(cycle, cycle[0]), " → "))
}

// StartLayers returns topological layers. Each layer's members can start in parallel.
// Layers are ordered: layer 0 has no dependencies; layer N depends only on earlier layers.
func (g *Graph) StartLayers() ([][]string, error) {
	if cycle := g.DetectCycle(); cycle != nil {
		return nil, fmt.Errorf("%s", CycleError(cycle))
	}

	inDegree := make(map[string]int, len(g.nodes))
	for _, n := range g.nodes {
		inDegree[n] = len(g.edges[n])
	}

	var layers [][]string
	remaining := make(map[string]bool, len(g.nodes))
	for _, n := range g.nodes {
		remaining[n] = true
	}

	for len(remaining) > 0 {
		var layer []string
		for n := range remaining {
			if inDegree[n] == 0 {
				layer = append(layer, n)
			}
		}
		if len(layer) == 0 {
			break // should not happen if DetectCycle passed
		}
		sort.Strings(layer)
		layers = append(layers, layer)
		for _, n := range layer {
			delete(remaining, n)
			for _, dep := range g.rev[n] {
				inDegree[dep]--
			}
		}
	}
	return layers, nil
}

// StopOrder returns the reverse of the start order: a flat list of actors
// ordered so that dependents come before their dependencies.
func (g *Graph) StopOrder() ([]string, error) {
	layers, err := g.StartLayers()
	if err != nil {
		return nil, err
	}
	var order []string
	for i := len(layers) - 1; i >= 0; i-- {
		// Within a layer, reverse alphabetical for stop order.
		layer := make([]string, len(layers[i]))
		copy(layer, layers[i])
		sort.Sort(sort.Reverse(sort.StringSlice(layer)))
		order = append(order, layer...)
	}
	return order, nil
}

// StopCascade returns the running dependents that must stop first (in order)
// when stopping actor x. This walks the reverse edges (dependents), recursively.
// Outermost dependents (leaves of the reverse graph) come first.
func (g *Graph) StopCascade(x string) []string {
	visited := make(map[string]bool)
	var result []string
	g.collectDependents(x, visited, &result)
	return result
}

func (g *Graph) collectDependents(x string, visited map[string]bool, result *[]string) {
	dependents := make([]string, len(g.rev[x]))
	copy(dependents, g.rev[x])
	sort.Strings(dependents)
	for _, d := range dependents {
		if visited[d] {
			continue
		}
		visited[d] = true
		g.collectDependents(d, visited, result)
		*result = append(*result, d)
	}
}

// StartCascade returns the stopped dependencies that must start first (in order)
// when starting actor x. Walks the edges (dependencies) recursively, in topological order.
func (g *Graph) StartCascade(x string) []string {
	visited := make(map[string]bool)
	var result []string
	g.collectDependencies(x, visited, &result)
	return result
}

func (g *Graph) collectDependencies(x string, visited map[string]bool, result *[]string) {
	deps := make([]string, len(g.edges[x]))
	copy(deps, g.edges[x])
	sort.Strings(deps)
	for _, d := range deps {
		if visited[d] {
			continue
		}
		visited[d] = true
		g.collectDependencies(d, visited, result)
		*result = append(*result, d)
	}
}
