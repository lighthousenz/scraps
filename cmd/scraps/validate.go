package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
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
	fmt.Fprintln(os.Stderr, "scraps validate: not implemented")
	return 3
}
