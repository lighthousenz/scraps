package model

import "go.yaml.in/yaml/v3"

// Scenario represents a single scenario.yaml file.
type Scenario struct {
	ID       string                   `yaml:"id"       json:"id"`
	Title    string                   `yaml:"title"    json:"title"`
	Symptom  string                   `yaml:"symptom"  json:"symptom,omitempty"`
	Links    map[string]string        `yaml:"links"    json:"links,omitempty"`
	Tags     []string                 `yaml:"tags"     json:"tags,omitempty"`
	Fixtures map[string]string        `yaml:"fixtures" json:"fixtures,omitempty"`
	Actors   map[string]ScenarioActor `yaml:"actors"   json:"actors"`
	Steps    []Step                   `yaml:"steps"    json:"steps"`
	Outcomes map[string]Outcome       `yaml:"outcomes" json:"outcomes"`
	History  []HistoryEntry           `yaml:"history"  json:"history,omitempty"`
	Node     *yaml.Node               `yaml:"-"        json:"-"`
}

// ScenarioActor binds a participant name to an actor definition.
type ScenarioActor struct {
	Uses  string         `yaml:"uses"  json:"uses"`
	With  map[string]any `yaml:"with"  json:"with,omitempty"`
	Start string         `yaml:"start" json:"start,omitempty"`
	Node  *yaml.Node     `yaml:"-"     json:"-"`
}

// Step is one line of the scenario timeline.
type Step struct {
	Targets   []string       `yaml:"-" json:"-"`
	Operation string         `yaml:"-" json:"-"`
	Params    map[string]any `yaml:"-" json:"-"`
	Node      *yaml.Node     `yaml:"-" json:"-"`
}

// Outcome is an ordered list of checks for a named end state.
type Outcome []Check

// Check is a single check within an outcome.
type Check struct {
	Artefact  string `yaml:"-" json:"-"`
	CheckName string `yaml:"-" json:"-"`
	Value     any    `yaml:"-" json:"-"`

	// StepError is set for step-error matchers.
	StepError *StepErrorMatcher `yaml:"-" json:"-"`

	Node *yaml.Node `yaml:"-" json:"-"`
}

// StepErrorMatcher matches a step failure.
type StepErrorMatcher struct {
	Actor     string `yaml:"actor,omitempty"     json:"actor,omitempty"`
	Operation string `yaml:"operation,omitempty" json:"operation,omitempty"`
	Message   any    `yaml:"message,omitempty"   json:"message,omitempty"`
}

// HistoryEntry maps version conditions to an expected outcome.
type HistoryEntry struct {
	When   map[string]string `yaml:"when"   json:"when"`
	Expect string            `yaml:"expect" json:"expect"`
	Node   *yaml.Node        `yaml:"-"      json:"-"`
}
