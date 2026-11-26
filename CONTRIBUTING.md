# Contributing

Thanks for your interest in Scraps. `README.md` is the specification; read it first.

## Before you start

- For anything bigger than a small fix, open an issue first so we can agree on the
  approach before you spend time on it.
- Security problems go through [SECURITY.md](SECURITY.md), not public issues.

## Development

The toolchain is managed by [mise](https://mise.jdx.dev):

```bash
mise trust
mise bootstrap         # installs pinned tools and git hooks
mise run check         # the gate CI runs: format, lint, build, test
```

`AGENTS.md` holds the build contract, package layout and coding conventions. They
apply to human contributors too. Its "Working in parallel" section is for maintainers'
automation and you can ignore it.

## Pull requests

- Commits follow [Conventional Commits](https://www.conventionalcommits.org) with a
  lowercase subject, for example `fix(verdict): treat timeout as broken`. Every commit
  in the PR is checked, and PRs land as merge commits.
- `mise run check` passes.
- A change to anything a user can see (scenario syntax, config files, the actor
  protocol, CLI flags, exit codes, report formats) updates `README.md` in the same PR.
- Keep the change narrow. Do not reformat files your change does not otherwise touch.

## License

By contributing you agree that your contributions are licensed under the
[MIT License](LICENSE).
