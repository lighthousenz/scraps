// Package expr parses and resolves ${{ }} expressions in strings.
package expr

import (
	"fmt"
	"regexp"
	"strings"
)

// exprRe matches ${{ ref }} patterns. Segments may contain word chars and hyphens.
var exprRe = regexp.MustCompile(`\$\{\{\s*([\w][\w.-]*)\s*\}\}`)

// malformedExprRe matches any ${{ ... }} to detect malformed expressions.
var malformedExprRe = regexp.MustCompile(`\$\{\{(.*?)\}\}`)

// Ref is a parsed dotted reference inside ${{ }}.
type Ref struct {
	Raw   string   // the full text between ${{ and }}
	Parts []string // dotted segments
}

// Parse extracts all expression references from a string.
func Parse(s string) []Ref {
	matches := exprRe.FindAllStringSubmatchIndex(s, -1)
	var refs []Ref
	for _, m := range matches {
		raw := s[m[2]:m[3]]
		refs = append(refs, Ref{
			Raw:   raw,
			Parts: strings.Split(raw, "."),
		})
	}
	return refs
}

// ContainsExpr reports whether s contains at least one ${{ }} expression.
func ContainsExpr(s string) bool {
	return exprRe.MatchString(s)
}

// IsExactExpr reports whether s is exactly one expression with no surrounding text.
func IsExactExpr(s string) bool {
	s = strings.TrimSpace(s)
	loc := exprRe.FindStringIndex(s)
	if loc == nil {
		return false
	}
	return loc[0] == 0 && loc[1] == len(s)
}

// StaticScope describes what names are declared for static checking.
type StaticScope struct {
	Fixtures map[string]bool            // fixture names
	Subjects map[string]bool            // subject names
	Actors   map[string]map[string]bool // actor name -> output keys
	Inputs   map[string]InputInfo       // input name -> info (actor defs only)
}

// InputInfo describes an input for static checking.
type InputInfo struct {
	IsActor bool
	Outputs map[string]bool // output keys for actor-typed inputs
}

// ValidRoots lists the legal root segments.
var ValidRoots = []string{"fixtures", "subjects", "actors", "inputs", "run"}

// ValidRunFields lists the legal run.* fields.
var ValidRunFields = map[string]bool{
	"id":        true,
	"seed":      true,
	"workdir":   true,
	"artefacts": true,
}

