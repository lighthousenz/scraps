package main

import (
	"fmt"
	"os"
)

var version = "dev"

type subcommand struct {
	name  string
	brief string
	run   func(args []string) int
}

var subcommands = []subcommand{
	{"init", "Write a starter scraps.yaml and an example scenario", cmdInit},
	{"lock", "Resolve Git actor and checker references and write scraps.lock", cmdLock},
	{"schema", "Generate .scraps/schema.json", cmdSchema},
	{"validate", "Full static validation", cmdValidate},
	{"run", "Run scenarios and print verdicts", cmdRun},
	{"diagram", "Print a scenario as a Mermaid sequence diagram", cmdDiagram},
	{"explain", "Show history and the last recorded verdict", cmdExplain},
}

func usage() {
	fmt.Fprintf(os.Stderr, "Usage: scraps <command> [flags]\n\nCommands:\n")
	for _, c := range subcommands {
		fmt.Fprintf(os.Stderr, "  %-12s %s\n", c.name, c.brief)
	}
	fmt.Fprintf(os.Stderr, "\nFlags:\n  --version    Print version and exit\n  --help       Show this help\n")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	arg := os.Args[1]

	if arg == "--version" || arg == "-version" {
		fmt.Println(version)
		return
	}
	if arg == "--help" || arg == "-help" || arg == "-h" {
		usage()
		return
	}

	for _, c := range subcommands {
		if c.name == arg {
			os.Exit(c.run(os.Args[2:]))
		}
	}

	fmt.Fprintf(os.Stderr, "scraps: unknown command %q\n", arg)
	usage()
	os.Exit(2)
}
