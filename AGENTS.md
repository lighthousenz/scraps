# AGENTS.md

Guidance for coding agents working in this repository.

Scraps is a conductor for deterministic, multi-actor regression scenarios. It reads
YAML scenarios, starts actors (external processes that speak a JSON-RPC protocol over
stdio), runs a step timeline against them, and classifies each run against a
version-conditioned history. `README.md` is the user-facing spec. Single Go module,
single binary.

## Tooling

mise is the only orchestrator in this repo. There is no Makefile, no npm scripts, and
no shell script that isn't a mise task. If you need a new command, add a mise task.

```bash
mise trust             # once per clone
mise bootstrap         # installs the pinned toolchain; safe to repeat
mise doctor project    # checks this machine is ready; inspects only, changes nothing
```

`mise bootstrap` is mise's built-in setup command, not a task. It is configured under
`[tools]` and `[bootstrap]` in `mise.toml`. Its counterpart, `mise doctor project`,
runs the `[doctor.checks.*]` in `mise.toml`. When you add a prerequisite, add both:
the bootstrap entry that provides it, and the doctor check that verifies it.

`mise bootstrap` also installs lefthook git hooks. They run the cheap checks before
each commit: `cog verify` on the commit message, `golangci-lint fmt --diff` on staged
`.go` files, `rumdl` on staged `.md` files, `yamlfmt -lint` on staged `.yml`/`.yaml`
files (excluding `testdata/`), and `mise fmt --check` on `mise.toml`. Set `LEFTHOOK=0`
to skip hooks in an emergency.

## Build and test

These tasks are the build contract.

```bash
mise run build             # go build ./... into dist/
mise run test              # go test ./... (race detector on)
mise run test -- -run X ./internal/verdict/...   # one package or test while iterating
mise run lint              # golangci-lint
mise run lint:md           # markdown lint (rumdl)
mise run fmt               # format Go, Markdown, YAML and mise.toml; writes files
mise run fmt:check         # check formatting without writing (Go, Markdown, YAML, mise.toml)
mise run check             # the gate: fmt:check, lint:md, lint, build, test
```

Workers are ephemeral. Never assume a warm module or build cache from a previous run.
Run the narrowest test that covers your change while iterating, then `mise run check`
once before opening a PR.

## Definition of done

A PR is ready when `mise run check` passes from a clean checkout.

If it cannot pass for a reason outside your task, say so in the PR description rather
than weakening the check. Never skip, `t.Skip`, or delete a test to get green. A
failing test is a finding, not an obstacle.

A change to anything a user can see (scenario syntax, `scraps.yaml`, `actor.yaml`, the
actor protocol, CLI flags, exit codes, report formats) updates `README.md` in the same
PR. The README is the spec; code that disagrees with it is a bug in one of them, and
the PR has to say which.

## Layout

Put new code where this says, so parallel work does not collide.

```text
cmd/scraps/        CLI entrypoint; one file per subcommand (run.go, validate.go, …)
internal/
  model/           shared types: project, scenario, actor definition, results
  config/          scraps.yaml, scenario.yaml and actor.yaml: parsing and static validation
  lock/            scraps.lock: reading, resolving GitHub sources, the source cache
  schema/          generated JSON Schema (`scraps schema`)
  expr/            ${{ }} reference resolution
  graph/           actor dependency graph: start order, stop order, cycle detection
  protocol/        JSON-RPC 2.0 over stdio: framing, types, error codes
  actor/           process supervision: spawn, process groups, mise exec, timeouts
  conductor/       one scenario run: prepare, start, steps, stop, collect
  verdict/         outcome matching, history conditions, verdict classification
  checks/          built-in checks: file-exists, json, text
  report/          JSON, JUnit, Markdown, Mermaid
docs/              normative protocol spec, when it outgrows the README
examples/          a runnable example project, including actors in other languages
testdata/          fixtures for Go tests; synthetic only
```

- One concern per package. `conductor` orchestrates and owns no parsing, matching or
  formatting.
- Shared types live in `internal/model`. Add to them; do not create parallel type files
  in other packages.
- A new subcommand gets its own file in `cmd/scraps/`. Do not grow `main.go` beyond
  dispatch.
- Supported platforms are linux and macOS. Process-group and signal code may be unix-only.
- Tests sit beside the code as `_test.go`, per Go convention.
- Only `actor` starts processes. Only `protocol` reads or writes the wire format.

## Conventions

- **Commits:** conventional, lowercase subject: `feat(verdict): add changed verdict`.
  One logical change per commit. Checked by cocogitto (`cog`); the `retro` type is
  allowed. PRs land as merge commits, so every commit in a PR must be conventional;
  merge commits and PR titles are not checked.
- **Branches:** if your branch was cut for you, do not rename or re-target it.
- **Comments:** explain why, not what. One line where one line does. The code should
  carry the rest.
- **Errors:** wrap with `%w` and context; never discard one. Every blocking call takes a
  `context.Context`.
- **Dependencies:** prefer the standard library. A new module dependency needs a
  one-line justification in the PR description.

## Constraints

- **Determinism is the product.** No `time.Sleep` to wait for state, in code or in
  tests. Poll a condition with a deadline, and inject the clock where time matters.
- **A harness failure is never an outcome.** Anything that goes wrong in Scraps or an
  actor (spawn failure, ready timeout, protocol violation, error code `1001`) produces
  the `broken` verdict. It must never be matched against a scenario's outcomes.
- **Protocol changes are versioned.** A breaking change to the actor protocol bumps the
  protocol version and keeps the previous version working.
- **No JS toolchain at the repo root.** Example actors under `examples/` may use Node
  or Python, provisioned through their own `actor.yaml` `tools`, never through root
  `mise.toml` tools.
- Never edit `go.sum` by hand: change `go.mod` and run `go mod tidy`.
- Never commit anything under `testdata/` or `examples/` that contains real
  credentials, customer data, or production hostnames.
- Never reformat files your task does not otherwise touch; it buries the diff.
- Never change `mise.toml`, `.golangci.yml`, or CI workflow files unless that is
  explicitly your task. These are shared surface; a change here breaks every other task
  in flight.

## Working in parallel

Several agents may be working on this repo at the same time, each on its own task, each
opening its own PR against the same base branch.

- Stay inside the files your task names. If you believe you need to change shared
  config, stop and escalate instead. Do not take the change unilaterally.
- If something you need does not exist yet and another task is plainly creating it, do
  not build a second copy. Escalate and say what you are waiting on.
- Assume your PR will be trial-merged against siblings you cannot see. Keep the change
  narrow and self-contained so that merge is boring.

## Amendments

This file is the build contract for every agent that works here. When a run surfaces a
durable lesson, such as a build step that needed warming or a trap that cost a round,
the retrospective promotes it into this file. Keep additions terse and specific; this
is a contract, not a changelog.
