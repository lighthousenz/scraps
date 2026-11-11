package actor_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lighthousenz/scraps/internal/actor"
)

func TestPrepareCacheHit(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.mjs"), []byte("console.log('hello')"), 0o644); err != nil {
		t.Fatalf("write main.mjs: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")

	cfg := &actor.PrepareConfig{
		SourceDir:        srcDir,
		CacheDir:         cacheDir,
		Tools:            nil,
		PrepareRun:       "",
		CacheKeySubjects: nil,
		Subjects:         nil,
	}

	ctx := context.Background()

	// First call: cache miss, copies source.
	res1, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if res1.Dir == "" {
		t.Fatal("expected non-empty dir")
	}

	// Check that main.mjs was copied.
	content, err := os.ReadFile(filepath.Join(res1.Dir, "main.mjs"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(content) != "console.log('hello')" {
		t.Errorf("unexpected content: %s", content)
	}

	// Check marker exists.
	if _, err := os.Stat(filepath.Join(res1.Dir, ".scraps-prepared")); err != nil {
		t.Error("prepared marker not found")
	}

	// Second call: cache hit, returns same directory.
	res2, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if res2.Dir != res1.Dir {
		t.Errorf("expected same dir on cache hit: %s != %s", res1.Dir, res2.Dir)
	}
}

func TestPrepareHalfPreparedRetry(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "app.py"), []byte("print('hi')"), 0o644); err != nil {
		t.Fatalf("write app.py: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")

	cfg := &actor.PrepareConfig{
		SourceDir: srcDir,
		CacheDir:  cacheDir,
	}

	ctx := context.Background()

	// First call to establish the directory.
	res1, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}

	// Simulate half-prepared: remove the marker.
	if err := os.Remove(filepath.Join(res1.Dir, ".scraps-prepared")); err != nil {
		t.Fatalf("remove marker: %v", err)
	}

	// Prepare again should delete and re-copy.
	res2, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}

	// Should have the marker now.
	if _, err := os.Stat(filepath.Join(res2.Dir, ".scraps-prepared")); err != nil {
		t.Error("prepared marker not found after retry")
	}

	// File should still be there.
	content, err := os.ReadFile(filepath.Join(res2.Dir, "app.py"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "print('hi')" {
		t.Errorf("unexpected content: %s", content)
	}
}

func TestPrepareConcurrent(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "index.js"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write index.js: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")

	// Use a prepare command that creates a unique file. If two runs execute
	// concurrently, they would create conflicting files.
	var prepareCount atomic.Int32

	cfg := &actor.PrepareConfig{
		SourceDir:        srcDir,
		CacheDir:         cacheDir,
		Tools:            nil,
		PrepareRun:       "",
		CacheKeySubjects: nil,
		Subjects:         nil,
	}

	ctx := context.Background()

	// Run 10 concurrent prepares.
	var wg sync.WaitGroup
	var dirs []string
	var mu sync.Mutex
	var errs []error

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prepareCount.Add(1)
			res, err := actor.Prepare(ctx, cfg)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			dirs = append(dirs, res.Dir)
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("prepare errors: %v", errs)
	}

	// All should return the same directory.
	for i, d := range dirs {
		if d != dirs[0] {
			t.Errorf("dirs[%d] = %s, want %s", i, d, dirs[0])
		}
	}
}

func TestPrepareWithResolver(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "setup.sh"), []byte("echo setup"), 0o644); err != nil {
		t.Fatalf("write setup.sh: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")

	subjects := map[string]string{"yjs": "13.6.27"}

	// Use a simple prepare command that writes the resolved version to a file.
	cfg := &actor.PrepareConfig{
		SourceDir:        srcDir,
		CacheDir:         cacheDir,
		Tools:            nil,
		PrepareRun:       "echo ${{ subjects.yjs }} > version.txt",
		CacheKeySubjects: []string{"yjs"},
		Subjects:         subjects,
		Resolver: func(expr string) (string, error) {
			// Simple resolver for subjects.X
			if strings.HasPrefix(expr, "subjects.") {
				name := strings.TrimPrefix(expr, "subjects.")
				if v, ok := subjects[name]; ok {
					return v, nil
				}
			}
			return "", nil
		},
	}

	ctx := context.Background()
	res, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// Check that version.txt was created with the resolved version.
	content, err := os.ReadFile(filepath.Join(res.Dir, "version.txt"))
	if err != nil {
		t.Fatalf("read version.txt: %v", err)
	}
	if strings.TrimSpace(string(content)) != "13.6.27" {
		t.Errorf("expected '13.6.27', got %q", strings.TrimSpace(string(content)))
	}
}

