package graph

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Diamond graph:
//
//	  A
//	 / \
//	B   C
//	 \ /
//	  D
func diamondGraph() *Graph {
	return New(
		[]string{"A", "B", "C", "D"},
		map[string][]string{
			"B": {"A"},
			"C": {"A"},
			"D": {"B", "C"},
		},
	)
}

func TestDetectCycle_NoCycle(t *testing.T) {
	g := diamondGraph()
	if cycle := g.DetectCycle(); cycle != nil {
		t.Errorf("expected no cycle, got %v", cycle)
	}
}

func TestDetectCycle_WithCycle(t *testing.T) {
	g := New(
		[]string{"A", "B", "C"},
		map[string][]string{
			"A": {"C"},
			"B": {"A"},
			"C": {"B"},
		},
	)
	cycle := g.DetectCycle()
	if cycle == nil {
		t.Fatal("expected a cycle")
	}
	if len(cycle) < 2 {
		t.Errorf("cycle too short: %v", cycle)
	}
}

func TestCycleError(t *testing.T) {
	msg := CycleError([]string{"A", "B", "C"})
	want := "dependency cycle: A → B → C → A"
	if msg != want {
		t.Errorf("CycleError = %q, want %q", msg, want)
	}
}

func TestStartLayers_Diamond(t *testing.T) {
	g := diamondGraph()
	layers, err := g.StartLayers()
	if err != nil {
		t.Fatal(err)
	}

	want := [][]string{
		{"A"},
		{"B", "C"},
		{"D"},
	}
	if diff := cmp.Diff(want, layers); diff != "" {
		t.Errorf("StartLayers mismatch (-want +got):\n%s", diff)
	}
}

func TestStopOrder_Diamond(t *testing.T) {
	g := diamondGraph()
	order, err := g.StopOrder()
	if err != nil {
		t.Fatal(err)
	}

	// D first, then B and C (reversed alphabetically), then A
	want := []string{"D", "C", "B", "A"}
	if diff := cmp.Diff(want, order); diff != "" {
		t.Errorf("StopOrder mismatch (-want +got):\n%s", diff)
	}
}

func TestStopCascade_Diamond(t *testing.T) {
	g := diamondGraph()

	// Stopping A should first stop D, then B, then C
	cascade := g.StopCascade("A")
	// Dependents of A: B and C. Dependents of B: D. Dependents of C: D.
	// So cascade should be D, then B and C
	if len(cascade) != 3 {
		t.Fatalf("StopCascade(A) = %v, want 3 elements", cascade)
	}
	// D should come before B and C
	dIdx := indexOf(cascade, "D")
	bIdx := indexOf(cascade, "B")
	cIdx := indexOf(cascade, "C")
	if dIdx > bIdx || dIdx > cIdx {
		t.Errorf("D should come before B and C: %v", cascade)
	}

	// Stopping D has no cascade
	cascade = g.StopCascade("D")
	if len(cascade) != 0 {
		t.Errorf("StopCascade(D) = %v, want empty", cascade)
	}
}

func TestStartCascade_Diamond(t *testing.T) {
	g := diamondGraph()

	// Starting D requires A, B, C
	cascade := g.StartCascade("D")
	if len(cascade) != 3 {
		t.Fatalf("StartCascade(D) = %v, want 3 elements", cascade)
	}
	// A should come before B and C
	aIdx := indexOf(cascade, "A")
	bIdx := indexOf(cascade, "B")
	cIdx := indexOf(cascade, "C")
	if aIdx > bIdx || aIdx > cIdx {
		t.Errorf("A should come before B and C: %v", cascade)
	}

	// Starting A has no dependencies
	cascade = g.StartCascade("A")
	if len(cascade) != 0 {
		t.Errorf("StartCascade(A) = %v, want empty", cascade)
	}
}

func TestStartLayers_NoDeps(t *testing.T) {
	g := New([]string{"x", "y", "z"}, nil)
	layers, err := g.StartLayers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 {
		t.Fatalf("expected 1 layer, got %d: %v", len(layers), layers)
	}
	want := []string{"x", "y", "z"}
	if diff := cmp.Diff(want, layers[0]); diff != "" {
		t.Errorf("layer mismatch (-want +got):\n%s", diff)
	}
}

func indexOf(sl []string, s string) int {
	for i, v := range sl {
		if v == s {
			return i
		}
	}
	return -1
}
