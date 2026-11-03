package model

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// UnmarshalYAML decodes a step from its YAML representation.
// A step is a mapping with exactly one key (the target actors) and a value
// that is either a string (operation with no params) or a mapping with one key
// (the operation name) whose value is the params.
func (s *Step) UnmarshalYAML(node *yaml.Node) error {
	s.Node = node
	if node.Kind != yaml.MappingNode || len(node.Content) != 2 {
		return fmt.Errorf("step must be a mapping with exactly one key")
	}

	targetNode := node.Content[0]
	valueNode := node.Content[1]

	// Parse targets: comma-separated actor names.
	rawTargets := targetNode.Value
	parts := strings.Split(rawTargets, ",")
	s.Targets = make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			s.Targets = append(s.Targets, t)
		}
	}
	if len(s.Targets) == 0 {
		return fmt.Errorf("step has no targets")
	}

	switch valueNode.Kind {
	case yaml.ScalarNode:
		// Simple operation: "pause-sync" or "start" or "stop"
		s.Operation = valueNode.Value
		s.Params = nil

	case yaml.MappingNode:
		if len(valueNode.Content) != 2 {
			return fmt.Errorf("step operation mapping must have exactly one key")
		}
		opName := valueNode.Content[0].Value
		paramNode := valueNode.Content[1]

		if opName == "await" {
			// await takes a condition name as its value
			s.Operation = "await"
			s.Params = map[string]any{"condition": paramNode.Value}
		} else {
			s.Operation = opName
			if paramNode.Kind == yaml.MappingNode {
				var params map[string]any
				if err := paramNode.Decode(&params); err != nil {
					return fmt.Errorf("decoding params for %q: %w", opName, err)
				}
				s.Params = params
			} else {
				// Single value param
				var val any
				if err := paramNode.Decode(&val); err != nil {
					return fmt.Errorf("decoding param value for %q: %w", opName, err)
				}
				s.Params = map[string]any{opName: val}
			}
		}

	default:
		return fmt.Errorf("step value must be a string or mapping")
	}

	return nil
}

// UnmarshalYAML decodes an outcome from YAML. An outcome is either a mapping of
// artefact -> checks, or a single "step-error" key.
func (o *Outcome) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("outcome must be a mapping")
	}
	var checks []Check
	for i := 0; i < len(node.Content)-1; i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		if keyNode.Value == "step-error" {
			var matcher StepErrorMatcher
			if err := valNode.Decode(&matcher); err != nil {
				return fmt.Errorf("decoding step-error: %w", err)
			}
			checks = append(checks, Check{
				StepError: &matcher,
				Node:      keyNode,
			})
		} else {
			// artefact: { checkName: value, ... }
			artefactName := keyNode.Value
			if valNode.Kind != yaml.MappingNode {
				return fmt.Errorf("outcome check for artefact %q must be a mapping", artefactName)
			}
			for j := 0; j < len(valNode.Content)-1; j += 2 {
				checkNameNode := valNode.Content[j]
				checkValNode := valNode.Content[j+1]
				var checkVal any
				if err := checkValNode.Decode(&checkVal); err != nil {
					return fmt.Errorf("decoding check value: %w", err)
				}
				checks = append(checks, Check{
					Artefact:  artefactName,
					CheckName: checkNameNode.Value,
					Value:     checkVal,
					Node:      checkNameNode,
				})
			}
		}
	}
	*o = checks
	return nil
}
