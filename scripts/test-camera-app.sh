#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
check_dir=$(mktemp -d)
trap 'rm -rf "$check_dir"' EXIT

app_sources=()
for source in "$root"/macos/CameraApp/*.swift; do
    if [[ ${source##*/} != main.swift ]]; then
        app_sources+=("$source")
    fi
done
xcrun swiftc -swift-version 5 -module-cache-path "$check_dir/modules" \
    -import-objc-header "$root/macos/CameraApp/HostPowerMessages.h" \
    "${app_sources[@]}" "$root"/macos/CameraAppTests/*.swift -o "$check_dir/camera-app-tests"
"$check_dir/camera-app-tests" "$root/macos/CameraAppTests/fake-helper.py"
