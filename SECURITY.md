# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub:
[Report a vulnerability](https://github.com/lighthousenz/scraps/security/advisories/new).
Do not open a public issue, pull request or discussion for a security problem.

Include what you found, how to reproduce it, and the version (`scraps --version`) or
commit you tested. We aim to acknowledge a report within five working days and will
keep you updated until it is resolved.

## Supported versions

Fixes land on `main` and ship in the next release. Only the latest release is
supported.

## Scope

Scraps runs the actors a scenario names, and actors are arbitrary programs. Running a
scenario from a source you do not trust is equivalent to running that source's code;
that is by design and is not a vulnerability in Scraps. Reports about Scraps itself
are in scope, for example `scraps.lock` hash verification being bypassable, or path
handling that escapes the project directory.
