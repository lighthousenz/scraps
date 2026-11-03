package verdict_test

import (
	"testing"

	"github.com/lighthousenz/scraps/internal/model"
	"github.com/lighthousenz/scraps/internal/verdict"
)

func TestEvalHistory(t *testing.T) {
	tests := []struct {
		name     string
		history  []model.HistoryEntry
		subjects map[string]model.Subject
		versions map[string]string
		want     verdict.HistoryResult
		wantErr  bool
	}{
		{
			name:    "no history entries defaults to correct",
			history: nil,
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "1.0.0"},
			want:     verdict.HistoryResult{Expected: "correct", Index: -1},
		},
		{
			name: "semver first match wins",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": "<2.0.0"}, Expect: "old-bug"},
				{When: map[string]string{"lib": ">=2.0.0"}, Expect: "new-bug"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "1.5.0"},
			want:     verdict.HistoryResult{Expected: "old-bug", Index: 0},
		},
		{
			name: "semver second entry matches",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": "<2.0.0"}, Expect: "old-bug"},
				{When: map[string]string{"lib": ">=2.0.0"}, Expect: "new-bug"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "2.1.0"},
			want:     verdict.HistoryResult{Expected: "new-bug", Index: 1},
		},
		{
			name: "semver AND constraint (space-separated)",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": ">=2.31.0 <2.33.0"}, Expect: "drops-one"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "2.32.0"},
			want:     verdict.HistoryResult{Expected: "drops-one", Index: 0},
		},
		{
			name: "semver AND constraint does not match outside range",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": ">=2.31.0 <2.33.0"}, Expect: "drops-one"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "2.33.0"},
			want:     verdict.HistoryResult{Expected: "correct", Index: -1},
		},
		{
			name: "semver OR constraint",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": "1.0.0 || 3.0.0"}, Expect: "bug"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "3.0.0"},
			want:     verdict.HistoryResult{Expected: "bug", Index: 0},
		},
		{
			name: "semver OR constraint does not match other versions",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": "1.0.0 || 3.0.0"}, Expect: "bug"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "2.0.0"},
			want:     verdict.HistoryResult{Expected: "correct", Index: -1},
		},
		{
			name: "exact scheme matches",
			history: []model.HistoryEntry{
				{When: map[string]string{"db": "nightly-2026-01-15"}, Expect: "crash"},
			},
			subjects: map[string]model.Subject{
				"db": {Scheme: "exact"},
			},
			versions: map[string]string{"db": "nightly-2026-01-15"},
			want:     verdict.HistoryResult{Expected: "crash", Index: 0},
		},
		{
			name: "exact scheme does not match different string",
			history: []model.HistoryEntry{
				{When: map[string]string{"db": "nightly-2026-01-15"}, Expect: "crash"},
			},
			subjects: map[string]model.Subject{
				"db": {Scheme: "exact"},
			},
			versions: map[string]string{"db": "nightly-2026-01-16"},
			want:     verdict.HistoryResult{Expected: "correct", Index: -1},
		},
		{
			name: "multi-subject AND in when",
			history: []model.HistoryEntry{
				{When: map[string]string{
					"client": ">=2.0.0",
					"server": ">=3.0.0",
				}, Expect: "both-new"},
			},
			subjects: map[string]model.Subject{
				"client": {Scheme: "semver"},
				"server": {Scheme: "semver"},
			},
			versions: map[string]string{
				"client": "2.1.0",
				"server": "3.5.0",
			},
			want: verdict.HistoryResult{Expected: "both-new", Index: 0},
		},
		{
			name: "multi-subject AND fails when one does not match",
			history: []model.HistoryEntry{
				{When: map[string]string{
					"client": ">=2.0.0",
					"server": ">=3.0.0",
				}, Expect: "both-new"},
			},
			subjects: map[string]model.Subject{
				"client": {Scheme: "semver"},
				"server": {Scheme: "semver"},
			},
			versions: map[string]string{
				"client": "2.1.0",
				"server": "2.5.0",
			},
			want: verdict.HistoryResult{Expected: "correct", Index: -1},
		},
		{
			name: "error: unknown subject in when",
			history: []model.HistoryEntry{
				{When: map[string]string{"unknown": "1.0.0"}, Expect: "bug"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "1.0.0"},
			wantErr:  true,
		},
		{
			name: "error: subject with no resolved version",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": ">=1.0.0"}, Expect: "bug"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{},
			wantErr:  true,
		},
		{
			name: "error: invalid semver version",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": ">=1.0.0"}, Expect: "bug"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "not-a-version"},
			wantErr:  true,
		},
		{
			name: "error: invalid semver constraint",
			history: []model.HistoryEntry{
				{When: map[string]string{"lib": ">>>bad"}, Expect: "bug"},
			},
			subjects: map[string]model.Subject{
				"lib": {Scheme: "semver"},
			},
			versions: map[string]string{"lib": "1.0.0"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verdict.EvalHistory(tt.history, tt.subjects, tt.versions)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Expected != tt.want.Expected || got.Index != tt.want.Index {
				t.Errorf("got {Expected: %q, Index: %d}, want {Expected: %q, Index: %d}",
					got.Expected, got.Index, tt.want.Expected, tt.want.Index)
			}
		})
	}
}

