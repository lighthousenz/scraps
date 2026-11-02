package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func cmdExplain(args []string) int {
	fs := flag.NewFlagSet("scraps explain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var subjects multiFlag
	fs.Var(&subjects, "subject", "Set a subject version (name=version); repeatable")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fmt.Fprintln(os.Stderr, "scraps explain: not implemented")
	return 3
}
