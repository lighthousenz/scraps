package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

const starterScrapsYAML = `version: 1

subjects:
  myapp:
    scheme: semver
    resolve: { env: MYAPP_VERSION }

actors:
  myactor: ./actors/myactor

defaults:
  step-timeout: 10s
  ready-timeout: 30s
  fail-on: [regressed, changed, unrecognised, broken, fixed]
`

const starterScenarioYAML = `id: example
title: Example scenario

actors:
  myactor:
    uses: myactor

steps:
  - myactor: ping

outcomes:
  correct:
    - myactor:
        file-exists: output.txt

history: []
`

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("scraps init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	// Check if scraps.yaml already exists.
	if _, err := os.Stat("scraps.yaml"); err == nil {
		fmt.Fprintln(os.Stderr, "scraps.yaml already exists")
		return 2
	}

	// Write scraps.yaml.
	if err := os.WriteFile("scraps.yaml", []byte(starterScrapsYAML), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write scraps.yaml: %s\n", err)
		return 3
	}
	fmt.Println("wrote scraps.yaml")

	// Create scenarios directory and example.
	scenDir := filepath.Join("scenarios", "example")
	if err := os.MkdirAll(scenDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: create scenarios dir: %s\n", err)
		return 3
	}

	scenPath := filepath.Join(scenDir, "scenario.yaml")
	if err := os.WriteFile(scenPath, []byte(starterScenarioYAML), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: write scenario: %s\n", err)
		return 3
	}
	fmt.Printf("wrote %s\n", scenPath)

	fmt.Println("\nNext steps:")
	fmt.Println("  1. Create your actor in actors/myactor/")
	fmt.Println("  2. Edit scraps.yaml to match your project")
	fmt.Println("  3. Run: scraps validate")
	fmt.Println("  4. Run: scraps run --subject myapp=1.0.0")

	return 0
}
