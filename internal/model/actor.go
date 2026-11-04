package model

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// ActorDefinition represents an actor.yaml file.
type ActorDefinition struct {
	Name        string                  `yaml:"name"        json:"name"`
	Kind        string                  `yaml:"kind"        json:"kind,omitempty"`
	Description string                  `yaml:"description" json:"description,omitempty"`
	Role        string                  `yaml:"role"        json:"role,omitempty"`
	Protocol    int                     `yaml:"protocol"    json:"protocol"`
	Tools       map[string]string       `yaml:"tools"       json:"tools,omitempty"`
	Prepare     *Prepare                `yaml:"prepare"     json:"prepare,omitempty"`
	Run         string                  `yaml:"run"         json:"run"`
	Inputs      map[string]InputDef     `yaml:"inputs"      json:"inputs,omitempty"`
	Outputs     map[string]OutputDef    `yaml:"outputs"     json:"outputs,omitempty"`
	Lifecycle   Lifecycle               `yaml:"lifecycle"   json:"lifecycle"`
	Conditions  map[string]ConditionDef `yaml:"conditions"  json:"conditions,omitempty"`
	Operations  map[string]OperationDef `yaml:"operations"  json:"operations,omitempty"`
	Node        *yaml.Node              `yaml:"-"           json:"-"`
}

// Prepare describes the build step that installs the subject version under test.
type Prepare struct {
	Run      string   `yaml:"run"       json:"run"`
	CacheKey []string `yaml:"cache-key" json:"cache-key,omitempty"`
}

// InputDef describes a declared input for an actor.
type InputDef struct {
	Type    string `yaml:"type"              json:"type"`
	Kind    string `yaml:"kind,omitempty"    json:"kind,omitempty"`
	Default any    `yaml:"default,omitempty" json:"default,omitempty"`
}

// OutputDef describes a declared output for an actor.
type OutputDef struct {
	Type string `yaml:"type" json:"type"`
}

// LifecycleEntry is a single lifecycle operation: either a bare string
// (e.g. "disconnect") or a one-key mapping (e.g. {snapshot: {as: final-state}}).
type LifecycleEntry map[string]any

// UnmarshalYAML accepts both a bare scalar and a mapping node.
func (e *LifecycleEntry) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*e = LifecycleEntry{node.Value: nil}
		return nil
	case yaml.MappingNode:
		var m map[string]any
		if err := node.Decode(&m); err != nil {
			return err
		}
		*e = LifecycleEntry(m)
		return nil
	default:
		return fmt.Errorf("lifecycle entry must be a string or mapping, got %v", node.Kind)
	}
}

// Lifecycle describes the phases of an actor's execution.
type Lifecycle struct {
	Start   []LifecycleEntry `yaml:"start"   json:"start,omitempty"`
	Ready   string           `yaml:"ready"   json:"ready,omitempty"`
	Stop    []LifecycleEntry `yaml:"stop"    json:"stop,omitempty"`
	Collect []LifecycleEntry `yaml:"collect" json:"collect,omitempty"`
}

// ConditionDef describes a polled condition.
type ConditionDef struct {
	Call     string         `yaml:"call"               json:"call"`
	Until    map[string]any `yaml:"until,omitempty"    json:"until,omitempty"`
	AllEqual bool           `yaml:"all-equal,omitempty" json:"all-equal,omitempty"`
	Every    string         `yaml:"every,omitempty"    json:"every,omitempty"`
}

// OperationDef describes an operation an actor can perform.
type OperationDef struct {
	Params   map[string]any `yaml:"params,omitempty"   json:"params,omitempty"`
	Returns  map[string]any `yaml:"returns,omitempty"  json:"returns,omitempty"`
	Artefact bool           `yaml:"artefact,omitempty" json:"artefact,omitempty"`
}
