#!/usr/bin/env bash
# Verifies the release artifacts in DIST for VERSION without launching anything.
#   scripts/verify-dist.sh dist 0.1.0
# A platform is verified when at least one of its artifacts exists; if some are
# missing it is a FAIL, if none exist it is skipped. The staged Windows executables
# are checked only while DIST/stage/win exists (release.sh runs this before removing it).
# Exits non-zero if any check fails, if both platforms are skipped (nothing verified)
# or if SHA256SUMS has no line for a file of this VERSION.
set -uo pipefail
cd "$(dirname "$0")/.."
DIST=${1:?dist dir}; VERSION=${2:?version}; NUMERIC=${VERSION%%-*}
fail=0
skipped=0
ok() { echo "ok   $*"; }
bad() { echo "FAIL $*"; fail=1; }
skip() { echo "skip $*"; }
check() { local what=$1; shift; if "$@" >/dev/null 2>&1; then ok "$what"; else bad "$what"; fi; }

dmg=$DIST/s3-anysync-$VERSION-macos-universal.dmg
zip=$DIST/s3-anysync-$VERSION-macos-universal.zip
exe=$DIST/s3-anysync-$VERSION-windows-amd64-installer.exe
tmp=$(mktemp -d)
mnt=
cleanup() {
  if [[ -n $mnt ]]; then hdiutil detach "$mnt" >/dev/null 2>&1 || hdiutil detach -force "$mnt" >/dev/null 2>&1; fi
  rm -rf "$tmp"
}
trap cleanup EXIT

# minos_ok BINARY: every slice has an LC_BUILD_VERSION with minos 13.0.
minos_ok() {
  local n slices
  slices=$(lipo -archs "$1" | wc -w | tr -d ' ')
  n=$(otool -l "$1" | grep -A4 LC_BUILD_VERSION | awk '$1 == "minos" && $2 == "13.0" {c++} END {print c+0}')
  [[ $slices -gt 0 && $n -eq $slices ]]
}

if [[ -f $zip || -f $dmg ]]; then
  if [[ -f $zip ]]; then
    if ditto -x -k "$zip" "$tmp/zip" 2>/dev/null && [[ -d $tmp/zip/s3-anysync.app ]]; then
      app=$tmp/zip/s3-anysync.app
      for b in s3-anysync s3-anysync-agent; do
        bin=$app/Contents/MacOS/$b
        if [[ -f $bin ]]; then
          archs=$(lipo -archs "$bin" 2>/dev/null)
          [[ $archs == *x86_64* && $archs == *arm64* ]] && ok "$b universal ($archs)" || bad "$b archs: $archs"
          minos_ok "$bin" && ok "$b minos 13.0 (all slices)" || bad "$b minos is not 13.0 on every slice"
        else
          bad "zip lacks Contents/MacOS/$b"
        fi
      done
      pl() { /usr/libexec/PlistBuddy -c "Print :$1" "$app/Contents/Info.plist" 2>/dev/null; }
      [[ $(pl CFBundleIdentifier) == com.niceit.s3-anysync ]] && ok "bundle id" || bad "bundle id $(pl CFBundleIdentifier)"
      [[ $(pl CFBundleShortVersionString) == "$NUMERIC" ]] && ok "version $NUMERIC" || bad "version $(pl CFBundleShortVersionString)"
      [[ $(pl LSMinimumSystemVersion) == 13.0 ]] && ok "minimum macOS 13.0" || bad "minimum macOS $(pl LSMinimumSystemVersion)"
      check "bundle signature (ad-hoc)" codesign --verify --deep --strict "$app"
    else
      bad "zip does not extract to s3-anysync.app"
    fi
  else
    bad "missing $zip"
  fi

  if [[ -f $dmg ]]; then
    mkdir -p "$tmp/mnt"
    if hdiutil attach -nobrowse -readonly -mountpoint "$tmp/mnt" "$dmg" >/dev/null 2>&1; then
      mnt=$tmp/mnt
      [[ -d $mnt/s3-anysync.app && -L $mnt/Applications ]] && ok "dmg contents (app and Applications link)" || bad "dmg contents"
      if hdiutil detach "$mnt" >/dev/null 2>&1; then mnt=; ok "dmg detached"; else bad "dmg detach"; fi
    else
      bad "dmg attach (read-only)"
    fi
  else
    bad "missing $dmg"
  fi
else
  skip "macOS (not built)"; skipped=$((skipped + 1))
fi

pecheck=$tmp/pecheck
if [[ -f $exe || -d $DIST/stage/win ]]; then
  if ! go build -o "$pecheck" ./tools/pecheck; then
    bad "building tools/pecheck"
  fi
  sub() { "$pecheck" "$1" 2>/dev/null; }
  if [[ -f $exe ]]; then
    [[ $(sub "$exe") == *": gui" ]] && ok "installer is a PE GUI program" || bad "installer PE check"
  else
    bad "missing $exe"
  fi
  if [[ -d $DIST/stage/win ]]; then
    w=$DIST/stage/win
    [[ $(sub "$w/s3-anysync-agent.exe") == *": gui" ]] && ok "agent.exe has no console" || bad "agent.exe subsystem"
    [[ $(sub "$w/s3-anysync-cli.exe") == *": console" ]] && ok "cli.exe is a console program" || bad "cli.exe subsystem"
    [[ $(sub "$w/s3-anysync.exe") == *": gui" ]] && ok "GUI .exe" || bad "GUI .exe subsystem"
  else
    skip "agent.exe subsystem (stage removed)"
    skip "cli.exe subsystem (stage removed)"
    skip "GUI .exe subsystem (stage removed)"
  fi
else
  skip "windows (not built)"; skipped=$((skipped + 1))
fi

if [[ -f $DIST/SHA256SUMS ]]; then
  check "SHA256SUMS" bash -c "cd '$DIST' && shasum -a 256 -c SHA256SUMS"
  grep -qF -- "-$VERSION-" "$DIST/SHA256SUMS" && ok "SHA256SUMS lists files of version $VERSION" || bad "SHA256SUMS has no line for version $VERSION"
else
  bad "missing $DIST/SHA256SUMS"
fi
[[ $skipped -lt 2 ]] || bad "nothing verified: no artifacts for version $VERSION in $DIST"
exit $fail
