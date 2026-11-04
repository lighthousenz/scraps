// Package lock reads scraps.lock and provides the cache directory for a source.
package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// LockFile represents the contents of scraps.lock.
type LockFile struct {
	Version int                  `yaml:"version"`
	Sources map[string]LockEntry `yaml:"sources"`
}

// LockEntry is one locked source.
type LockEntry struct {
	Commit string `yaml:"commit"`
	Hash   string `yaml:"hash"`
}

// Load reads and parses a scraps.lock file.
func Load(path string) (*LockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading lock file: %w", err)
	}
	var lf LockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parsing lock file: %w", err)
	}
	return &lf, nil
}

// Lookup returns the lock entry and cache directory for a source string.
// projectDir is the directory containing scraps.yaml.
func Lookup(lf *LockFile, source, projectDir string) (*LockEntry, string, error) {
	if lf == nil || lf.Sources == nil {
		return nil, "", fmt.Errorf("source %q not found in lock file; run \"scraps lock\"", source)
	}
	entry, ok := lf.Sources[source]
	if !ok {
		return nil, "", fmt.Errorf("source %q not found in lock file; run \"scraps lock\"", source)
	}
	// Extract the hex hash from "sha256:<hex>"
	hash := entry.Hash
	if idx := strings.Index(hash, ":"); idx >= 0 {
		hash = hash[idx+1:]
	}
	cacheDir := filepath.Join(projectDir, ".scraps", "sources", hash)
	return &entry, cacheDir, nil
}

// MismatchError indicates a lock-file mismatch: a missing source or a hash
// that does not match the locked value. Callers should exit 2 (invalid config)
// for these, not 3 (scraps itself failed).
type MismatchError struct {
	Message string
}

func (e *MismatchError) Error() string { return e.Message }

// IsGitHubSource reports whether source is a github: reference.
func IsGitHubSource(source string) bool {
	return strings.HasPrefix(source, "github:")
}

// IsLocalSource reports whether source is a local path reference.
func IsLocalSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../")
}
