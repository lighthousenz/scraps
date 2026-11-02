package main

import (
	"flag"
	"fmt"
	"os"
)

func cmdSchema(args []string) int {
	fs := flag.NewFlagSet("scraps schema", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Fprintln(os.Stderr, "scraps schema: not implemented")
	return 3
}
