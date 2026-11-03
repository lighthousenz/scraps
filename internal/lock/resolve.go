package lock

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
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// DefaultGitHubBaseURL is the default base URL for GitHub repositories.
const DefaultGitHubBaseURL = "https://github.com"

// ParsedSource holds the parsed components of a github: source string.
type ParsedSource struct {
	Owner string
	Repo  string
	Path  string
	Ref   string
	Raw   string
}

var githubSourceRe = regexp.MustCompile(`^github:([^/]+)/([^/]+)/(.+)@(.+)$`)

// ParseGitHubSource parses a "github:owner/repo/path@ref" string.
func ParseGitHubSource(source string) (*ParsedSource, error) {
	m := githubSourceRe.FindStringSubmatch(source)
	if m == nil {
		return nil, fmt.Errorf("invalid github source %q: expected github:owner/repo/path@ref", source)
	}
	return &ParsedSource{
		Owner: m[1],
		Repo:  m[2],
		Path:  m[3],
		Ref:   m[4],
		Raw:   source,
	}, nil
}

// RepoURL returns the repository URL using the given base URL.
func (p *ParsedSource) RepoURL(baseURL string) string {
	return baseURL + "/" + p.Owner + "/" + p.Repo
}

// ResolveRef resolves a ref (tag, branch, or commit) to a commit SHA using git ls-remote.
// For annotated tags it returns the underlying commit, not the tag object.
func ResolveRef(ctx context.Context, repoURL, ref string) (string, error) {
	// Query for both the short ref and its fully-qualified tag/branch forms,
	// including the peeled ^{} line that annotated tags produce.
	cmd := exec.CommandContext(ctx, "git", "ls-remote", repoURL,
		ref, "refs/tags/"+ref, "refs/tags/"+ref+"^{}", "refs/heads/"+ref)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w: %s", repoURL, ref, err, stderr.String())
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		// ref might be a full commit SHA; try to use it directly.
		if isHexSHA(ref) {
			return ref, nil
		}
		return "", fmt.Errorf("ref %q not found in %s", ref, repoURL)
	}

	type refLine struct {
		sha     string
		refname string
	}
	lines := strings.Split(output, "\n")
	var refs []refLine
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			refs = append(refs, refLine{sha: parts[0], refname: parts[1]})
		}
	}
	if len(refs) == 0 {
		return "", fmt.Errorf("unexpected git ls-remote output: %s", output)
	}

	// Prefer the dereferenced commit for annotated tags (refs/tags/<ref>^{}).
	for _, r := range refs {
		if r.refname == "refs/tags/"+ref+"^{}" {
			return r.sha, nil
		}
	}
	// Prefer exact ref matches in priority order.
	for _, candidate := range []string{
		"refs/tags/" + ref,
		"refs/heads/" + ref,
	} {
		for _, r := range refs {
			if r.refname == candidate {
				return r.sha, nil
			}
		}
	}
	// If only one line matched, use it.
	if len(refs) == 1 {
		return refs[0].sha, nil
	}
	// Multiple matches with no exact hit — ambiguous.
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.refname
	}
	return "", fmt.Errorf("ref %q is ambiguous in %s, matches: %s", ref, repoURL, strings.Join(names, ", "))
}

func isHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		isDigit := c >= '0' && c <= '9'
		isHex := c >= 'a' && c <= 'f'
		if !isDigit && !isHex {
			return false
		}
	}
	return true
}

// FetchCommit clones a specific commit shallowly into a temporary directory
// and returns the path to the checkout.
func FetchCommit(ctx context.Context, repoURL, commit, tmpDir string) (string, error) {
	dir := filepath.Join(tmpDir, "checkout")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating checkout dir: %w", err)
	}

	// git init
	if err := runGit(ctx, dir, "init"); err != nil {
		return "", fmt.Errorf("git init: %w", err)
	}

	// git fetch --depth 1 origin <commit>
	if err := runGit(ctx, dir, "fetch", "--depth", "1", repoURL, commit); err != nil {
		return "", fmt.Errorf("git fetch: %w", err)
	}

	// git checkout FETCH_HEAD
	if err := runGit(ctx, dir, "checkout", "FETCH_HEAD"); err != nil {
		return "", fmt.Errorf("git checkout: %w", err)
	}

	return dir, nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	// Prepend config overrides to neutralize user/system git settings that
	// affect working-tree content (autocrlf, eol, smudge/clean filters).
	// This keeps HashDir's output a property of the commit, not the machine.
	fullArgs := append([]string{
		"-c", "core.autocrlf=false",
		"-c", "core.eol=lf",
		"-c", "core.symlinks=true",
		"-c", "filter.lfs.smudge=",
		"-c", "filter.lfs.process=",
		"-c", "filter.lfs.clean=",
		"-c", "filter.lfs.required=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}
	return nil
}

// CopySubtree copies the path subtree from src into dst, preserving file modes.
// It rejects symlinks and special files.
func CopySubtree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink found at %s: symlinks are not allowed in actor sources", rel)
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("special file found at %s: only regular files are allowed in actor sources", rel)
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}
		perm := info.Mode().Perm()
		return os.WriteFile(target, data, perm)
	})
}

