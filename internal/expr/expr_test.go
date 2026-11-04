package expr

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Ref
	}{
		{"no expr", "hello world", nil},
		{"single", "${{ fixtures.doc }}", []Ref{{Raw: "fixtures.doc", Parts: []string{"fixtures", "doc"}}}},
		{"embedded", "prefix ${{ subjects.yjs }} suffix", []Ref{{Raw: "subjects.yjs", Parts: []string{"subjects", "yjs"}}}},
		{"multiple", "${{ fixtures.a }} and ${{ subjects.b }}", []Ref{
			{Raw: "fixtures.a", Parts: []string{"fixtures", "a"}},
			{Raw: "subjects.b", Parts: []string{"subjects", "b"}},
		}},
		{"actor outputs", "${{ actors.server.outputs.url }}", []Ref{
			{Raw: "actors.server.outputs.url", Parts: []string{"actors", "server", "outputs", "url"}},
		}},
		{"run field", "${{ run.id }}", []Ref{{Raw: "run.id", Parts: []string{"run", "id"}}}},
		{"spaces", "${{  fixtures.doc  }}", []Ref{{Raw: "fixtures.doc", Parts: []string{"fixtures", "doc"}}}},
		{"hyphenated name", "${{ fixtures.two-paragraphs }}", []Ref{{Raw: "fixtures.two-paragraphs", Parts: []string{"fixtures", "two-paragraphs"}}}},
		{"hyphenated actor", "${{ actors.collab-server.outputs.url }}", []Ref{{Raw: "actors.collab-server.outputs.url", Parts: []string{"actors", "collab-server", "outputs", "url"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.input)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestContainsExpr(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"hello", false},
		{"${{ foo.bar }}", true},
		{"pre ${{ x.y }} post", true},
	}
	for _, tt := range tests {
		if got := ContainsExpr(tt.input); got != tt.want {
			t.Errorf("ContainsExpr(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsExactExpr(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"${{ foo.bar }}", true},
		{"pre ${{ foo.bar }}", false},
		{"${{ foo.bar }} post", false},
		{"hello", false},
	}
	for _, tt := range tests {
		if got := IsExactExpr(tt.input); got != tt.want {
			t.Errorf("IsExactExpr(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestStaticCheck(t *testing.T) {
	scope := &StaticScope{
		Fixtures: map[string]bool{"doc": true},
		Subjects: map[string]bool{"yjs": true},
		Actors:   map[string]map[string]bool{"server": {"url": true}},
		Inputs: map[string]InputInfo{
			"srv":  {IsActor: true, Outputs: map[string]bool{"url": true}},
			"room": {IsActor: false},
		},
	}

	tests := []struct {
		name    string
		ref     Ref
		wantErr bool
	}{
		{"valid fixture", Ref{Raw: "fixtures.doc", Parts: []string{"fixtures", "doc"}}, false},
		{"unknown fixture", Ref{Raw: "fixtures.nope", Parts: []string{"fixtures", "nope"}}, true},
		{"valid subject", Ref{Raw: "subjects.yjs", Parts: []string{"subjects", "yjs"}}, false},
		{"unknown subject", Ref{Raw: "subjects.nope", Parts: []string{"subjects", "nope"}}, true},
		{"valid actor output", Ref{Raw: "actors.server.outputs.url", Parts: []string{"actors", "server", "outputs", "url"}}, false},
		{"unknown actor", Ref{Raw: "actors.nope.outputs.url", Parts: []string{"actors", "nope", "outputs", "url"}}, true},
		{"unknown actor output", Ref{Raw: "actors.server.outputs.nope", Parts: []string{"actors", "server", "outputs", "nope"}}, true},
		{"bad actors path", Ref{Raw: "actors.server", Parts: []string{"actors", "server"}}, true},
		{"valid input", Ref{Raw: "inputs.srv", Parts: []string{"inputs", "srv"}}, false},
		{"valid input outputs", Ref{Raw: "inputs.srv.outputs.url", Parts: []string{"inputs", "srv", "outputs", "url"}}, false},
		{"non-actor input outputs", Ref{Raw: "inputs.room.outputs.url", Parts: []string{"inputs", "room", "outputs", "url"}}, true},
		{"unknown input", Ref{Raw: "inputs.nope", Parts: []string{"inputs", "nope"}}, true},
		{"valid run", Ref{Raw: "run.id", Parts: []string{"run", "id"}}, false},
		{"valid run seed", Ref{Raw: "run.seed", Parts: []string{"run", "seed"}}, false},
		{"unknown run field", Ref{Raw: "run.nope", Parts: []string{"run", "nope"}}, true},
		{"unknown root", Ref{Raw: "nope.foo", Parts: []string{"nope", "foo"}}, true},
		{"too few segments", Ref{Raw: "fixtures", Parts: []string{"fixtures"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := StaticCheck(tt.ref, scope)
			gotErr := msg != ""
			if gotErr != tt.wantErr {
				t.Errorf("StaticCheck(%q) error=%v (msg=%q), wantErr=%v", tt.ref.Raw, gotErr, msg, tt.wantErr)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	ctx := &Context{
		Fixtures: map[string]any{"doc": "/path/to/doc"},
		Subjects: map[string]string{"yjs": "13.6.27"},
		Actors:   map[string]map[string]any{"server": {"url": "ws://localhost:1234"}},
		Inputs: map[string]any{
			"srv": map[string]any{
				"name":    "server",
				"outputs": map[string]any{"url": "ws://localhost:1234"},
			},
		},
		Run: map[string]any{"id": "run-1", "seed": int64(42)},
	}

	tests := []struct {
		name    string
		ref     Ref
		want    any
		wantErr bool
	}{
		{"fixture", Ref{Raw: "fixtures.doc", Parts: []string{"fixtures", "doc"}}, "/path/to/doc", false},
		{"subject", Ref{Raw: "subjects.yjs", Parts: []string{"subjects", "yjs"}}, "13.6.27", false},
		{"actor output", Ref{Raw: "actors.server.outputs.url", Parts: []string{"actors", "server", "outputs", "url"}}, "ws://localhost:1234", false},
		{"input outputs", Ref{Raw: "inputs.srv.outputs.url", Parts: []string{"inputs", "srv", "outputs", "url"}}, "ws://localhost:1234", false},
		{"run id", Ref{Raw: "run.id", Parts: []string{"run", "id"}}, "run-1", false},
		{"run seed", Ref{Raw: "run.seed", Parts: []string{"run", "seed"}}, int64(42), false},
		{"missing fixture", Ref{Raw: "fixtures.nope", Parts: []string{"fixtures", "nope"}}, nil, true},
		{"missing subject", Ref{Raw: "subjects.nope", Parts: []string{"subjects", "nope"}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.ref, ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve(%q) error=%v, wantErr=%v", tt.ref.Raw, err, tt.wantErr)
			}
			if !tt.wantErr {
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("Resolve(%q) mismatch (-want +got):\n%s", tt.ref.Raw, diff)
				}
			}
		})
	}
}

func TestResolveString(t *testing.T) {
	ctx := &Context{
		Fixtures: map[string]any{"doc": "/path/to/doc"},
		Subjects: map[string]string{"yjs": "13.6.27"},
		Run:      map[string]any{"id": "run-1"},
	}

	tests := []struct {
		name  string
		input string
		want  any
	}{
		{"exact expr preserves type", "${{ fixtures.doc }}", "/path/to/doc"},
		{"embedded converts to string", "ver ${{ subjects.yjs }} here", "ver 13.6.27 here"},
		{"no expr", "hello", "hello"},
		{"multiple embedded", "${{ run.id }}-${{ subjects.yjs }}", "run-1-13.6.27"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveString(tt.input, ctx)
			if err != nil {
				t.Fatalf("ResolveString(%q) error: %v", tt.input, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ResolveString(%q) mismatch (-want +got):\n%s", tt.input, diff)
			}
		})
	}
}

func TestCheckAllExprs(t *testing.T) {
	scope := &StaticScope{
		Fixtures: map[string]bool{"doc": true},
		Subjects: map[string]bool{"yjs": true},
	}

	tests := []struct {
		name     string
		input    string
		wantErrs int
	}{
		{"valid", "${{ fixtures.doc }}", 0},
		{"invalid", "${{ fixtures.nope }}", 1},
		{"multiple one invalid", "${{ fixtures.doc }} ${{ subjects.nope }}", 1},
		{"no exprs", "hello", 0},
		{"malformed brackets", "${{ foo['bar'] }}", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := CheckAllExprs(tt.input, scope)
			if len(errs) != tt.wantErrs {
				t.Errorf("CheckAllExprs(%q) got %d errors, want %d: %v", tt.input, len(errs), tt.wantErrs, errs)
			}
		})
	}
}
