package main

import (
	"flag"
	"fmt"
	"os"
)

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("scraps init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Fprintln(os.Stderr, "scraps init: not implemented")
	return 3
}