// HashDir computes the content hash of a directory.
// The hash is sha256 over every regular file, taken in byte order of their
// slash-separated relative paths. For each file: <relative path>\0<mode>\0<decimal length>\0<bytes>.
// Mode is "644" or "755". Symlinks and special files are errors.
func HashDir(dir string) (string, error) {
	type entry struct {
		rel  string
		mode string
		data []byte
	}

	var entries []entry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
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
		// Normalize to forward slashes.
		rel = filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink found at %s: symlinks are not allowed", rel)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("special file found at %s: only regular files are allowed", rel)
		}

		mode := "644"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "755"
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}

		entries = append(entries, entry{rel: rel, mode: mode, data: data})
		return nil
	})
	if err != nil {
		return "", err
	}

	// Sort by relative path (byte order).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rel < entries[j].rel
	})

	h := sha256.New()
	nul := []byte{0}
	for _, e := range entries {
		_, _ = io.WriteString(h, e.rel)
		_, _ = h.Write(nul)
		_, _ = io.WriteString(h, e.mode)
		_, _ = h.Write(nul)
		_, _ = fmt.Fprintf(h, "%d", len(e.data))
		_, _ = h.Write(nul)
		_, _ = h.Write(e.data)
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// GitHubBaseURL returns SCRAPS_GITHUB_BASE_URL if set, otherwise DefaultGitHubBaseURL.
func GitHubBaseURL() string {
	if v := os.Getenv("SCRAPS_GITHUB_BASE_URL"); v != "" {
		return v
	}
	return DefaultGitHubBaseURL
}

// CollectGitHubSources returns all unique github: sources from a project's actors and checkers.
func CollectGitHubSources(actors map[string]string, checkers []string) []string {
	seen := make(map[string]bool)
	var sources []string
	for _, source := range actors {
		if IsGitHubSource(source) && !seen[source] {
			seen[source] = true
			sources = append(sources, source)
		}
	}
	for _, source := range checkers {
		if IsGitHubSource(source) && !seen[source] {
			seen[source] = true
			sources = append(sources, source)
		}
	}
	sort.Strings(sources)
	return sources
}

// Resolve resolves and fetches all GitHub sources, returning a new lock file.
// It populates the source cache under projectDir/.scraps/sources/.
func Resolve(ctx context.Context, projectDir string, actors map[string]string, checkers []string) (*LockFile, error) {
	baseURL := GitHubBaseURL()
	sources := CollectGitHubSources(actors, checkers)

	lf := &LockFile{
		Version: 1,
		Sources: make(map[string]LockEntry),
	}

	for _, source := range sources {
		parsed, err := ParseGitHubSource(source)
		if err != nil {
			return nil, fmt.Errorf("parsing source %q: %w", source, err)
		}

		repoURL := parsed.RepoURL(baseURL)
		commit, err := ResolveRef(ctx, repoURL, parsed.Ref)
		if err != nil {
			return nil, fmt.Errorf("resolving %q: %w", source, err)
		}

		// Fetch the commit shallowly.
		tmpDir, err := os.MkdirTemp("", "scraps-fetch-*")
		if err != nil {
			return nil, fmt.Errorf("creating temp dir: %w", err)
		}

		checkoutDir, err := FetchCommit(ctx, repoURL, commit, tmpDir)
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("fetching %q: %w", source, err)
		}

		// Copy the path subtree into a temporary location and compute its hash.
		subtreeDir := filepath.Join(checkoutDir, parsed.Path)
		if _, err := os.Stat(subtreeDir); os.IsNotExist(err) {
			_ = os.RemoveAll(tmpDir)
			commitPrefix := commit
			if len(commitPrefix) > 12 {
				commitPrefix = commitPrefix[:12]
			}
			return nil, fmt.Errorf("path %q not found in %s at %s", parsed.Path, parsed.Owner+"/"+parsed.Repo, commitPrefix)
		}

		hash, err := HashDir(subtreeDir)
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("hashing %q: %w", source, err)
		}

		// Copy to cache.
		cacheDir := filepath.Join(projectDir, ".scraps", "sources", hash)
		if err := os.RemoveAll(cacheDir); err != nil {
			_ = os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("cleaning cache for %q: %w", source, err)
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			_ = os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("creating cache dir for %q: %w", source, err)
		}
		if err := CopySubtree(subtreeDir, cacheDir); err != nil {
			_ = os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("copying subtree for %q: %w", source, err)
		}

		_ = os.RemoveAll(tmpDir)

		lf.Sources[source] = LockEntry{
			Commit: commit,
			Hash:   "sha256:" + hash,
		}
	}

	return lf, nil
}

