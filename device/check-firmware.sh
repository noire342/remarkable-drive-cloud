#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
set -eu
expected=87b574fc7ac1a15d502e96458a94a5279a189023bd476758e4aaca9bae13575a
actual=$(sha256sum /usr/bin/xochitl); actual=${actual%% *}
[ "$(uname -m)" = armv7l ] && [ "$actual" = "$expected" ] || {
    printf '%s\n' 'Unsupported firmware: cloud service stays offline; document library unchanged.' >&2
    exit 1
}
