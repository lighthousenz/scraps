package main_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var builtBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "scraps-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	builtBin = filepath.Join(dir, "scraps")
	cmd := exec.CommandContext(context.Background(), "go", "build",
		"-ldflags", "-X main.version=test-version",
		"-o", builtBin, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "building scraps: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func TestSubcommandStubsExit3(t *testing.T) {
	bin := builtBin
	// validate is implemented; excluded from stub tests.
	cmds := []string{"init", "lock", "schema", "run", "diagram", "explain"}
	for _, name := range cmds {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), bin, name)
			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("expected exit error, got %v (output: %s)", err, out)
			}
			if exitErr.ExitCode() != 3 {
				t.Fatalf("expected exit code 3, got %d (output: %s)", exitErr.ExitCode(), out)
			}
			want := "scraps " + name + ": not implemented"
			if !strings.Contains(string(out), want) {
				t.Fatalf("expected output to contain %q, got %q", want, string(out))
			}
		})
	}
}

func TestValidateExitsWithProblems(t *testing.T) {
	bin := builtBin
	// validate without a scraps.yaml should exit 2
	cmd := exec.CommandContext(t.Context(), bin, "validate")
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got %v (output: %s)", err, out)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("expected exit code 2, got %d (output: %s)", exitErr.ExitCode(), out)
	}
	if !strings.Contains(string(out), "scraps.yaml not found") {
		t.Fatalf("expected scraps.yaml not found, got %q", string(out))
	}
}

func TestSubcommandHelpExit0(t *testing.T) {
	bin := builtBin
	cmds := []string{"init", "lock", "schema", "validate", "run", "diagram", "explain"}
	for _, name := range cmds {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), bin, name, "--help")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s --help failed: %v (output: %s)", name, err, out)
			}
		})
	}
}

func TestVersionFlag(t *testing.T) {
	bin := builtBin
	cmd := exec.CommandContext(t.Context(), bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--version failed: %v (output: %s)", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "test-version" {
		t.Fatalf("--version: got %q, want %q", got, "test-version")
	}
}
