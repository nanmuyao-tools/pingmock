# Contributing to pingmock

Thanks for your interest! Bug reports, ideas and pull requests are welcome. For anything larger than a small fix, please open an issue first so we can agree on the approach.

## Development

You need Linux, Go 1.22 or later, and iproute2. The end-to-end checks also need root, `ping` and `curl`.

```bash
make build   # build ./pingmock
make test    # go vet and unit tests with the race detector (no root needed)
make e2e     # build and run test/e2e.sh with sudo
make lint    # gofmt and go vet
```

CI runs `make lint`-equivalent checks, the unit tests and the end-to-end checks on every push and pull request.

## How the code is organized

```
main.go                  command-line flags; wires the packages together
internal/
  rule/                  rule types and parsing: Spec, Scenario, targets
  engine/                per-target state, decisions, statistics, history
  icmp/                  parsing echo requests and building replies
  responder/             reads packets from the device and answers them
  api/                   HTTP control API
  config/                JSON configuration files (-config)
  tun/                   TUN device, addresses and routes (Linux only)
examples/                example configuration
test/e2e.sh              end-to-end checks with the real ping command
scripts/release-notes.sh release notes from an annotated tag
```

A request flows through the packages like this:

```
tun device ──► responder ──► icmp.ParseEchoRequest
                  │
                  ├──► engine.RuleSet.Decide   (which rule, which phase, reply or drop, delay)
                  ├──► engine.History.Add
                  └──► icmp.BuildEchoReply ──► written to the device after the delay
```

Everything except `tun` and `main.go` can be tested without root. `responder` takes any `io.ReadWriter`, so its tests feed it packets directly, and `engine` accepts a fake clock through `engine.WithClock`.

Code under `internal/` is not part of the public API and may change at any time.

## Guidelines

- Keep pull requests focused on one change, and include tests for it. Bug fixes should come with a test that fails without the fix.
- pingmock uses only the Go standard library; please discuss before adding a dependency.
- Update both `README.md` and `README.zh-CN.md` when behavior visible to users changes. If you can only write one of them, say so in the pull request and the other will be updated.
- Seeded random sequences are part of the behavior users rely on: with the same seed and rules, a target must keep getting the same results across versions. Changes that alter them need a good reason and a note in the release.

## Releasing

Maintainers publish a release by pushing an annotated tag. Its message becomes the release notes: the first line is the title, the rest is the body.

```bash
git tag -a v0.3.0 -F notes.txt
git push origin v0.3.0
```

The release workflow runs the tests, builds static binaries for linux/amd64 and linux/arm64, and creates the GitHub release. Tags with a hyphen, such as `v0.3.0-rc.1`, become prereleases.
