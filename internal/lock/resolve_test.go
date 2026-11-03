package lock

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %s: %s", name, args, err, out)
	}
}

func runOutput(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %s", name, args, err)
	}
	return strings.TrimSpace(string(out))
}

// setupGitHubRepo creates a bare git repo at <baseDir>/<owner>/<repo> and sets
// SCRAPS_GITHUB_BASE_URL so that "github:<owner>/<repo>/<path>@<ref>" resolves
// through the local filesystem.
// subtreePath is the path within the repo that holds the files.
// refType is "tag", "branch", or "commit".
// refName is the tag/branch name (ignored for "commit").
func setupGitHubRepo(t *testing.T, owner, repo, subtreePath string, files map[string]string, refType, refName string) (commitSHA string) {
	t.Helper()

	baseDir := t.TempDir()
	t.Setenv("SCRAPS_GITHUB_BASE_URL", "file://"+baseDir)

	workDir := t.TempDir()
	run(t, workDir, "git", "init", "-b", "main")
	run(t, workDir, "git", "config", "user.email", "test@test.com")
	run(t, workDir, "git", "config", "user.name", "Test")

	for relPath, content := range files {
		fullPath := filepath.Join(workDir, subtreePath, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run(t, workDir, "git", "add", "-A")
	run(t, workDir, "git", "commit", "-m", "initial")

	switch refType {
	case "tag":
		run(t, workDir, "git", "tag", refName)
	case "branch":
		if refName != "main" {
			run(t, workDir, "git", "checkout", "-b", refName)
		}
	}

	sha := runOutput(t, workDir, "git", "rev-parse", "HEAD")

	// Clone bare into <baseDir>/<owner>/<repo>.
	repoDir := filepath.Join(baseDir, owner, repo)
	if err := os.MkdirAll(filepath.Dir(repoDir), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, "", "git", "clone", "--bare", workDir, repoDir)

	return sha
}

func TestResolveRefTag(t *testing.T) {
	baseDir := t.TempDir()

	workDir := t.TempDir()
	run(t, workDir, "git", "init", "-b", "main")
	run(t, workDir, "git", "config", "user.email", "test@test.com")
	run(t, workDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, workDir, "git", "add", "-A")
	run(t, workDir, "git", "commit", "-m", "initial")
	run(t, workDir, "git", "tag", "v1.0.0")
	wantSHA := runOutput(t, workDir, "git", "rev-parse", "HEAD")

	bareDir := filepath.Join(baseDir, "bare.git")
	run(t, "", "git", "clone", "--bare", workDir, bareDir)

	ctx := context.Background()
	got, err := ResolveRef(ctx, bareDir, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != wantSHA {
		t.Errorf("ResolveRef(tag) = %q, want %q", got, wantSHA)
	}
}

func TestResolveRefAnnotatedTag(t *testing.T) {
	baseDir := t.TempDir()

	workDir := t.TempDir()
	run(t, workDir, "git", "init", "-b", "main")
	run(t, workDir, "git", "config", "user.email", "test@test.com")
	run(t, workDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, workDir, "git", "add", "-A")
	run(t, workDir, "git", "commit", "-m", "initial")
	// Create an annotated tag — ls-remote returns both the tag object and the ^{} commit.
	run(t, workDir, "git", "tag", "-a", "v2.0.0", "-m", "release v2.0.0")
	wantSHA := runOutput(t, workDir, "git", "rev-parse", "HEAD")

	bareDir := filepath.Join(baseDir, "bare.git")
	run(t, "", "git", "clone", "--bare", workDir, bareDir)

	ctx := context.Background()
	got, err := ResolveRef(ctx, bareDir, "v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != wantSHA {
		t.Errorf("ResolveRef(annotated tag) = %q, want commit %q", got, wantSHA)
	}
}

func TestResolveRefBranch(t *testing.T) {
	workDir := t.TempDir()
	run(t, workDir, "git", "init", "-b", "main")
	run(t, workDir, "git", "config", "user.email", "test@test.com")
	run(t, workDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, workDir, "git", "add", "-A")
	run(t, workDir, "git", "commit", "-m", "initial")
	run(t, workDir, "git", "checkout", "-b", "feature")
	wantSHA := runOutput(t, workDir, "git", "rev-parse", "HEAD")

	bareDir := t.TempDir()
	run(t, "", "git", "clone", "--bare", workDir, bareDir)

	ctx := context.Background()
	got, err := ResolveRef(ctx, bareDir, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if got != wantSHA {
		t.Errorf("ResolveRef(branch) = %q, want %q", got, wantSHA)
	}
}

func TestResolveRefCommit(t *testing.T) {
	workDir := t.TempDir()
	run(t, workDir, "git", "init", "-b", "main")
	run(t, workDir, "git", "config", "user.email", "test@test.com")
	run(t, workDir, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, workDir, "git", "add", "-A")
	run(t, workDir, "git", "commit", "-m", "initial")
	wantSHA := runOutput(t, workDir, "git", "rev-parse", "HEAD")

	bareDir := t.TempDir()
	run(t, "", "git", "clone", "--bare", workDir, bareDir)

	ctx := context.Background()
	// Full 40-hex commit SHA should be accepted directly.
	got, err := ResolveRef(ctx, bareDir, wantSHA)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantSHA {
		t.Errorf("ResolveRef(commit) = %q, want %q", got, wantSHA)
	}
}

func TestHashDirStability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	h1, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("HashDir not stable: %s != %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("HashDir length = %d, want 64", len(h1))
	}
}

func TestHashDirRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := HashDir(dir)
	if err == nil {
		t.Fatal("expected error for symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %q, want symlink mention", err)
	}
}

func TestCopySubtreeRejectsSymlinks(t *testing.T) {
	srcDir := t.TempDir()
	target := filepath.Join(srcDir, "target.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(srcDir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	dstDir := t.TempDir()
	err := CopySubtree(srcDir, dstDir)
	if err == nil {
		t.Fatal("expected error for symlink")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %q, want symlink mention", err)
	}
}

func TestCacheLayout(t *testing.T) {
	commitSHA := setupGitHubRepo(t, "test", "repo", "actors/myactor", map[string]string{
		"actor.yaml": "name: myactor\nprotocol: 1\nrun: echo hi\n",
		"main.py":    "print('hello')\n",
	}, "tag", "v1.0.0")

	projectDir := t.TempDir()
	ctx := context.Background()

	actors := map[string]string{
		"myactor": "github:test/repo/actors/myactor@v1.0.0",
	}

	lf, err := Resolve(ctx, projectDir, actors, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(lf.Sources) != 1 {
		t.Fatalf("sources count = %d, want 1", len(lf.Sources))
	}

	source := "github:test/repo/actors/myactor@v1.0.0"
	entry, ok := lf.Sources[source]
	if !ok {
		t.Fatal("source not found in lock")
	}
	if entry.Commit != commitSHA {
		t.Errorf("commit = %q, want %q", entry.Commit, commitSHA)
	}
	if !strings.HasPrefix(entry.Hash, "sha256:") {
		t.Errorf("hash = %q, want sha256: prefix", entry.Hash)
	}

	// Verify cache directory exists.
	hash := strings.TrimPrefix(entry.Hash, "sha256:")
	cacheDir := filepath.Join(projectDir, ".scraps", "sources", hash)
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		t.Errorf("cache dir does not exist: %s", cacheDir)
	}

	// Verify actor.yaml is in cache.
	actorYAML := filepath.Join(cacheDir, "actor.yaml")
	if _, err := os.Stat(actorYAML); os.IsNotExist(err) {
		t.Errorf("actor.yaml not found in cache: %s", actorYAML)
	}

	// Verify hash stability — resolve again, should get same hash.
	lf2, err := Resolve(ctx, projectDir, actors, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry2 := lf2.Sources[source]
	if entry2.Hash != entry.Hash {
		t.Errorf("hash not stable: %q != %q", entry2.Hash, entry.Hash)
	}
}

func TestPruning(t *testing.T) {
	setupGitHubRepo(t, "test", "repo", "actors/a", map[string]string{
		"actor.yaml": "name: a\nprotocol: 1\nrun: echo a\n",
	}, "tag", "v1.0.0")

	projectDir := t.TempDir()
	ctx := context.Background()

	actors := map[string]string{
		"a": "github:test/repo/actors/a@v1.0.0",
	}

	lf, err := Resolve(ctx, projectDir, actors, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lf.Sources) != 1 {
		t.Fatalf("initial sources = %d, want 1", len(lf.Sources))
	}

	lockPath := filepath.Join(projectDir, "scraps.lock")
	if err := Write(lf, lockPath); err != nil {
		t.Fatal(err)
	}

	// Resolve with no sources — should produce an empty lock.
	emptyActors := map[string]string{}
	lf2, err := Resolve(ctx, projectDir, emptyActors, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lf2.Sources) != 0 {
		t.Errorf("pruned sources = %d, want 0", len(lf2.Sources))
	}
}

func TestFrozenMissingSource(t *testing.T) {
	projectDir := t.TempDir()
	ctx := context.Background()

	lf := &LockFile{
		Version: 1,
		Sources: make(map[string]LockEntry),
	}

	actors := map[string]string{
		"a": "github:test/repo/actors/a@v1.0.0",
	}

	err := Frozen(ctx, projectDir, actors, nil, lf)
	if err == nil {
		t.Fatal("expected error for missing source")
	}
	if !strings.Contains(err.Error(), "not found in lock file") {
		t.Errorf("error = %q, want 'not found in lock file'", err)
	}
}

func TestFrozenHashMismatch(t *testing.T) {
	commitSHA := setupGitHubRepo(t, "test", "repo", "actors/a", map[string]string{
		"actor.yaml": "name: a\nprotocol: 1\nrun: echo a\n",
	}, "tag", "v1.0.0")

	projectDir := t.TempDir()
	ctx := context.Background()

	// Lock with wrong hash.
	lf := &LockFile{
		Version: 1,
		Sources: map[string]LockEntry{
			"github:test/repo/actors/a@v1.0.0": {
				Commit: commitSHA,
				Hash:   "sha256:0000000000000000000000000000000000000000000000000000000000000000",
			},
		},
	}

	actors := map[string]string{
		"a": "github:test/repo/actors/a@v1.0.0",
	}

	err := Frozen(ctx, projectDir, actors, nil, lf)
	if err == nil {
		t.Fatal("expected error for hash mismatch")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("error = %q, want 'hash mismatch'", err)
	}
}

func TestFrozenRestoresMissingCache(t *testing.T) {
	commitSHA := setupGitHubRepo(t, "test", "repo", "actors/a", map[string]string{
		"actor.yaml": "name: a\nprotocol: 1\nrun: echo a\n",
	}, "tag", "v1.0.0")

	projectDir := t.TempDir()
	ctx := context.Background()

	actors := map[string]string{
		"a": "github:test/repo/actors/a@v1.0.0",
	}
	lf, err := Resolve(ctx, projectDir, actors, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the cache directory.
	entry := lf.Sources["github:test/repo/actors/a@v1.0.0"]
	hash := strings.TrimPrefix(entry.Hash, "sha256:")
	cacheDir := filepath.Join(projectDir, ".scraps", "sources", hash)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}

	frozenLF := &LockFile{
		Version: 1,
		Sources: map[string]LockEntry{
			"github:test/repo/actors/a@v1.0.0": {
				Commit: commitSHA,
				Hash:   entry.Hash,
			},
		},
	}

	// Frozen should restore the missing cache.
	if err := Frozen(ctx, projectDir, actors, nil, frozenLF); err != nil {
		t.Fatalf("Frozen failed: %s", err)
	}

	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		t.Error("cache not restored")
	}
}

func TestWriteAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scraps.lock")

	lf := &LockFile{
		Version: 1,
		Sources: map[string]LockEntry{
			"github:z/repo/path@v2.0.0": {
				Commit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Hash:   "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			},
			"github:a/repo/path@v1.0.0": {
				Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Hash:   "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			},
		},
	}

	if err := Write(lf, path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 {
		t.Errorf("version = %d, want 1", loaded.Version)
	}
	if len(loaded.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(loaded.Sources))
	}

	// Verify keys are sorted in the file.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	aIdx := strings.Index(content, "github:a/repo/path@v1.0.0")
	zIdx := strings.Index(content, "github:z/repo/path@v2.0.0")
	if aIdx < 0 || zIdx < 0 {
		t.Fatal("source keys not found in lock file")
	}
	if aIdx > zIdx {
		t.Error("lock file sources not sorted")
	}
}

func TestParseGitHubSource(t *testing.T) {
	tests := []struct {
		source    string
		wantOwner string
		wantRepo  string
		wantPath  string
		wantRef   string
		wantErr   bool
	}{
		{
			source:    "github:lighthousenz/scraps-actors/hocuspocus@v1.2.0",
			wantOwner: "lighthousenz",
			wantRepo:  "scraps-actors",
			wantPath:  "hocuspocus",
			wantRef:   "v1.2.0",
		},
		{
			source:    "github:owner/repo/deep/path/here@main",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantPath:  "deep/path/here",
			wantRef:   "main",
		},
		{
			source:  "invalid",
			wantErr: true,
		},
		{
			source:  "github:owner/repo@v1.0.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			parsed, err := ParseGitHubSource(tt.source)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", parsed.Owner, tt.wantOwner)
			}
			if parsed.Repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", parsed.Repo, tt.wantRepo)
			}
			if parsed.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", parsed.Path, tt.wantPath)
			}
			if parsed.Ref != tt.wantRef {
				t.Errorf("ref = %q, want %q", parsed.Ref, tt.wantRef)
			}
		})
	}
}

func TestCollectGitHubSources(t *testing.T) {
	actors := map[string]string{
		"b": "github:owner/repo/b@v1.0.0",
		"a": "github:owner/repo/a@v1.0.0",
		"c": "./actors/local",
	}
	checkers := []string{
		"github:owner/repo/b@v1.0.0",
		"github:owner/repo/checker@v2.0.0",
	}

	sources := CollectGitHubSources(actors, checkers)
	want := []string{
		"github:owner/repo/a@v1.0.0",
		"github:owner/repo/b@v1.0.0",
		"github:owner/repo/checker@v2.0.0",
	}

	if len(sources) != len(want) {
		t.Fatalf("sources = %v, want %v", sources, want)
	}
	for i := range want {
		if sources[i] != want[i] {
			t.Errorf("sources[%d] = %q, want %q", i, sources[i], want[i])
		}
	}
}
