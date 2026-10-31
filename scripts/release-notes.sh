#!/usr/bin/env bash
# Prints release notes for TAG: the body of its annotated tag message,
# followed by a link comparing it with the previous release tag.
#   scripts/release-notes.sh v0.3.0 nanmuyao-tools/ghostwire
set -euo pipefail
tag=$1
repo=$2

if [ "$(git cat-file -t "$tag")" != "tag" ]; then
  echo "error: $tag is not an annotated tag; create it with git tag -a" >&2
  exit 1
fi
body=$(git tag -l --format='%(contents:body)' "$tag" | sed -e '/-----BEGIN PGP SIGNATURE-----/,$d')
if [ -z "${body//[[:space:]]/}" ]; then
  body=$(git tag -l --format='%(contents:subject)' "$tag")
fi
printf '%s\n' "$body"

if prev=$(git describe --tags --abbrev=0 --match 'v*' "$tag^" 2>/dev/null); then
  printf '\n**Full Changelog**: https://github.com/%s/compare/%s...%s\n' "$repo" "$prev" "$tag"
fi
