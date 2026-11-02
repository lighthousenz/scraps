package model

// Project represents the top-level scraps.yaml configuration.
type Project struct {
	Version  int                `yaml:"version"  json:"version"`
	Subjects map[string]Subject `yaml:"subjects" json:"subjects"`
	Links    map[string]Link    `yaml:"links"    json:"links,omitempty"`
	Actors   map[string]string  `yaml:"actors"   json:"actors,omitempty"`
	Checkers []string           `yaml:"checkers" json:"checkers,omitempty"`
	Defaults Defaults           `yaml:"defaults" json:"defaults,omitempty"`
}

// Subject is a versioned dependency under test.
type Subject struct {
	Scheme  string        `yaml:"scheme"  json:"scheme"`
	Resolve SubjectSource `yaml:"resolve" json:"resolve"`
}

// SubjectSource describes how to resolve a subject version.
type SubjectSource struct {
	Env   string `yaml:"env,omitempty"   json:"env,omitempty"`
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
}

// Link defines a link type that scenarios can carry.
type Link struct {
	Pattern      string        `yaml:"pattern"       json:"pattern"`
	URL          string        `yaml:"url"            json:"url"`
	Required     bool          `yaml:"required"       json:"required,omitempty"`
	RequiredWhen *RequiredWhen `yaml:"required-when"  json:"required-when,omitempty"`
}

// RequiredWhen is a conditional link requirement.
type RequiredWhen struct {
	ExpectsNot string `yaml:"expects-not" json:"expects-not"`
}

// Defaults holds project-wide default values.
type Defaults struct {
	StepTimeout  string   `yaml:"step-timeout"  json:"step-timeout,omitempty"`
	ReadyTimeout string   `yaml:"ready-timeout" json:"ready-timeout,omitempty"`
	FailOn       []string `yaml:"fail-on"       json:"fail-on,omitempty"`
}
