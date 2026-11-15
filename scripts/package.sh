#!/bin/sh
# Run on Linux arm64 with Go 1.27.4 + GCC/libc. Build web assets first.
set -eu
cd "$(dirname "$0")/.."
version=${1:?Usage: package.sh VERSION /absolute/pinned-go2rtc [OUTPUT_DIR]}
backend=${2:?Provide the unmodified go2rtc v1.9.15 Linux arm64 executable}
out=${3:-dist}
case "$version" in ''|*[!a-zA-Z0-9._-]*|.*) echo 'Invalid artifact version' >&2; exit 1;; esac
[ "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" = linux/arm64 ] || { echo 'Use a Linux arm64 build host' >&2; exit 1; }
[ "$(go env GOVERSION)" = go1.27.4 ] || { echo 'Go 1.27.4 is required' >&2; exit 1; }
expected=af416f22b21a24ab83b8d78642190b4932ae695b4e386853f11d1b0319781763
[ "$(sha256sum "$backend" | cut -d ' ' -f 1)" = "$expected" ] || { echo 'Pinned go2rtc digest mismatch' >&2; exit 1; }
name="camstream-${version}-linux-arm64"
mkdir -p "$out"
[ ! -e "$out/$name" ] && [ ! -e "$out/$name.tar.gz" ] || { echo 'Artifact version already exists' >&2; exit 1; }
stage=$(mktemp -d "$out/.package.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
root="$stage/$name"
mkdir -p "$root/bin" "$root/deploy" "$root/licenses"
CGO_ENABLED=1 GOOS=linux GOARCH=arm64 GOWORK=off go build -trimpath -buildvcs=false -ldflags='-s -w' -o "$root/bin/camstream" ./cmd/camstream
cp "$backend" "$root/bin/go2rtc"
chmod 0755 "$root/bin/"*
cp -R deploy/. "$root/deploy/"
cp LICENSE "$root/licenses/camstream.LICENSE"
cp deploy/go2rtc.LICENSE "$root/licenses/go2rtc.LICENSE"
cp docs/GUIDE_INSTALLATION.md "$root/INSTALLATION.md"
cp data/web/manifest.json "$root/web-manifest.json"
printf 'version=%s\ngo=%s\ncgo=1\ntarget=linux/arm64\ngo2rtc=1.9.15\ngo2rtc_sha256=%s\n' "$version" "$(go env GOVERSION)" "$expected" > "$root/BUILDINFO"
ldd "$root/bin/camstream" > "$root/RUNTIME_LIBRARIES"
"$root/bin/camstream" config validate --config "$root/deploy/config.example.yaml"
(cd "$root"; find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
tar -C "$stage" -czf "$out/$name.tar.gz" "$name"
mv "$root" "$out/$name"
(cd "$out"; sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
printf 'Prepared %s\n' "$out/$name.tar.gz"
