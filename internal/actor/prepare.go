package actor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	preparedMarker = ".scraps-prepared"
	maxOutputLines = 50
	// lockPollInterval is the interval for polling the lock in non-blocking mode.
	lockPollInterval = 100 * time.Millisecond
)

// SubjectResolver resolves expressions like ${{ subjects.X }} in a prepare.run command.
type SubjectResolver func(expr string) (string, error)

// PrepareConfig holds configuration for the prepare step.
type PrepareConfig struct {
	// SourceDir is the actor's source directory.
	SourceDir string
	// CacheDir is the base cache directory (e.g., ".scraps/cache/prepared/").
	CacheDir string
	// Tools from the actor definition.
	Tools map[string]string
	// PrepareRun is the prepare command (may contain ${{ subjects.X }}).
	// Empty string means no prepare step.
	PrepareRun string
	// CacheKeySubjects is the list of subject names in prepare.cache-key.
	CacheKeySubjects []string
	// Subjects is the full set of resolved subject versions.
	Subjects map[string]string
	// Resolver resolves ${{ subjects.X }} expressions in PrepareRun.
	Resolver SubjectResolver
}

// PrepareResult holds the result of a prepare operation.
type PrepareResult struct {
	// Dir is the path to the prepared directory.
	Dir string
}

// Prepare ensures the actor's prepared directory exists and is ready.
// It uses file locking for concurrency safety. The context controls
// cancellation of the lock acquisition and the prepare command.
func Prepare(ctx context.Context, cfg *PrepareConfig) (*PrepareResult, error) {
	key, err := computeCacheKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("compute cache key: %w", err)
	}

	preparedDir := filepath.Join(cfg.CacheDir, key)
	lockPath := preparedDir + ".lock"

	if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}

	// Acquire exclusive file lock with context-aware polling.
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	defer func() { _ = lockFile.Close() }()

	if err := flockWithContext(ctx, lockFile, syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	// Check if already prepared.
	markerPath := filepath.Join(preparedDir, preparedMarker)
	if _, err := os.Stat(markerPath); err == nil {
		return &PrepareResult{Dir: preparedDir}, nil
	}

	// Remove incomplete directory if it exists.
	if _, err := os.Stat(preparedDir); err == nil {
		if err := os.RemoveAll(preparedDir); err != nil {
			return nil, fmt.Errorf("remove incomplete prepared dir: %w", err)
		}
	}

	// Copy source directory.
	if err := copyDir(cfg.SourceDir, preparedDir); err != nil {
		return nil, fmt.Errorf("copy source directory: %w", err)
	}

	// Run prepare command if specified.
	if cfg.PrepareRun != "" {
		prepareCmd := cfg.PrepareRun
		if cfg.Resolver != nil {
			prepareCmd, err = resolveExpressions(prepareCmd, cfg.Resolver)
			if err != nil {
				_ = os.RemoveAll(preparedDir)
				return nil, fmt.Errorf("resolve expressions in prepare.run: %w", err)
			}
		}

		if err := runPrepareCommand(ctx, cfg.Tools, prepareCmd, preparedDir); err != nil {
			_ = os.RemoveAll(preparedDir)
			return nil, err
		}
	}

	// Write marker.
	if err := os.WriteFile(markerPath, []byte("prepared\n"), 0o644); err != nil {
		_ = os.RemoveAll(preparedDir)
		return nil, fmt.Errorf("write prepared marker: %w", err)
	}

	return &PrepareResult{Dir: preparedDir}, nil
}

