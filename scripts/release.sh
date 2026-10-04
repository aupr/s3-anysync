#!/usr/bin/env bash
# Builds the release artifacts into dist/. Run from the repository root on macOS.
#   scripts/release.sh                 macOS and Windows
#   scripts/release.sh --mac-only
#   scripts/release.sh --windows-only
# Never launches the built apps.
# Cleaning is per platform: --mac-only removes only dist/*macos*, --windows-only
# only dist/*windows*, all removes all of dist/. SHA256SUMS and the stage dir are
# always rebuilt, and SHA256SUMS covers every dist/s3-anysync-* file present.
set -euo pipefail
cd "$(dirname "$0")/.."
source scripts/lib.sh

mode=${1:-all}
[[ $mode == all || $mode == --mac-only || $mode == --windows-only ]] || die "unknown option $mode"
[[ $(uname -s) == Darwin ]] || die "run this on macOS"

WAILS=${WAILS:-$HOME/go/bin/wails}
need go npm git shasum
case $mode in all | --mac-only) need lipo hdiutil ditto codesign /usr/libexec/PlistBuddy ;; esac
case $mode in all | --windows-only) need makensis ;; esac
[[ -x $WAILS ]] || die "wails CLI not found at $WAILS (go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0)"

ver=$(scripts/version.sh) || die "scripts/version.sh failed"
VERSION=${ver%%$'\n'*}
NUMERIC=${ver##*$'\n'}
[[ -n $VERSION && -n $NUMERIC ]] || die "empty version"
log "version $VERSION ($NUMERIC)"
DIST=$PWD/dist
STAGE=$DIST/stage
[[ -f go.mod && -d scripts && $DIST == "$PWD/dist" ]] || die "refusing to clean $DIST"
# KEEP_STAGE=1 keeps dist/stage (debugging and inspection of the staged executables).
trap 'rm -f cmd/agent/rsrc_windows_*.syso; [[ -n ${KEEP_STAGE:-} ]] || rm -rf "$STAGE"' EXIT
shopt -s nullglob
case $mode in
  all) rm -rf "$DIST" ;;
  --mac-only) rm -rf "$DIST"/*macos* "$DIST/SHA256SUMS" "$STAGE" ;;
  --windows-only) rm -rf "$DIST"/*windows* "$DIST/SHA256SUMS" "$STAGE" ;;
esac
mkdir -p "$STAGE"
LDVER="-X main.version=$VERSION"

log "frontend"
(cd frontend && npm ci && npm run build)

build_mac() {
  local flags="-mmacosx-version-min=13.0"
  export MACOSX_DEPLOYMENT_TARGET=13.0
  log "macOS GUI (universal)"
  (cd cmd/gui && CGO_CFLAGS="$flags" CGO_LDFLAGS="$flags" \
    "$WAILS" build -clean -platform darwin/universal -ldflags "$LDVER" -s)
  local app=$STAGE/mac/s3-anysync.app
  mkdir -p "$STAGE/mac"
  ditto cmd/gui/build/bin/s3-anysync.app "$app"

  log "macOS agent (arm64 + amd64)"
  CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 CGO_CFLAGS="$flags" CGO_LDFLAGS="$flags" \
    go build -trimpath -ldflags "$LDVER" -o "$STAGE/agent-arm64" ./cmd/agent
  CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 CC="clang -arch x86_64" CGO_CFLAGS="$flags" CGO_LDFLAGS="$flags" \
    go build -trimpath -ldflags "$LDVER" -o "$STAGE/agent-amd64" ./cmd/agent
  lipo -create -output "$app/Contents/MacOS/s3-anysync-agent" "$STAGE/agent-arm64" "$STAGE/agent-amd64"

  local plist=$app/Contents/Info.plist pb=/usr/libexec/PlistBuddy
  setkey() { "$pb" -c "Set :$1 $2" "$plist" 2>/dev/null || "$pb" -c "Add :$1 string $2" "$plist"; }
  setkey CFBundleShortVersionString "$NUMERIC"
  setkey CFBundleVersion "$NUMERIC"
  setkey CFBundleGetInfoString "s3-anysync $VERSION"
  # Ad-hoc signature over the whole bundle (Apple Silicon requires a signature;
  # this is not a Developer ID signature, Gatekeeper still asks on first launch).
  codesign --force --sign - "$app/Contents/MacOS/s3-anysync-agent"
  codesign --force --deep --sign - "$app"
  codesign --verify --deep --strict "$app" || die "ad-hoc signature does not verify"

  log "DMG and ZIP"
  mkdir -p "$STAGE/dmg"
  ditto "$app" "$STAGE/dmg/s3-anysync.app"
  ln -s /Applications "$STAGE/dmg/Applications"
  hdiutil create -volname "s3-anysync $VERSION" -srcfolder "$STAGE/dmg" -ov -format UDZO \
    "$DIST/s3-anysync-$VERSION-macos-universal.dmg"
  (cd "$STAGE/mac" && ditto -c -k --keepParent s3-anysync.app "$DIST/s3-anysync-$VERSION-macos-universal.zip")
}

build_windows() {
  local win=$STAGE/win v4
  v4=$(numeric4 "$NUMERIC")
  mkdir -p "$win"
  # The executables stay in $STAGE/win until the script exits (verify-dist reads them).
  log "Windows GUI"
  (cd cmd/gui && "$WAILS" build -clean -platform windows/amd64 -ldflags "$LDVER" -s)
  cp cmd/gui/build/bin/s3-anysync.exe "$win/"

  log "Windows agent and CLI"
  rm -f cmd/agent/*.syso
  go run github.com/tc-hib/go-winres@v0.3.3 simply \
    --arch amd64 --out cmd/agent/rsrc --manifest cli \
    --icon cmd/gui/build/windows/icon.ico \
    --product-name s3-anysync --product-version "$v4" --file-version "$v4" \
    --file-description "s3-anysync agent and command-line tool" --copyright niceit
  # rsrc_windows_amd64.syso is only linked into windows/amd64 builds; the EXIT trap
  # removes it even when a build fails.
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "$LDVER -X main.role=agent -H=windowsgui" -o "$win/s3-anysync-agent.exe" ./cmd/agent
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "$LDVER -X main.role=cli" -o "$win/s3-anysync-cli.exe" ./cmd/agent
  rm -f cmd/agent/*.syso

  log "Windows installer"
  # makensis aborts (std::bad_alloc) under an unset or C locale, so pin a UTF-8 one.
  LC_ALL=en_US.UTF-8 makensis -V2 -WX -DVERSION="$VERSION" -DVERSION4="$v4" -DSRCDIR="$win" \
    -DICON="$PWD/cmd/gui/build/windows/icon.ico" \
    -DOUTFILE="$DIST/s3-anysync-$VERSION-windows-amd64-installer.exe" \
    packaging/windows/s3-anysync.nsi
}

case $mode in
  all) build_mac; build_windows ;;
  --mac-only) build_mac ;;
  --windows-only) build_windows ;;
esac

log "checksums"
(cd "$DIST" && files=(s3-anysync-*) && [[ ${#files[@]} -gt 0 ]] && shasum -a 256 "${files[@]}" > SHA256SUMS) || die "no artifacts to checksum"
# Runs before the EXIT trap removes the stage dir so the Windows executables are checked too.
log "verify"
scripts/verify-dist.sh "$DIST" "$VERSION" || die "artifact verification failed (see FAIL lines above)"
log "done: $DIST"
