# CLAUDE.md

How to work in this repository. The documents named here are the source of truth; this file says where they are and what the maintainer expects.

## What this is

pingmock mocks network answers on Linux, per target, for testing software that decides things from them: ping (ICMP and ICMPv6 echo), with paths for traceroute and mtr, TCP connections (accepted, refused, unanswered, then closed or reset), and DNS failures under `pingmock exec`. [README.md](README.md) says what it does, [docs/architecture.md](docs/architecture.md) is the tour of the code, [docs/planning.md](docs/planning.md) is how the roadmap is worked out, and [TODO.md](TODO.md) (in Chinese) is the roadmap itself, with what was verified at each step. [CONTRIBUTING.md](CONTRIBUTING.md) has the design principles and the release procedure.

## Principles that decide what gets built

- **Four core values, for every protocol**: transparent, per target, reproducible, controllable and observable (defined in CONTRIBUTING). A feature that cannot keep all four is reconsidered before any code is written, or not built.
- **Test applications, not protocols.** A feature earns its place by helping people test their programs.
- **Evidence first.** Every direction traces to an issue, an experiment or a test result; docs/planning.md shows the method. Verify technical premises by experiment before designing. Syntax users will write (rules, configuration file, API) is agreed with the maintainer before it is implemented, since it is hard to change once released.
- **Order by number of users**, as the user table in TODO.md does.

## How the maintainer expects the work to be done

- **Verify before concluding.** Say what was run and what it showed. An experiment whose setup failed is not a result. Never report CI as passing without having seen it pass.
- **Dependencies are pinned to releases more than two weeks old**: Go modules, GitHub Actions, container images, tools in the dev container. pingmock itself depends only on the standard library and `go.yaml.in/yaml/v3`; `client/` uses the standard library alone. Discuss before adding anything.
- **Both languages change together**: `README.md` and `README.zh-CN.md`, `docs/*.md` and `docs/*.zh-CN.md`, every example's two READMEs.
- **Seeded sequences are behavior.** With the same seed and rules, a target keeps getting the same results across versions.
- **Tests come with the change**, and a bug fix comes with a test that fails without it. Mutation-check new tests when it is cheap: make them fail once on purpose.
- **Nothing of unknown origin is committed**: no scratch files, no drafts. Release notes live in the annotated tag, not in the repository.
- **TODO.md is kept current**: when a step is done, check it off and record what was observed, so the next round can trace it.
- **Commit messages** follow the history: `area: what the change does`, in the imperative and lower case after the area (`exec: rules for names, with a DNS server for the command`, `tun: never replace a route the host already has`, `TODO: ...`, `docs: ...`). The body says why.

## Running things

- Linux only. On a Mac, or wherever there is no Linux, use the development container: [.devcontainer/README.md](.devcontainer/README.md). On a Mac the packages `internal/app` and `internal/tun` do not even build; the other packages' unit tests run anywhere with `go test`.
- `make test` (unit tests, no root), `make e2e`, `make e2e-exec`, `make e2e-compat` (root, Linux), `make e2e-docker` (Docker), `make lint`. CI runs them all on Ubuntu 24.04 and 26.04, plus the examples and the dev container: [.github/workflows/ci.yml](.github/workflows/ci.yml).
- Releases are made by pushing an annotated tag; after one, the version named in the examples is updated (CONTRIBUTING → Releasing).
