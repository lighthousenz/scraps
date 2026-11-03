package model

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestOutcomeUnmarshal_StepErrorWithAnchor(t *testing.T) {
	// Verify that YAML anchors in step-error matchers are resolved correctly.
	// A re-marshal approach would break because the alias lands outside the
	// subtree, so the fresh decoder would fail with "unknown anchor".
	input := `
outcomes:
  npe:
    step-error: &npe_match
      actor: bob
      message: {matches: "Cannot read"}
  retry:
    step-error: *npe_match
`
	var doc struct {
		Outcomes map[string]Outcome `yaml:"outcomes"`
	}
	if err := yaml.Unmarshal([]byte(input), &doc); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	npe := doc.Outcomes["npe"]
	if len(npe) != 1 || npe[0].StepError == nil {
		t.Fatal("expected npe outcome with step-error")
	}
	if npe[0].StepError.Actor != "bob" {
		t.Errorf("npe actor = %q, want bob", npe[0].StepError.Actor)
	}

	retry := doc.Outcomes["retry"]
	if len(retry) != 1 || retry[0].StepError == nil {
		t.Fatal("expected retry outcome with step-error")
	}
	if retry[0].StepError.Actor != "bob" {
		t.Errorf("retry actor = %q, want bob", retry[0].StepError.Actor)
	}
}

func TestOutcomeUnmarshal_StepErrorUnknownKey(t *testing.T) {
	// Verify that unknown keys in step-error matchers are still rejected.
	input := `
step-error:
  actor: bob
  mesage: {matches: "typo"}
`
	var outcome Outcome
	if err := yaml.Unmarshal([]byte(input), &outcome); err == nil {
		t.Error("expected error for unknown key 'mesage', got nil")
	}
}
