package lock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	lockFile := filepath.Join(dir, "scraps.lock")
	content := `version: 1
sources:
  "github:owner/repo/path@v1.0.0":
    commit: 0123456789abcdef0123456789abcdef01234567
    hash: sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789
`
	if err := os.WriteFile(lockFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	lf, err := Load(lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if lf.Version != 1 {
		t.Errorf("version = %d, want 1", lf.Version)
	}
	if len(lf.Sources) != 1 {
		t.Fatalf("sources count = %d, want 1", len(lf.Sources))
	}

	entry, ok := lf.Sources["github:owner/repo/path@v1.0.0"]
	if !ok {
		t.Fatal("missing source entry")
	}
	if entry.Commit != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("commit = %q", entry.Commit)
	}
	if entry.Hash != "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789" {
		t.Errorf("hash = %q", entry.Hash)
	}
}

func TestLookup(t *testing.T) {
	lf := &LockFile{
		Version: 1,
		Sources: map[string]LockEntry{
			"github:owner/repo/path@v1.0.0": {
				Commit: "abc123",
				Hash:   "sha256:deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
			},
		},
	}

	t.Run("found", func(t *testing.T) {
		entry, dir, err := Lookup(lf, "github:owner/repo/path@v1.0.0", "/project")
		if err != nil {
			t.Fatal(err)
		}
		if entry.Commit != "abc123" {
			t.Errorf("commit = %q", entry.Commit)
		}
		want := filepath.Join("/project", ".scraps", "sources", "deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567")
		if dir != want {
			t.Errorf("dir = %q, want %q", dir, want)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, _, err := Lookup(lf, "github:other/repo@v2.0.0", "/project")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("nil lock", func(t *testing.T) {
		_, _, err := Lookup(nil, "github:owner/repo@v1.0.0", "/project")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestIsGitHubSource(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"github:owner/repo/path@v1.0.0", true},
		{"./actors/my-actor", false},
		{"../shared/actors/x", false},
	}
	for _, tt := range tests {
		if got := IsGitHubSource(tt.source); got != tt.want {
			t.Errorf("IsGitHubSource(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}

func TestIsLocalSource(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"./actors/my-actor", true},
		{"../shared/actors/x", true},
		{"github:owner/repo@v1", false},
	}
	for _, tt := range tests {
		if got := IsLocalSource(tt.source); got != tt.want {
			t.Errorf("IsLocalSource(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}
