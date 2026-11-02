package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func cmdDiagram(args []string) int {
	fs := flag.NewFlagSet("scraps diagram", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fmt.Fprintln(os.Stderr, "scraps diagram: not implemented")
	return 3
}
