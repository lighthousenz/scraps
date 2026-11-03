package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lighthousenz/scraps/internal/config"
	"github.com/lighthousenz/scraps/internal/schema"
)

func cmdSchema(args []string) int {
	fs := flag.NewFlagSet("scraps schema", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	lp, problems := config.LoadProject(nil)
	if lp == nil {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		return 2
	}

	data, err := schema.Generate(lp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scraps schema: %s\n", err)
		return 3
	}

	outDir := filepath.Join(lp.ProjectDir, ".scraps")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "scraps schema: %s\n", err)
		return 3
	}

	outPath := filepath.Join(outDir, "schema.json")
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "scraps schema: %s\n", err)
		return 3
	}

	fmt.Printf("ok: wrote %s\n", outPath)
	return 0
}
