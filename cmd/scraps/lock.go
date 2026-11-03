package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lighthousenz/scraps/internal/config"
	"github.com/lighthousenz/scraps/internal/lock"
)

func cmdLock(args []string) int {
	fs := flag.NewFlagSet("scraps lock", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	frozen := fs.Bool("frozen", false, "Fetch only what is already locked; never rewrite the lock")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "scraps lock: %s\n", err)
		return 3
	}

	projectFile, err := config.FindProjectFile(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scraps lock: %s\n", err)
		return 2
	}
	projectDir := filepath.Dir(projectFile)
	lockPath := filepath.Join(projectDir, "scraps.lock")

	// Load project config (we only need actors and checkers).
	lp, problems := config.LoadProject(nil)
	if lp == nil {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		return 2
	}

	ctx := context.Background()

	if *frozen {
		// Load existing lock file.
		lf, err := lock.Load(lockPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scraps lock --frozen: %s\n", err)
			return 2
		}

		if err := lock.Frozen(ctx, projectDir, lp.Project.Actors, lp.Project.Checkers, lf); err != nil {
			fmt.Fprintf(os.Stderr, "scraps lock --frozen: %s\n", err)
			return 2
		}

		fmt.Println("ok: lock verified")
		return 0
	}

	// Normal mode: resolve and write.
	lf, err := lock.Resolve(ctx, projectDir, lp.Project.Actors, lp.Project.Checkers, lp.LockFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scraps lock: %s\n", err)
		return 3
	}

	if err := lock.Write(lf, lockPath); err != nil {
		fmt.Fprintf(os.Stderr, "scraps lock: %s\n", err)
		return 3
	}

	fmt.Printf("ok: %d sources locked\n", len(lf.Sources))
	return 0
}
