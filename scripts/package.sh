#!/usr/bin/env bash
# Build the desktop app and wrap it into .rpm and .deb packages.
#
#   VERSION=1.2.3 ./scripts/package.sh        # -> dist/
#
# Requires: wails, nfpm, and the Linux webview toolchain (see README).
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)

# nfpm expands ${VERSION} but does not fail when it's unset — it quietly
# produces a package labelled 0.0.0~rc0. Shipping a mislabelled artifact is
# worse than not building, so refuse up front.
if [[ -z "${VERSION:-}" ]]; then
    echo "error: VERSION is not set (e.g. VERSION=1.2.3 $0)" >&2
    exit 1
fi
export VERSION

for tool in wails nfpm; do
    command -v "$tool" >/dev/null || {
        echo "error: $tool not found on PATH" >&2
        exit 1
    }
done

echo "==> building desktop app"
# wails runs `go build` in the directory holding wails.json, hence the cd.
(cd cmd/kscope-desktop && wails build)

BIN="$ROOT/cmd/kscope-desktop/build/bin/kscope-desktop"
[[ -x "$BIN" ]] || {
    echo "error: expected binary at $BIN" >&2
    exit 1
}

echo "==> packaging $VERSION"
mkdir -p dist
for pkg in rpm deb; do
    nfpm package -f packaging/nfpm.yaml -p "$pkg" -t dist/
done

ls -la dist/
