# Contributing to pingmock

Thanks for your interest! Bug reports, ideas and pull requests are welcome. For anything larger than a small fix, please open an issue first so we can agree on the approach.

## Development

You need Linux, Go 1.22 or later, and iproute2. The end-to-end checks also need root, `ping` and `curl`.

```bash
make build        # build ./pingmock
make test         # go vet and unit tests with the race detector (no root needed)
make e2e          # build and run test/e2e.sh with sudo
make e2e-exec     # pingmock exec, rootless and as root
make e2e-compat   # fping, pro-bing and icmplib against pingmock (needs fping and icmplib)
make e2e-docker   # the container image and the Compose example (needs Docker)
make lint         # gofmt, go vet and shellcheck
```

CI runs `make lint`-equivalent checks, the unit tests and the end-to-end checks on every push and pull request.

## How the code is organized

[docs/packet-path.md](docs/packet-path.md) ([中文](docs/packet-path.zh-CN.md)) follows one ping through the kernel to pingmock and back, [docs/packets.md](docs/packets.md) ([中文](docs/packets.zh-CN.md)) takes the packets apart byte by byte, and [docs/architecture.md](docs/architecture.md) ([中文](docs/architecture.zh-CN.md)) is a tour of the code: the packages, the journey of one ping through them, the core types, and where to make common changes. In short:

```
main.go                  entry point; calls internal/app
internal/
  app/                   the command: flags and configuration, starting up, pingmock exec
  tun/                   TUN device, addresses and routes (Linux only)
  responder/             reads packets from the device and answers them
  icmp/                  parsing echo requests and building replies (IPv4 and IPv6)
  engine/                per-target state, decisions, statistics, history
  rule/                  rule types and their text and JSON forms
  api/                   HTTP control API
  config/                YAML configuration files (-config)
client/                  public Go client for the control API
contrib/apparmor/        AppArmor profile for pingmock exec on Ubuntu 23.10+
docs/                    the code tour, the packet path and the packet anatomy
examples/                example configuration, Compose file, and complete examples in Go, Python and shell
test/                    end-to-end checks: e2e.sh, e2e-exec.sh, e2e-compat.sh, e2e-docker.sh;
                         test/compat holds the libraries e2e-compat.sh uses, as its own module
scripts/release-notes.sh release notes from an annotated tag
Dockerfile               the container image
```

Everything except `tun`, `app/instance.go` and `app/exec_linux.go` can be tested without root. `responder` takes any `io.ReadWriter`, so its tests feed it packets directly, and `engine` accepts a fake clock through `engine.WithClock`.

Code under `internal/` is not part of the public API and may change at any time. `client/` is public: keep it compatible, and test it against the real API handler as its tests do. `examples/go-demo` uses it the way users do; CI runs its tests, and those of the other examples, against the current code, so update the examples along with what they use.

## Guidelines

- Keep pull requests focused on one change, and include tests for it. Bug fixes should come with a test that fails without the fix.
- pingmock depends only on the Go standard library and `go.yaml.in/yaml/v3` (for configuration files, in `internal/config`); please discuss before adding a dependency. The `client` package uses the standard library alone, so programs that import it pull in nothing else.
- Update both `README.md` and `README.zh-CN.md` when behavior visible to users changes. If you can only write one of them, say so in the pull request and the other will be updated.
- Seeded random sequences are part of the behavior users rely on: with the same seed and rules, a target must keep getting the same results across versions. Changes that alter them need a good reason and a note in the release.

## Releasing

Maintainers publish a release by pushing an annotated tag. The release is titled with the tag name, and the tag message, after its first line, becomes the release notes. The first line is the tag's own summary and is not published. `--cleanup=whitespace` keeps Markdown headings: without it, git drops every line that starts with `#` as a comment.

```bash
git tag -a v0.3.0 --cleanup=whitespace -F notes.txt
git push origin v0.3.0
```

The release workflow runs the tests, builds static binaries for linux/amd64 and linux/arm64, creates the GitHub release, and pushes the container image to `ghcr.io/nanmuyao/pingmock`.

After a release, update the examples that name a version, so users who copy them get the new one: `PINGMOCK_VERSION` in `examples/github-actions/pingmock-tests.yml`, and the `require` line in `examples/go-demo/go.mod` (then `go mod tidy` there). CI's `workflow-example` job runs the workflow example against that release. Tags with a hyphen, such as `v0.3.0-rc.1`, become prereleases.