// StaticCheck validates a reference against the declared scope.
// Returns an error message, or "" if valid.
func StaticCheck(ref Ref, scope *StaticScope) string {
	if len(ref.Parts) < 2 {
		return fmt.Sprintf("expression %q: too few segments", ref.Raw)
	}

	root := ref.Parts[0]
	switch root {
	case "fixtures":
		if len(ref.Parts) != 2 {
			return fmt.Sprintf("expression %q: fixtures.<name> expects exactly one segment after 'fixtures'", ref.Raw)
		}
		name := ref.Parts[1]
		if scope != nil && scope.Fixtures != nil && !scope.Fixtures[name] {
			return fmt.Sprintf("expression %q: unknown fixture %q", ref.Raw, name)
		}

	case "subjects":
		if len(ref.Parts) != 2 {
			return fmt.Sprintf("expression %q: subjects.<name> expects exactly one segment after 'subjects'", ref.Raw)
		}
		name := ref.Parts[1]
		if scope != nil && scope.Subjects != nil && !scope.Subjects[name] {
			return fmt.Sprintf("expression %q: unknown subject %q", ref.Raw, name)
		}

	case "actors":
		// actors.<name>.outputs.<key>
		if len(ref.Parts) < 4 || ref.Parts[2] != "outputs" {
			return fmt.Sprintf("expression %q: expected actors.<name>.outputs.<key>", ref.Raw)
		}
		if len(ref.Parts) != 4 {
			return fmt.Sprintf("expression %q: expected actors.<name>.outputs.<key>", ref.Raw)
		}
		name := ref.Parts[1]
		key := ref.Parts[3]
		if scope != nil && scope.Actors != nil {
			outputs, ok := scope.Actors[name]
			if !ok {
				return fmt.Sprintf("expression %q: unknown actor %q", ref.Raw, name)
			}
			if outputs != nil && !outputs[key] {
				return fmt.Sprintf("expression %q: actor %q has no output %q", ref.Raw, name, key)
			}
		}

	case "inputs":
		// inputs.<name> or inputs.<name>.outputs.<key>
		if len(ref.Parts) == 2 {
			name := ref.Parts[1]
			if scope != nil && scope.Inputs != nil {
				if _, ok := scope.Inputs[name]; !ok {
					return fmt.Sprintf("expression %q: unknown input %q", ref.Raw, name)
				}
			}
		} else if len(ref.Parts) == 4 && ref.Parts[2] == "outputs" {
			name := ref.Parts[1]
			if scope != nil && scope.Inputs != nil {
				info, ok := scope.Inputs[name]
				if !ok {
					return fmt.Sprintf("expression %q: unknown input %q", ref.Raw, name)
				}
				if !info.IsActor {
					return fmt.Sprintf("expression %q: input %q is not of type actor", ref.Raw, name)
				}
				key := ref.Parts[3]
				if info.Outputs != nil && !info.Outputs[key] {
					return fmt.Sprintf("expression %q: input %q has no output %q", ref.Raw, name, key)
				}
			}
		} else {
			return fmt.Sprintf("expression %q: expected inputs.<name> or inputs.<name>.outputs.<key>", ref.Raw)
		}

	case "run":
		if len(ref.Parts) != 2 {
			return fmt.Sprintf("expression %q: expected run.<field>", ref.Raw)
		}
		field := ref.Parts[1]
		if !ValidRunFields[field] {
			return fmt.Sprintf("expression %q: unknown run field %q", ref.Raw, field)
		}

	default:
		return fmt.Sprintf("expression %q: unknown root %q", ref.Raw, root)
	}
	return ""
}

// Context provides values for expression resolution.
type Context struct {
	Fixtures map[string]any
	Subjects map[string]string
	Actors   map[string]map[string]any // actor name -> outputs
	Inputs   map[string]any            // input name -> value (or map with "outputs" key for actor-typed)
	Run      map[string]any
}

// Resolve resolves a single reference against a value context.
func Resolve(ref Ref, ctx *Context) (any, error) {
	root := ref.Parts[0]
	switch root {
	case "fixtures":
		if len(ref.Parts) != 2 {
			return nil, fmt.Errorf("expression %q: expected fixtures.<name>", ref.Raw)
		}
		name := ref.Parts[1]
		v, ok := ctx.Fixtures[name]
		if !ok {
			return nil, fmt.Errorf("expression %q: fixture %q not found", ref.Raw, name)
		}
		return v, nil

	case "subjects":
		if len(ref.Parts) != 2 {
			return nil, fmt.Errorf("expression %q: expected subjects.<name>", ref.Raw)
		}
		name := ref.Parts[1]
		v, ok := ctx.Subjects[name]
		if !ok {
			return nil, fmt.Errorf("expression %q: subject %q not found", ref.Raw, name)
		}
		return v, nil

	case "actors":
		if len(ref.Parts) != 4 || ref.Parts[2] != "outputs" {
			return nil, fmt.Errorf("expression %q: expected actors.<name>.outputs.<key>", ref.Raw)
		}
		name := ref.Parts[1]
		outputs, ok := ctx.Actors[name]
		if !ok {
			return nil, fmt.Errorf("expression %q: actor %q not found", ref.Raw, name)
		}
		key := ref.Parts[3]
		v, ok := outputs[key]
		if !ok {
			return nil, fmt.Errorf("expression %q: actor %q has no output %q", ref.Raw, name, key)
		}
		return v, nil

	case "inputs":
		if len(ref.Parts) == 2 {
			name := ref.Parts[1]
			v, ok := ctx.Inputs[name]
			if !ok {
				return nil, fmt.Errorf("expression %q: input %q not found", ref.Raw, name)
			}
			return v, nil
		}
		if len(ref.Parts) == 4 && ref.Parts[2] == "outputs" {
			name := ref.Parts[1]
			v, ok := ctx.Inputs[name]
			if !ok {
				return nil, fmt.Errorf("expression %q: input %q not found", ref.Raw, name)
			}
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expression %q: input %q is not an actor", ref.Raw, name)
			}
			outputs, ok := m["outputs"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expression %q: input %q has no outputs", ref.Raw, name)
			}
			key := ref.Parts[3]
			val, ok := outputs[key]
			if !ok {
				return nil, fmt.Errorf("expression %q: input %q has no output %q", ref.Raw, name, key)
			}
			return val, nil
		}
		return nil, fmt.Errorf("expression %q: expected inputs.<name> or inputs.<name>.outputs.<key>", ref.Raw)

	case "run":
		if len(ref.Parts) != 2 {
			return nil, fmt.Errorf("expression %q: expected run.<field>", ref.Raw)
		}
		field := ref.Parts[1]
		v, ok := ctx.Run[field]
		if !ok {
			return nil, fmt.Errorf("expression %q: run.%s not found", ref.Raw, field)
		}
		return v, nil

	default:
		return nil, fmt.Errorf("expression %q: unknown root %q", ref.Raw, root)
	}
}

