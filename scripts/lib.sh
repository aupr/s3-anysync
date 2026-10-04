#!/usr/bin/env bash
# Helpers shared by the release scripts.
log() { printf '\033[1m==> %s\033[0m\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
need() {
  local t
  for t in "$@"; do command -v "$t" >/dev/null 2>&1 || die "missing tool: $t"; done
}
# numeric4 1.2.3 -> 1.2.3.0 (Windows resources and NSIS need four parts)
numeric4() { echo "$1.0"; }
