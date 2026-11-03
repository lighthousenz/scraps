package checks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Artefact describes an artefact produced by a run.
type Artefact struct {
	Name  string
	Dir   string   // absolute directory
	Files []string // relative file paths
}

// Result is the outcome of running a check.
type Result struct {
	Match  bool
	Reason string
}

// Run executes a built-in check against an artefact.
// Returns an error only for bad params; a missing file or malformed data
// returns no-match with a reason.
func Run(checkName string, artefact Artefact, params any) (Result, error) {
	switch checkName {
	case "file-exists":
		return runFileExists(artefact, params)
	case "json":
		return runJSON(artefact, params)
	case "text":
		return runText(artefact, params)
	default:
		return Result{}, fmt.Errorf("unknown built-in check %q", checkName)
	}
}

func runFileExists(art Artefact, params any) (Result, error) {
	relPath, ok := params.(string)
	if !ok {
		return Result{}, fmt.Errorf("file-exists: params must be a string, got %T", params)
	}
	absPath := filepath.Join(art.Dir, relPath)
	_, err := os.Stat(absPath)
	if err != nil {
		return Result{Match: false, Reason: fmt.Sprintf("file %q does not exist", relPath)}, nil
	}
	return Result{Match: true, Reason: fmt.Sprintf("file %q exists", relPath)}, nil
}

func runJSON(art Artefact, params any) (Result, error) {
	m, ok := params.(map[string]any)
	if !ok {
		return Result{}, fmt.Errorf("json: params must be a mapping, got %T", params)
	}

	pathRaw, ok := m["path"]
	if !ok {
		return Result{}, fmt.Errorf("json: missing required param \"path\"")
	}
	pathStr, ok := pathRaw.(string)
	if !ok {
		return Result{}, fmt.Errorf("json: \"path\" must be a string, got %T", pathRaw)
	}

	equalsRaw, ok := m["equals"]
	if !ok {
		return Result{}, fmt.Errorf("json: missing required param \"equals\"")
	}

	// Validate path before any file I/O, so bad params always produce an
	// error regardless of artefact state.
	segments, err := parseJSONPath(pathStr)
	if err != nil {
		return Result{}, fmt.Errorf("json: %w", err)
	}

	// Determine the file to read. A nil error with empty fileName means no
	// .json file existed in the artefact (runtime data, not bad params).
	fileName, err := resolveJSONFile(art, m)
	if err != nil {
		return Result{}, err
	}
	if fileName == "" {
		return Result{Match: false, Reason: fmt.Sprintf("no .json file in artefact %q", art.Name)}, nil
	}

	absPath := filepath.Join(art.Dir, fileName)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return Result{Match: false, Reason: fmt.Sprintf("cannot read %q: %v", fileName, err)}, nil
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Result{Match: false, Reason: fmt.Sprintf("malformed JSON in %q: %v", fileName, err)}, nil
	}

	val, err := walkJSON(parsed, segments)
	if err != nil {
		return Result{Match: false, Reason: fmt.Sprintf("path %q: %v", pathStr, err)}, nil
	}

	if deepEqual(val, equalsRaw) {
		return Result{Match: true, Reason: fmt.Sprintf("path %q equals expected value", pathStr)}, nil
	}
	return Result{Match: false, Reason: fmt.Sprintf("path %q: got %v, want %v", pathStr, val, equalsRaw)}, nil
}

func resolveJSONFile(art Artefact, m map[string]any) (string, error) {
	if fileRaw, ok := m["file"]; ok {
		fileStr, ok := fileRaw.(string)
		if !ok {
			return "", fmt.Errorf("json: \"file\" must be a string, got %T", fileRaw)
		}
		return fileStr, nil
	}
	// Default to the only .json file.
	var jsonFiles []string
	for _, f := range art.Files {
		if strings.HasSuffix(f, ".json") {
			jsonFiles = append(jsonFiles, f)
		}
	}
	if len(jsonFiles) == 0 {
		// No .json files is a runtime condition (the actor produced none),
		// not a bad param. Return empty to signal no-match upstream.
		return "", nil
	}
	if len(jsonFiles) > 1 {
		return "", fmt.Errorf("json: artefact %q has %d .json files; set \"file\" param to disambiguate", art.Name, len(jsonFiles))
	}
	return jsonFiles[0], nil
}

type jsonSegment struct {
	kind  string // "key", "index", "length"
	key   string
	index int
}

