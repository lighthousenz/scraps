package model

// Verdict classifies a run's actual outcome against the expected one.
type Verdict string

const (
	VerdictAsExpected   Verdict = "as-expected"
	VerdictFixed        Verdict = "fixed"
	VerdictRegressed    Verdict = "regressed"
	VerdictChanged      Verdict = "changed"
	VerdictUnrecognised Verdict = "unrecognised"
	VerdictBroken       Verdict = "broken"
)

// ScenarioResult is the per-scenario entry in a run report.
type ScenarioResult struct {
	ID             string                `json:"id"`
	Title          string                `json:"title"`
	Links          map[string]LinkResult `json:"links,omitempty"`
	Tags           []string              `json:"tags,omitempty"`
	Expected       string                `json:"expected"`
	Actual         string                `json:"actual"`
	Verdict        Verdict               `json:"verdict"`
	MatchedHistory int                   `json:"matched-history"`
	DurationMs     int64                 `json:"duration-ms"`
	Artefacts      string                `json:"artefacts"`
	Reason         string                `json:"reason,omitempty"`
	Overlapping    []string              `json:"overlapping,omitempty"`
}

// LinkResult is a resolved link in a scenario result.
type LinkResult struct {
	Value string `json:"value"`
	URL   string `json:"url"`
}

// RunReport is the top-level report for a scraps run.
type RunReport struct {
	Schema   int               `json:"schema"`
	Scraps   string            `json:"scraps"`
	Seed     int64             `json:"seed"`
	Subjects map[string]string `json:"subjects"`
	Results  []ScenarioResult  `json:"results"`
}