func TestMatchStepError(t *testing.T) {
	tests := []struct {
		name    string
		matcher *model.StepErrorMatcher
		failure *verdict.StepFailure
		want    bool
		wantErr bool
	}{
		{
			name:    "no failure means no match",
			matcher: &model.StepErrorMatcher{Actor: "alice"},
			failure: nil,
			want:    false,
		},
		{
			name:    "actor matches",
			matcher: &model.StepErrorMatcher{Actor: "alice"},
			failure: &verdict.StepFailure{Actor: "alice", Operation: "write", Message: "error"},
			want:    true,
		},
		{
			name:    "actor does not match",
			matcher: &model.StepErrorMatcher{Actor: "alice"},
			failure: &verdict.StepFailure{Actor: "bob", Operation: "write", Message: "error"},
			want:    false,
		},
		{
			name:    "operation matches",
			matcher: &model.StepErrorMatcher{Operation: "write"},
			failure: &verdict.StepFailure{Actor: "alice", Operation: "write", Message: "error"},
			want:    true,
		},
		{
			name:    "operation does not match",
			matcher: &model.StepErrorMatcher{Operation: "read"},
			failure: &verdict.StepFailure{Actor: "alice", Operation: "write", Message: "error"},
			want:    false,
		},
		{
			name:    "exact message matches",
			matcher: &model.StepErrorMatcher{Message: "toy write failed"},
			failure: &verdict.StepFailure{Actor: "toy", Operation: "write", Message: "toy write failed"},
			want:    true,
		},
		{
			name:    "exact message does not match",
			matcher: &model.StepErrorMatcher{Message: "toy write failed"},
			failure: &verdict.StepFailure{Actor: "toy", Operation: "write", Message: "different error"},
			want:    false,
		},
		{
			name: "regex message matches",
			matcher: &model.StepErrorMatcher{
				Message: map[string]any{"matches": "Cannot read"},
			},
			failure: &verdict.StepFailure{Actor: "bob", Operation: "insert", Message: "Cannot read properties of null"},
			want:    true,
		},
		{
			name: "regex message does not match",
			matcher: &model.StepErrorMatcher{
				Message: map[string]any{"matches": "^Cannot read$"},
			},
			failure: &verdict.StepFailure{Actor: "bob", Operation: "insert", Message: "Cannot read properties of null"},
			want:    false,
		},
		{
			name: "all fields match together",
			matcher: &model.StepErrorMatcher{
				Actor:     "bob",
				Operation: "insert-paragraph",
				Message:   map[string]any{"matches": "Cannot read"},
			},
			failure: &verdict.StepFailure{Actor: "bob", Operation: "insert-paragraph", Message: "Cannot read properties of null"},
			want:    true,
		},
		{
			name: "one field fails in multi-field matcher",
			matcher: &model.StepErrorMatcher{
				Actor:     "alice",
				Operation: "insert-paragraph",
				Message:   map[string]any{"matches": "Cannot read"},
			},
			failure: &verdict.StepFailure{Actor: "bob", Operation: "insert-paragraph", Message: "Cannot read properties of null"},
			want:    false,
		},
		{
			name: "invalid regex is an error",
			matcher: &model.StepErrorMatcher{
				Message: map[string]any{"matches": "[invalid"},
			},
			failure: &verdict.StepFailure{Actor: "a", Operation: "b", Message: "c"},
			wantErr: true,
		},
		{
			name: "bad message form is an error",
			matcher: &model.StepErrorMatcher{
				Message: map[string]any{"unknown": "value"},
			},
			failure: &verdict.StepFailure{Actor: "a", Operation: "b", Message: "c"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verdict.MatchStepError(tt.matcher, tt.failure)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
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