func parseJSONPath(path string) ([]jsonSegment, error) {
	if path == "" || path[0] != '$' {
		return nil, fmt.Errorf("invalid path %q: must start with $", path)
	}
	rest := path[1:]
	var segments []jsonSegment
	for rest != "" {
		if rest[0] == '.' {
			rest = rest[1:]
			if rest == "length" || strings.HasPrefix(rest, "length") && (len(rest) == 6 || rest[6] == '.' || rest[6] == '[') {
				if len(rest) >= 6 && rest[:6] == "length" {
					segments = append(segments, jsonSegment{kind: "length"})
					rest = rest[6:]
					continue
				}
			}
			// Read a key.
			end := strings.IndexAny(rest, ".[")
			if end == -1 {
				segments = append(segments, jsonSegment{kind: "key", key: rest})
				rest = ""
			} else {
				segments = append(segments, jsonSegment{kind: "key", key: rest[:end]})
				rest = rest[end:]
			}
		} else if rest[0] == '[' {
			end := strings.Index(rest, "]")
			if end == -1 {
				return nil, fmt.Errorf("invalid path %q: unclosed [", path)
			}
			idxStr := rest[1:end]
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return nil, fmt.Errorf("invalid path %q: bad index %q", path, idxStr)
			}
			segments = append(segments, jsonSegment{kind: "index", index: idx})
			rest = rest[end+1:]
		} else {
			return nil, fmt.Errorf("invalid path %q: unexpected char %q", path, string(rest[0]))
		}
	}
	return segments, nil
}

func walkJSON(val any, segments []jsonSegment) (any, error) {
	for _, seg := range segments {
		switch seg.kind {
		case "key":
			m, ok := val.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected object at .%s, got %T", seg.key, val)
			}
			v, ok := m[seg.key]
			if !ok {
				return nil, fmt.Errorf("key %q not found", seg.key)
			}
			val = v
		case "index":
			arr, ok := val.([]any)
			if !ok {
				return nil, fmt.Errorf("expected array at [%d], got %T", seg.index, val)
			}
			if seg.index < 0 || seg.index >= len(arr) {
				return nil, fmt.Errorf("index %d out of range (length %d)", seg.index, len(arr))
			}
			val = arr[seg.index]
		case "length":
			switch v := val.(type) {
			case []any:
				val = float64(len(v))
			case string:
				val = float64(utf8.RuneCountInString(v))
			case map[string]any:
				// When the current value is an object, treat .length as
				// a key lookup so fields named "length" are accessible.
				lv, ok := v["length"]
				if !ok {
					return nil, fmt.Errorf("key %q not found", "length")
				}
				val = lv
			default:
				return nil, fmt.Errorf(".length: expected array or string, got %T", val)
			}
		}
	}
	return val, nil
}

// deepEqual compares two values for JSON-level deep equality.
// JSON numbers are float64; YAML may decode them as int.
func deepEqual(a, b any) bool {
	an := toFloat(a)
	bn := toFloat(b)
	if an != nil && bn != nil {
		return *an == *bn
	}

	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case nil:
		return b == nil
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			bval, ok := bv[k]
			if !ok || !deepEqual(v, bval) {
				return false
			}
		}
		return true
	default:
		// Fall back to numeric comparison for int/float variants.
		return false
	}
}

func toFloat(v any) *float64 {
	switch n := v.(type) {
	case float64:
		return &n
	case float32:
		f := float64(n)
		return &f
	case int:
		f := float64(n)
		return &f
	case int64:
		f := float64(n)
		return &f
	case int32:
		f := float64(n)
		return &f
	case uint64:
		f := float64(n)
		return &f
	default:
		return nil
	}
}

func runText(art Artefact, params any) (Result, error) {
	m, ok := params.(map[string]any)
	if !ok {
		return Result{}, fmt.Errorf("text: params must be a mapping, got %T", params)
	}

	fileRaw, ok := m["file"]
	if !ok {
		return Result{}, fmt.Errorf("text: missing required param \"file\"")
	}
	fileStr, ok := fileRaw.(string)
	if !ok {
		return Result{}, fmt.Errorf("text: \"file\" must be a string, got %T", fileRaw)
	}

	matchesRaw, ok := m["matches"]
	if !ok {
		return Result{}, fmt.Errorf("text: missing required param \"matches\"")
	}
	matchesStr, ok := matchesRaw.(string)
	if !ok {
		return Result{}, fmt.Errorf("text: \"matches\" must be a string, got %T", matchesRaw)
	}

	re, err := regexp.Compile(matchesStr)
	if err != nil {
		return Result{}, fmt.Errorf("text: invalid regex %q: %w", matchesStr, err)
	}

	absPath := filepath.Join(art.Dir, fileStr)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return Result{Match: false, Reason: fmt.Sprintf("cannot read %q: %v", fileStr, err)}, nil
	}

	if re.Match(data) {
		return Result{Match: true, Reason: fmt.Sprintf("file %q matches /%s/", fileStr, matchesStr)}, nil
	}
	return Result{Match: false, Reason: fmt.Sprintf("file %q does not match /%s/", fileStr, matchesStr)}, nil
}
