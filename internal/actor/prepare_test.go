package actor_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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

	// First call: cache miss, copies source.
	res1, err := actor.Prepare(cfg)
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
	res2, err := actor.Prepare(cfg)
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

	// First call to establish the directory.
	res1, err := actor.Prepare(cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}

	// Simulate half-prepared: remove the marker.
	if err := os.Remove(filepath.Join(res1.Dir, ".scraps-prepared")); err != nil {
		t.Fatalf("remove marker: %v", err)
	}

	// Prepare again should delete and re-copy.
	res2, err := actor.Prepare(cfg)
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
			res, err := actor.Prepare(cfg)
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

	res, err := actor.Prepare(cfg)
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

	_, err := actor.Prepare(cfg)
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

	res1, err := actor.Prepare(cfg1)
	if err != nil {
		t.Fatalf("prepare v1: %v", err)
	}

	res2, err := actor.Prepare(cfg2)
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

	res1, err := actor.Prepare(cfg1)
	if err != nil {
		t.Fatalf("prepare with node 22: %v", err)
	}

	res2, err := actor.Prepare(cfg2)
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

	res, err := actor.Prepare(cfg)
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
