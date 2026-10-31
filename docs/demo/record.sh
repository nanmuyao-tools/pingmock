#!/bin/bash
# Records the demo at the top of the README from real command output.
#
#   docs/demo/record.sh            # writes docs/demo/demo.cast and docs/demo/demo.svg
#
# Needs: asciinema (pip install asciinema), svg-term (npm install -g svg-term-cli),
# ping and ip, and whatever ghostwire exec needs (see the README). Run it on a
# machine with IPv6, so ghostwire does not log that it is unavailable.
set -euo pipefail

demo=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$demo/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Build what the demo runs. ghostwire from the working tree, fastest from the Go
# example; both are found through PATH, fastest as ./fastest. VERSION, if set,
# is the version ghostwire reports, as in the release workflow: record a
# release's demo with VERSION=v0.5.1 from a checkout of that tag.
(cd "$repo" && go build -ldflags "-X main.version=${VERSION:-}" -o "$work/ghostwire" .)
(cd "$repo/examples/go-demo" && go build -o "$work/fastest" ./cmd/fastest)

cd "$work"
PATH=$work:$PATH asciinema rec --overwrite --cols 100 --rows 32 -q \
  -c "bash '$demo/outer.sh'" "$demo/demo.cast"
svg-term --in "$demo/demo.cast" --out "$demo/demo.svg" --window --width 100 --height 32
echo "wrote $demo/demo.cast and $demo/demo.svg"
