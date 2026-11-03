package verdict_test

import (
	"testing"

	"github.com/lighthousenz/scraps/internal/model"
	"github.com/lighthousenz/scraps/internal/verdict"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name        string
		input       verdict.ClassifyInput
		wantVerdict model.Verdict
		wantReason  string
		wantOverlap []string
	}{
		{
			name: "broken ignores outcomes",
			input: verdict.ClassifyInput{
				Expected: "correct",
				Matched:  []string{"correct"},
				Broken:   true,
				Reason:   "actor crashed",
			},
			wantVerdict: model.VerdictBroken,
			wantReason:  "actor crashed",
		},
		{
			name: "no outcome matched",
			input: verdict.ClassifyInput{
				Expected: "correct",
				Matched:  nil,
			},
			wantVerdict: model.VerdictUnrecognised,
			wantOverlap: nil,
		},
		{
			name: "more than one outcome matched",
			input: verdict.ClassifyInput{
				Expected: "correct",
				Matched:  []string{"correct", "shouts"},
			},
			wantVerdict: model.VerdictUnrecognised,
			wantOverlap: []string{"correct", "shouts"},
		},
		{
			name: "as-expected: correct matches correct",
			input: verdict.ClassifyInput{
				Expected: "correct",
				Matched:  []string{"correct"},
			},
			wantVerdict: model.VerdictAsExpected,
		},
		{
			name: "as-expected: failure matches same failure",
			input: verdict.ClassifyInput{
				Expected: "drops-one",
				Matched:  []string{"drops-one"},
			},
			wantVerdict: model.VerdictAsExpected,
		},
		{
			name: "fixed: expected failure, got correct",
			input: verdict.ClassifyInput{
				Expected: "drops-one",
				Matched:  []string{"correct"},
			},
			wantVerdict: model.VerdictFixed,
		},
		{
			name: "regressed: expected correct, got failure",
			input: verdict.ClassifyInput{
				Expected: "correct",
				Matched:  []string{"drops-one"},
			},
			wantVerdict: model.VerdictRegressed,
		},
		{
			name: "changed: expected one failure, got different failure",
			input: verdict.ClassifyInput{
				Expected: "drops-one",
				Matched:  []string{"shouts"},
			},
			wantVerdict: model.VerdictChanged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := verdict.Classify(tt.input)
			if got.Verdict != tt.wantVerdict {
				t.Errorf("verdict: got %q, want %q", got.Verdict, tt.wantVerdict)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("reason: got %q, want %q", got.Reason, tt.wantReason)
			}
			if len(tt.wantOverlap) > 0 {
				if len(got.Overlapping) != len(tt.wantOverlap) {
					t.Errorf("overlapping: got %v, want %v", got.Overlapping, tt.wantOverlap)
				} else {
					for i, v := range tt.wantOverlap {
						if got.Overlapping[i] != v {
							t.Errorf("overlapping[%d]: got %q, want %q", i, got.Overlapping[i], v)
						}
					}
				}
			}
		})
	}
}

func TestShouldFail(t *testing.T) {
	tests := []struct {
		name     string
		verdicts []model.Verdict
		failOn   []string
		want     bool
		wantErr  bool
	}{
		{
			name:     "as-expected is not in default fail-on",
			verdicts: []model.Verdict{model.VerdictAsExpected},
			failOn:   []string{"fixed", "regressed", "changed", "unrecognised", "broken"},
			want:     false,
		},
		{
			name:     "regressed triggers default fail-on",
			verdicts: []model.Verdict{model.VerdictRegressed},
			failOn:   []string{"fixed", "regressed", "changed", "unrecognised", "broken"},
			want:     true,
		},
		{
			name:     "fixed does not trigger when not in fail-on",
			verdicts: []model.Verdict{model.VerdictFixed},
			failOn:   []string{"regressed", "broken"},
			want:     false,
		},
		{
			name:     "broken always triggers when in fail-on",
			verdicts: []model.Verdict{model.VerdictAsExpected, model.VerdictBroken},
			failOn:   []string{"broken"},
			want:     true,
		},
		{
			name:     "empty verdicts never fail",
			verdicts: nil,
			failOn:   []string{"regressed"},
			want:     false,
		},
		{
			name:     "empty fail-on never fails",
			verdicts: []model.Verdict{model.VerdictRegressed},
			failOn:   nil,
			want:     false,
		},
		{
			name:     "unknown verdict in fail-on is error",
			verdicts: []model.Verdict{model.VerdictAsExpected},
			failOn:   []string{"invalid-verdict"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verdict.ShouldFail(tt.verdicts, tt.failOn)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultFailOn(t *testing.T) {
	defaults := verdict.DefaultFailOn()
	for _, v := range defaults {
		if v == model.VerdictAsExpected {
			t.Errorf("as-expected should not be in default fail-on")
		}
	}
	// Should contain all other verdicts.
	expected := map[model.Verdict]bool{
		model.VerdictFixed:        true,
		model.VerdictRegressed:    true,
		model.VerdictChanged:      true,
		model.VerdictUnrecognised: true,
		model.VerdictBroken:       true,
	}
	for _, v := range defaults {
		delete(expected, v)
	}
	if len(expected) > 0 {
		t.Errorf("missing from default fail-on: %v", expected)
	}
}