func TestPrepareFailure(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.py"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write main.py: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")

	cfg := &actor.PrepareConfig{
		SourceDir:  srcDir,
		CacheDir:   cacheDir,
		PrepareRun: "exit 1",
	}

	ctx := context.Background()
	_, err := actor.Prepare(ctx, cfg)
	if err == nil {
		t.Fatal("expected error from failing prepare command")
	}
	if !strings.Contains(err.Error(), "prepare command failed") {
		t.Errorf("expected 'prepare command failed' in error, got: %v", err)
	}

	// The directory should have been cleaned up.
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".lock") {
			t.Errorf("unexpected entry in cache dir after failed prepare: %s", e.Name())
		}
	}
}

func TestPrepareDifferentSubjectVersions(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.js"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write main.js: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")

	cfg1 := &actor.PrepareConfig{
		SourceDir:        srcDir,
		CacheDir:         cacheDir,
		CacheKeySubjects: []string{"yjs"},
		Subjects:         map[string]string{"yjs": "1.0.0"},
	}

	cfg2 := &actor.PrepareConfig{
		SourceDir:        srcDir,
		CacheDir:         cacheDir,
		CacheKeySubjects: []string{"yjs"},
		Subjects:         map[string]string{"yjs": "2.0.0"},
	}

	ctx := context.Background()

	res1, err := actor.Prepare(ctx, cfg1)
	if err != nil {
		t.Fatalf("prepare v1: %v", err)
	}

	res2, err := actor.Prepare(ctx, cfg2)
	if err != nil {
		t.Fatalf("prepare v2: %v", err)
	}

	// Different versions should produce different directories.
	if res1.Dir == res2.Dir {
		t.Error("expected different directories for different subject versions")
	}
}

func TestPrepareDifferentTools(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.js"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write main.js: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")

	cfg1 := &actor.PrepareConfig{
		SourceDir: srcDir,
		CacheDir:  cacheDir,
		Tools:     map[string]string{"node": "22.0.0"},
	}

	cfg2 := &actor.PrepareConfig{
		SourceDir: srcDir,
		CacheDir:  cacheDir,
		Tools:     map[string]string{"node": "23.0.0"},
	}

	ctx := context.Background()

	res1, err := actor.Prepare(ctx, cfg1)
	if err != nil {
		t.Fatalf("prepare with node 22: %v", err)
	}

	res2, err := actor.Prepare(ctx, cfg2)
	if err != nil {
		t.Fatalf("prepare with node 23: %v", err)
	}

	if res1.Dir == res2.Dir {
		t.Error("expected different directories for different tool versions")
	}
}

func TestPrepareNoPrepareCommand(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "actor.yaml"), []byte("name: test"), 0o644); err != nil {
		t.Fatalf("write actor.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.py"), []byte("print('hi')"), 0o644); err != nil {
		t.Fatalf("write main.py: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")

	cfg := &actor.PrepareConfig{
		SourceDir:  srcDir,
		CacheDir:   cacheDir,
		PrepareRun: "",
	}

	ctx := context.Background()
	res, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// Should still have the files.
	if _, err := os.Stat(filepath.Join(res.Dir, "actor.yaml")); err != nil {
		t.Error("actor.yaml not copied")
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "main.py")); err != nil {
		t.Error("main.py not copied")
	}

	// Should have the marker.
	if _, err := os.Stat(filepath.Join(res.Dir, ".scraps-prepared")); err != nil {
		t.Error("prepared marker not found")
	}
}

