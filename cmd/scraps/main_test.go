package main_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func scrapsPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := dir + "/scraps"
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	cmd.Dir = "."
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building scraps: %v", err)
	}
	return bin
}

func TestSubcommandStubsExit3(t *testing.T) {
	bin := scrapsPath(t)
	cmds := []string{"init", "lock", "schema", "validate", "run", "diagram", "explain"}
	for _, name := range cmds {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			cmd := exec.CommandContext(ctx, bin, name)
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

func TestVersionFlag(t *testing.T) {
	bin := scrapsPath(t)
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, bin, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--version failed: %v (output: %s)", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got == "" {
		t.Fatal("--version produced empty output")
	}
	if got != "dev" {
		t.Logf("version is %q (expected 'dev' in test build)", got)
	}
}
