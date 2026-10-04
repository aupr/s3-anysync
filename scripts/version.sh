#!/usr/bin/env bash
# Prints the release version (line 1) and its numeric form (line 2).
# A commit tagged vX.Y.Z gives X.Y.Z; anything else 0.0.0-dev+<sha>, with .dirty
# appended when the working tree has uncommitted changes (release.sh refuses a dirty release).
set -euo pipefail
tag=$(git describe --tags --exact-match 2>/dev/null || true)
if [[ $tag =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  full=${BASH_REMATCH[1]}
else
  full="0.0.0-dev+$(git rev-parse --short HEAD)"
  [[ -z $(git status --porcelain) ]] || full+=".dirty"
fi
echo "$full"
echo "${full%%-*}"