// Frozen verifies the lock file against declared sources, fetching missing caches.
// It returns an error if any source is missing from the lock or if a hash mismatches.
func Frozen(ctx context.Context, projectDir string, actors map[string]string, checkers []string, lf *LockFile) error {
	baseURL := GitHubBaseURL()
	sources := CollectGitHubSources(actors, checkers)

	for _, source := range sources {
		entry, ok := lf.Sources[source]
		if !ok {
			return &MismatchError{Message: fmt.Sprintf("source %q not found in lock file", source)}
		}

		hash := entry.Hash
		if idx := strings.Index(hash, ":"); idx >= 0 {
			hash = hash[idx+1:]
		}

		cacheDir := filepath.Join(projectDir, ".scraps", "sources", hash)
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			// Cache is missing; fetch and verify.
			parsed, err := ParseGitHubSource(source)
			if err != nil {
				return fmt.Errorf("parsing source %q: %w", source, err)
			}

			repoURL := parsed.RepoURL(baseURL)
			tmpDir, err := os.MkdirTemp("", "scraps-frozen-*")
			if err != nil {
				return fmt.Errorf("creating temp dir: %w", err)
			}

			checkoutDir, err := FetchCommit(ctx, repoURL, entry.Commit, tmpDir)
			if err != nil {
				_ = os.RemoveAll(tmpDir)
				return fmt.Errorf("fetching %q: %w", source, err)
			}

			subtreeDir := filepath.Join(checkoutDir, parsed.Path)
			if _, err := os.Stat(subtreeDir); os.IsNotExist(err) {
				_ = os.RemoveAll(tmpDir)
				commitPrefix := entry.Commit
				if len(commitPrefix) > 12 {
					commitPrefix = commitPrefix[:12]
				}
				return &MismatchError{Message: fmt.Sprintf("path %q not found in %s at %s", parsed.Path, parsed.Owner+"/"+parsed.Repo, commitPrefix)}
			}

			actualHash, err := HashDir(subtreeDir)
			if err != nil {
				_ = os.RemoveAll(tmpDir)
				return fmt.Errorf("hashing %q: %w", source, err)
			}

			if actualHash != hash {
				_ = os.RemoveAll(tmpDir)
				return &MismatchError{Message: fmt.Sprintf("hash mismatch for %q: expected sha256:%s, got sha256:%s", source, hash, actualHash)}
			}

			// Copy to cache.
			if err := os.MkdirAll(cacheDir, 0o755); err != nil {
				_ = os.RemoveAll(tmpDir)
				return fmt.Errorf("creating cache dir for %q: %w", source, err)
			}
			if err := CopySubtree(subtreeDir, cacheDir); err != nil {
				_ = os.RemoveAll(tmpDir)
				return fmt.Errorf("copying subtree for %q: %w", source, err)
			}

			_ = os.RemoveAll(tmpDir)
		} else if err == nil {
			// Cache exists; verify its hash.
			actualHash, err := HashDir(cacheDir)
			if err != nil {
				return fmt.Errorf("hashing cached %q: %w", source, err)
			}
			if actualHash != hash {
				return &MismatchError{Message: fmt.Sprintf("hash mismatch for %q: expected sha256:%s, got sha256:%s", source, hash, actualHash)}
			}
		} else {
			return fmt.Errorf("checking cache for %q: %w", source, err)
		}
	}

	return nil
}

// Write writes a lock file to disk with sorted source keys.
func Write(lf *LockFile, path string) error {
	// Build sorted output.
	type yamlEntry struct {
		Key   string
		Entry LockEntry
	}
	var sorted []yamlEntry
	for k, v := range lf.Sources {
		sorted = append(sorted, yamlEntry{Key: k, Entry: v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Key < sorted[j].Key
	})

	// Build ordered map for YAML output.
	sources := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
	}
	for _, e := range sorted {
		sources.Content = append(sources.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.Key, Style: yaml.DoubleQuotedStyle},
			&yaml.Node{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
				Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "commit"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.Entry.Commit},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "hash"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: e.Entry.Hash},
				},
			},
		)
	}

	doc := &yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{
			{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
				Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"},
					{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"},
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "sources"},
					sources,
				},
			},
		},
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encoding lock file: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("closing encoder: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating lock file directory: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
