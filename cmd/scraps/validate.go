package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/lighthousenz/scraps/internal/config"
)

func cmdValidate(args []string) int {
	fs := flag.NewFlagSet("scraps validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	paths := fs.Args()
	lp, loadProblems := config.LoadProject(paths)
	if lp == nil {
		for _, p := range loadProblems {
			fmt.Fprintln(os.Stderr, p)
		}
		return 2
	}

	validateProblems := config.Validate(lp)
	allProblems := append(loadProblems, validateProblems...)

	if len(allProblems) > 0 {
		for _, p := range allProblems {
			fmt.Fprintln(os.Stderr, p)
		}
		return 2
	}

	fmt.Printf("ok: %d scenarios\n", len(lp.Scenarios))
	return 0
}
