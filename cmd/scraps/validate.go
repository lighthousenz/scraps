package main

import (
	"flag"
	"fmt"
	"os"
)

func cmdValidate(args []string) int {
	fs := flag.NewFlagSet("scraps validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Fprintln(os.Stderr, "scraps validate: not implemented")
	return 3
}