func TestPrepareSymlinksCopied(t *testing.T) {
	srcDir := t.TempDir()

	// Create a regular file and a symlink to it.
	if err := os.WriteFile(filepath.Join(srcDir, "target.js"), []byte("module.exports = {}"), 0o644); err != nil {
		t.Fatalf("write target.js: %v", err)
	}
	if err := os.Symlink("target.js", filepath.Join(srcDir, "link.js")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	cfg := &actor.PrepareConfig{
		SourceDir: srcDir,
		CacheDir:  cacheDir,
	}

	ctx := context.Background()
	res, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// The symlink should exist in the prepared dir.
	linkPath := filepath.Join(res.Dir, "link.js")
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat link.js in prepared dir: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("expected link.js to be a symlink in the prepared dir")
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "target.js" {
		t.Errorf("expected symlink target 'target.js', got %q", target)
	}
}

func TestPrepareSymlinkChangesInvalidateCache(t *testing.T) {
	srcDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "a.js"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write a.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "b.js"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write b.js: %v", err)
	}
	if err := os.Symlink("a.js", filepath.Join(srcDir, "link.js")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	cfg := &actor.PrepareConfig{
		SourceDir: srcDir,
		CacheDir:  cacheDir,
	}

	ctx := context.Background()

	res1, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}

	// Re-point the symlink.
	if err := os.Remove(filepath.Join(srcDir, "link.js")); err != nil {
		t.Fatalf("remove symlink: %v", err)
	}
	if err := os.Symlink("b.js", filepath.Join(srcDir, "link.js")); err != nil {
		t.Fatalf("create new symlink: %v", err)
	}

	res2, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}

	if res1.Dir == res2.Dir {
		t.Error("expected different cache dirs when symlink target changes")
	}
}

func TestPrepareOutsideTreeSymlink(t *testing.T) {
	// Create an external file outside the source tree.
	externalDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(externalDir, "shared.js"), []byte("module.exports = 'shared'"), 0o644); err != nil {
		t.Fatalf("write shared.js: %v", err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.js"), []byte("require('./lib')"), 0o644); err != nil {
		t.Fatalf("write main.js: %v", err)
	}

	// Create a symlink that points outside the source tree.
	if err := os.Symlink(filepath.Join(externalDir, "shared.js"), filepath.Join(srcDir, "lib.js")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	cfg := &actor.PrepareConfig{
		SourceDir: srcDir,
		CacheDir:  cacheDir,
	}

	ctx := context.Background()
	res, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// The outside-tree symlink should be materialised as a regular file
	// containing the target's content.
	libPath := filepath.Join(res.Dir, "lib.js")
	fi, err := os.Lstat(libPath)
	if err != nil {
		t.Fatalf("lstat lib.js in prepared dir: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("expected lib.js to be a regular file (materialised from outside-tree symlink), not a symlink")
	}

	content, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatalf("read lib.js: %v", err)
	}
	if string(content) != "module.exports = 'shared'" {
		t.Errorf("unexpected content: %s", content)
	}
}

func TestPrepareOutsideTreeSymlinkChangesInvalidateCache(t *testing.T) {
	externalDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(externalDir, "shared.js"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("write shared.js: %v", err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.js"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write main.js: %v", err)
	}
	if err := os.Symlink(filepath.Join(externalDir, "shared.js"), filepath.Join(srcDir, "lib.js")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	cfg := &actor.PrepareConfig{
		SourceDir: srcDir,
		CacheDir:  cacheDir,
	}

	ctx := context.Background()

	res1, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}

	// Change the external file content.
	if err := os.WriteFile(filepath.Join(externalDir, "shared.js"), []byte("v2"), 0o644); err != nil {
		t.Fatalf("update shared.js: %v", err)
	}

	res2, err := actor.Prepare(ctx, cfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}

	if res1.Dir == res2.Dir {
		t.Error("expected different cache dirs when outside-tree symlink target content changes")
	}
}

func TestPrepareCancelledContext(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.js"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write main.js: %v", err)
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	cfg := &actor.PrepareConfig{
		SourceDir:  srcDir,
		CacheDir:   cacheDir,
		PrepareRun: "sleep 60",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := actor.Prepare(ctx, cfg)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestPrepareOutsideTreeSymlinkMissingTarget(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.js"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write main.js: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "gone.js")
	if err := os.Symlink(missing, filepath.Join(srcDir, "lib.js")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	cfg := &actor.PrepareConfig{SourceDir: srcDir, CacheDir: filepath.Join(t.TempDir(), "cache")}
	_, err := actor.Prepare(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected prepare to fail for a dangling outside-tree symlink")
	}
	for _, want := range []string{"lib.js", "target is missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestPrepareOutsideTreeSymlinkToDirectory(t *testing.T) {
	externalDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(externalDir, "shared.js"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("write shared.js: %v", err)
	}
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.js"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write main.js: %v", err)
	}
	if err := os.Symlink(externalDir, filepath.Join(srcDir, "lib")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	cfg := &actor.PrepareConfig{SourceDir: srcDir, CacheDir: filepath.Join(t.TempDir(), "cache")}
	_, err := actor.Prepare(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected prepare to fail for an outside-tree directory symlink")
	}
	for _, want := range []string{"lib", "directory outside source tree"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
