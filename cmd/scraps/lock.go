package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func cmdLock(args []string) int {
	fs := flag.NewFlagSet("scraps lock", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	frozen := fs.Bool("frozen", false, "Fetch only what is already locked; never rewrite the lock")
	_ = frozen
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fmt.Fprintln(os.Stderr, "scraps lock: not implemented")
	return 3
}
