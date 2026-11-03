package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("scraps run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var subjects multiFlag
	fs.Var(&subjects, "subject", "Set a subject version (name=version); repeatable")

	var only multiFlag
	fs.Var(&only, "only", "Select scenarios by id; repeatable")

	var tags multiFlag
	fs.Var(&tags, "tag", "Select scenarios by tag; repeatable")

	failOn := fs.String("fail-on", "", "Comma-separated verdicts that exit non-zero")
	_ = failOn

	var reports multiFlag
	fs.Var(&reports, "report", "Write a report (format=path); repeatable")

	seed := fs.Int64("seed", 0, "Reuse the seed from an earlier run")
	_ = seed

	parallel := fs.Int("parallel", 1, "Run up to n scenarios at once")
	_ = parallel

	keep := fs.Bool("keep", false, "Keep workdirs for passing scenarios too")
	_ = keep

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fmt.Fprintln(os.Stderr, "scraps run: not implemented")
	return 3
}