// flockWithContext acquires a file lock using non-blocking polling, respecting
// context cancellation and deadlines.
func flockWithContext(ctx context.Context, f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK {
			return err
		}
		// Lock is held by another process; poll with context awareness.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

func computeCacheKey(cfg *PrepareConfig) (string, error) {
	h := sha256.New()

	// Hash the source directory contents.
	contentHash, err := hashDir(cfg.SourceDir)
	if err != nil {
		return "", fmt.Errorf("hash source directory: %w", err)
	}
	h.Write(contentHash)

	// Hash the sorted tools.
	type toolEntry struct {
		name    string
		version string
	}
	sortedTools := make([]toolEntry, 0, len(cfg.Tools))
	for name, version := range cfg.Tools {
		sortedTools = append(sortedTools, toolEntry{name, version})
	}
	sort.Slice(sortedTools, func(i, j int) bool {
		return sortedTools[i].name < sortedTools[j].name
	})
	for _, t := range sortedTools {
		_, _ = fmt.Fprintf(h, "tool:%s=%s\n", t.name, t.version)
	}

	// Hash the sorted name=version pairs from cache-key subjects.
	sortedSubjects := make([]string, len(cfg.CacheKeySubjects))
	copy(sortedSubjects, cfg.CacheKeySubjects)
	sort.Strings(sortedSubjects)
	for _, name := range sortedSubjects {
		version, ok := cfg.Subjects[name]
		if !ok {
			version = ""
		}
		_, _ = fmt.Fprintf(h, "subject:%s=%s\n", name, version)
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func hashDir(dir string) ([]byte, error) {
	h := sha256.New()

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("abs dir: %w", err)
	}

	type fileEntry struct {
		relPath  string
		hashPath string // the path whose content we hash (may differ for outside-tree symlinks)
		symlink  bool
	}
	var files []fileEntry

	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// Use forward slashes for stability.
		rel = filepath.ToSlash(rel)

		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", rel, err)
			}

			resolved := target
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(path), resolved)
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				return fmt.Errorf("resolve symlink %s: %w", rel, err)
			}

			if isInsideDir(resolved, absDir) {
				// In-tree symlink: hash the link text (the copy recreates the link).
				files = append(files, fileEntry{relPath: rel, hashPath: path, symlink: true})
			} else {
				// Outside-tree symlink: hash the target content (the copy
				// materialises the target as a regular file). Hashing runs before
				// copyDir, so reject unusable targets here with copyDir's messages.
				fi, err := os.Stat(resolved)
				if err != nil {
					return fmt.Errorf("symlink %s points outside source tree and target is missing: %w", rel, err)
				}
				if fi.IsDir() {
					return fmt.Errorf("symlink %s points to directory outside source tree; not supported", rel)
				}
				files = append(files, fileEntry{relPath: rel, hashPath: resolved, symlink: false})
			}
			return nil
		}

		if !d.Type().IsRegular() {
			return fmt.Errorf("unsupported file type %s for %s", d.Type(), rel)
		}
		files = append(files, fileEntry{relPath: rel, hashPath: path, symlink: false})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort by relative path in byte order.
	sort.Slice(files, func(i, j int) bool {
		return files[i].relPath < files[j].relPath
	})

	for _, f := range files {
		if f.symlink {
			// In-tree symlink: hash the link target string.
			target, err := os.Readlink(f.hashPath)
			if err != nil {
				return nil, err
			}
			_, _ = fmt.Fprintf(h, "symlink:%s\x00%s\x00", f.relPath, target)
			continue
		}

		info, err := os.Lstat(f.hashPath)
		if err != nil {
			return nil, err
		}

		mode := "644"
		if info.Mode()&0o111 != 0 {
			mode = "755"
		}

		content, err := os.ReadFile(f.hashPath)
		if err != nil {
			return nil, err
		}

		// Input: <relative path>\0<mode>\0<decimal length>\0<bytes>
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00", f.relPath, mode, len(content))
		h.Write(content)
	}

	return h.Sum(nil), nil
}

func copyDir(src, dst string) error {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("abs source: %w", err)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", rel, err)
			}

			// Resolve the symlink's absolute destination.
			resolved := linkTarget
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(filepath.Dir(path), resolved)
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				return fmt.Errorf("resolve symlink %s: %w", rel, err)
			}

			// If the symlink points within the source tree, recreate it as a
			// link (it will resolve correctly in the copy).
			if isInsideDir(resolved, absSrc) {
				return os.Symlink(linkTarget, target)
			}

			// Outside-tree symlink: verify the target exists, then copy its
			// content so the prepared dir is self-contained.
			fi, err := os.Stat(resolved)
			if err != nil {
				return fmt.Errorf("symlink %s points outside source tree and target is missing: %w", rel, err)
			}
			if fi.IsDir() {
				return fmt.Errorf("symlink %s points to directory outside source tree; not supported", rel)
			}
			return copyFile(resolved, target)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("unsupported file type %s for %s", d.Type(), rel)
		}
		return copyFile(path, target)
	})
}

// isInsideDir reports whether path is inside dir. Both must be absolute and clean.
func isInsideDir(path, dir string) bool {
	// Ensure dir ends with separator for prefix comparison.
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	return strings.HasPrefix(filepath.Clean(path)+string(filepath.Separator), prefix)
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func resolveExpressions(cmd string, resolver SubjectResolver) (string, error) {
	result := cmd
	for {
		start := strings.Index(result, "${{")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], "}}")
		if end == -1 {
			break
		}
		end += start + 2
		expr := strings.TrimSpace(result[start+3 : end-2])
		resolved, err := resolver(expr)
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", expr, err)
		}
		result = result[:start] + resolved + result[end:]
	}
	return result, nil
}

func runPrepareCommand(ctx context.Context, tools map[string]string, cmd string, dir string) error {
	name, args, err := BuildCommand(tools, cmd)
	if err != nil {
		return fmt.Errorf("build prepare command: %w", err)
	}

	execCmd := exec.CommandContext(ctx, name, args...)
	execCmd.Dir = dir
	execCmd.Env = os.Environ()
	execCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// On context cancellation, send SIGTERM to the process group so children
	// are also stopped. WaitDelay bounds how long Run waits for I/O to drain
	// after the cancel function fires.
	execCmd.Cancel = func() error {
		if execCmd.Process == nil {
			return nil
		}
		return syscall.Kill(-execCmd.Process.Pid, syscall.SIGTERM)
	}
	execCmd.WaitDelay = 5 * time.Second

	var output bytes.Buffer
	execCmd.Stdout = &output
	execCmd.Stderr = &output

	if err := execCmd.Run(); err != nil {
		lines := strings.Split(output.String(), "\n")
		if len(lines) > maxOutputLines {
			lines = lines[len(lines)-maxOutputLines:]
		}
		return fmt.Errorf("prepare command failed: %w\n%s", err, strings.Join(lines, "\n"))
	}
	return nil
}