// ResolveString resolves all expressions in a string.
// If the string is exactly one expression, the resolved value is returned with its type preserved.
// If expressions are embedded in a larger string, they're converted to strings.
func ResolveString(s string, ctx *Context) (any, error) {
	if IsExactExpr(s) {
		refs := Parse(s)
		if len(refs) == 1 {
			return Resolve(refs[0], ctx)
		}
	}

	result := exprRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := exprRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		ref := Ref{
			Raw:   sub[1],
			Parts: strings.Split(sub[1], "."),
		}
		v, err := Resolve(ref, ctx)
		if err != nil {
			return match // keep original on error
		}
		return fmt.Sprintf("%v", v)
	})
	return result, nil
}

// ResolveStringStrict resolves all expressions, returning an error if any cannot be resolved.
func ResolveStringStrict(s string, ctx *Context) (any, error) {
	if IsExactExpr(s) {
		refs := Parse(s)
		if len(refs) == 1 {
			return Resolve(refs[0], ctx)
		}
	}

	var resolveErr error
	result := exprRe.ReplaceAllStringFunc(s, func(match string) string {
		if resolveErr != nil {
			return match
		}
		sub := exprRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		ref := Ref{
			Raw:   sub[1],
			Parts: strings.Split(sub[1], "."),
		}
		v, err := Resolve(ref, ctx)
		if err != nil {
			resolveErr = err
			return match
		}
		return fmt.Sprintf("%v", v)
	})
	if resolveErr != nil {
		return nil, resolveErr
	}
	return result, nil
}

// CheckAllExprs statically checks every expression found in a string.
// It also reports malformed ${{ }} expressions that don't match the valid pattern.
func CheckAllExprs(s string, scope *StaticScope) []string {
	refs := Parse(s)
	var errs []string
	for _, ref := range refs {
		if msg := StaticCheck(ref, scope); msg != "" {
			errs = append(errs, msg)
		}
	}

	// Find malformed expressions: anything matching ${{ ... }} but not the valid pattern.
	validLocs := exprRe.FindAllStringIndex(s, -1)
	allLocs := malformedExprRe.FindAllStringSubmatchIndex(s, -1)
	for _, loc := range allLocs {
		isValid := false
		for _, vl := range validLocs {
			if vl[0] == loc[0] && vl[1] == loc[1] {
				isValid = true
				break
			}
		}
		if !isValid {
			inner := strings.TrimSpace(s[loc[2]:loc[3]])
			errs = append(errs, fmt.Sprintf("malformed expression \"${{ %s }}\"", inner))
		}
	}
	return errs
}
