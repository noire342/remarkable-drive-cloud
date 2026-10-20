#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
[ "$#" = 1 ] || { printf '%s\n' 'Usage: sh install.sh TABLET_IP' >&2; exit 1; }
case "$(uname -s)" in Darwin) system=darwin;; Linux) system=linux;; *) printf '%s\n' 'Unsupported computer OS.' >&2;exit 1;;esac
case "$(uname -m)" in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=amd64;; *) printf '%s\n' 'Unsupported computer architecture.' >&2;exit 1;;esac
build="$root/build"
helper="drive-cloud-setup-$system-$arch"
bundle=remarkable-drive-cloud-linux-armv7.tar.gz
[ -f "$build/SHA256SUMS" ] || { printf '%s\n' 'Build first: python3 scripts/build_development.py' >&2;exit 1; }
check() {
    expected=$(awk -v name="$1" '$2==name {print $1}' "$build/SHA256SUMS")
    [ "${#expected}" = 64 ] || return 1
    if command -v sha256sum >/dev/null 2>&1;then actual=$(sha256sum "$build/$1");else actual=$(shasum -a 256 "$build/$1");fi
    [ "${actual%% *}" = "$expected" ]
}
check "$helper" && check "$bundle" || { printf '%s\n' 'Development artifact checksum mismatch.' >&2;exit 1; }
sum=$(awk -v name="$bundle" '$2==name {print $1}' "$build/SHA256SUMS")
chmod 700 "$build/$helper"
exec "$build/$helper" install --host "$1" --bundle "$build/$bundle" --sha256 "$sum"
