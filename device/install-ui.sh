#!/bin/sh
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Lorenzo Garrone (noire342)
# Installs only our QML, or an isolated pinned XOVI runtime when none is installed.
set -eu
umask 077
base=/home/root/drive-cloud
global=/home/root/xovi

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
save_file() {
    if [ -e "$1" ]; then cp -p "$1" "$transaction/$2.new"; mv "$transaction/$2.new" "$transaction/$2"; else : >"$transaction/$2.absent"; fi
}
restore_file() {
    if [ -f "$transaction/$2.absent" ]; then
        rm -f "$1"
    elif [ -f "$transaction/$2" ]; then
        cp -p "$transaction/$2" "$1.drive-cloud-restore-$$"
        mv "$1.drive-cloud-restore-$$" "$1"
    fi
}
valid_transaction() {
    [ "$(dirname "$transaction")" = "$base/backups" ] && [ -d "$transaction" ] && [ ! -L "$transaction" ] || return 1
    name=${transaction##*/}
    case "$name" in install-[0-9]*-[0-9]*) ;; *) return 1 ;; esac
}
clear_validation() {
    if [ -f "$transaction/ui-validation.guard" ]; then
        guard=$(cat "$transaction/ui-validation.guard")
        case "$guard" in /run/systemd/system/xochitl.service.d/zzzz-remarkable-drive-cloud-validation-*.conf)
            [ "$(dirname "$guard")" = /run/systemd/system/xochitl.service.d ] && rm -f "$guard"
            ;;
        esac
    fi
    if [ -f "$base/ui-validation.pending" ] && [ "$(cat "$base/ui-validation.pending")" = "$transaction" ]; then
        rm -f "$base/ui-validation.pending" "$base/ui-validation.attempted"
    fi
    systemctl daemon-reload || true
    systemctl reset-failed xochitl || true
}

operation=${1:-}
if [ "$operation" = rollback ] || [ "$operation" = recover-ui-validation ] || [ "$operation" = finish-ui-validation ]; then
    if [ "$operation" = recover-ui-validation ]; then
        [ -f "$base/ui-validation.pending" ] || exit 0
        transaction=$(cat "$base/ui-validation.pending")
    else
        transaction=$2
    fi
    valid_transaction || die 'Invalid UI validation transaction.'
    if [ "$operation" = finish-ui-validation ]; then clear_validation; rm -f "$base/ui-validation.failed"; exit 0; fi
    if [ -f "$transaction/ui-qrr.path" ]; then
        qrr=$(cat "$transaction/ui-qrr.path")
        case "$qrr" in "$global/exthome/qt-resource-rebuilder"|/home/root/quickshare/runtime/xovi/exthome/qt-resource-rebuilder|"$base/runtime/xovi/exthome/qt-resource-rebuilder") ;; *) die 'Invalid UI rollback path.' ;; esac
        restore_file "$qrr/drive-cloud.qmd" ui-qmd
        if [ -f "$transaction/ui-hashtab-created" ]; then rm -f "$qrr/hashtab"; fi
    fi
    if [ -d "$transaction/ui.before" ]; then
        rm -rf "$base/ui"
        cp -Rp "$transaction/ui.before" "$base/ui"
    elif [ -f "$transaction/ui.absent" ]; then
        rm -rf "$base/ui"
    fi
    restore_file "$base/ui-runtime.conf" ui-runtime.conf
    if [ -f "$transaction/ui-runtime-created" ]; then rm -rf "$base/runtime/xovi"; fi
    clear_validation
    if [ "$operation" = recover-ui-validation ]; then
        # Recovery runs from the Xochitl launcher; never try to start Xochitl
        # recursively here. Its caller will continue with the restored UI.
        printf '%s\n' "$transaction" >"$base/ui-validation.failed"
        systemctl disable --now remarkable-drive-cloud.service >/dev/null 2>&1 || true
        logger -t drive-cloud 'Interrupted UI validation recovered; candidate removed and receiver disabled.' || true
    fi
    exit 0
fi

payload=$1
transaction=$2
[ -f "$payload/tablet-ui/drive-cloud.qmd" ] && [ -f "$payload/tablet-ui/CloudPage.qml" ] || die 'The confirmation UI is missing from the release.'
[ ! -L "$base/ui" ] && [ ! -L "$base/runtime" ] || die 'Refusing a symlink UI/runtime directory.'
if [ -d "$base/ui" ]; then
    cp -Rp "$base/ui" "$transaction/ui.before.new"
    mv "$transaction/ui.before.new" "$transaction/ui.before"
else : >"$transaction/ui.absent"; fi
save_file "$base/ui-runtime.conf" ui-runtime.conf

kind=owned
runtime="$base/runtime/xovi"
# Reuse the existing Quick Share-owned runtime too, without taking ownership.
if [ ! -f "$global/xovi.so" ] && [ -f /home/root/quickshare/runtime/xovi/xovi.so ]; then global=/home/root/quickshare/runtime/xovi; fi
if [ -f "$global/xovi.so" ]; then
    [ -f "$global/extensions.d/qt-resource-rebuilder.so" ] || die 'Existing XOVI lacks qt-resource-rebuilder; no existing extension was changed.'
    kind=existing
    runtime=$global
elif [ -e "$runtime" ]; then
    [ -f "$runtime/xovi.so" ] && [ -f "$runtime/extensions.d/qt-resource-rebuilder.so" ] || die 'The owned XOVI runtime is incomplete.'
else
    archive="$payload/dependencies/xovi-arm32.tar.gz"
    expected=$(sed -n 's/^XOVI_SHA256=//p' "$payload/device/dependencies.env")
    [ "${#expected}" = 64 ] || die 'Missing pinned XOVI checksum.'
    if [ ! -f "$archive" ]; then
        url=$(sed -n 's/^XOVI_URL=//p' "$payload/device/dependencies.env")
        case "$url" in https://github.com/asivery/rm-xovi-extensions/releases/download/*/xovi-arm32.tar.gz) ;; *) die 'Invalid XOVI source.';; esac
        mkdir -p "$payload/dependencies"
        if command -v curl >/dev/null 2>&1; then curl --fail --location --proto '=https' --proto-redir '=https' "$url" -o "$archive.new"
        else wget -O "$archive.new" "$url"; fi
        actual=$(sha256sum "$archive.new"); actual=${actual%% *}
        [ "$actual" = "$expected" ] || die 'XOVI download checksum mismatch.'
        mv "$archive.new" "$archive"
    fi
    actual=$(sha256sum "$archive"); actual=${actual%% *}
    [ "$actual" = "$expected" ] || die 'XOVI dependency checksum mismatch.'
    mkdir -p "$transaction/xovi-stage"
    # Extract an explicit allowlist. Other bundled extensions are never activated.
    tar -xzf "$archive" -C "$transaction/xovi-stage" xovi/xovi.so xovi/extensions.d/qt-resource-rebuilder.so
    [ -f "$transaction/xovi-stage/xovi/xovi.so" ] && [ -f "$transaction/xovi-stage/xovi/extensions.d/qt-resource-rebuilder.so" ] || die 'The XOVI archive is incomplete.'
    mkdir -p "$base/runtime"
    mv "$transaction/xovi-stage/xovi" "$runtime"
    : >"$transaction/ui-runtime-created"
fi

[ ! -L "$runtime" ] || die 'Refusing a symlink runtime root.'
qrr="$runtime/exthome/qt-resource-rebuilder"
[ ! -L "$qrr" ] || die 'Refusing a symlink QML modification directory.'
mkdir -p "$qrr"
printf '%s\n' "$qrr" >"$transaction/ui-qrr.path"
if [ -f "$qrr/drive-cloud.qmd" ] && ! grep -q '/home/root/drive-cloud/' "$qrr/drive-cloud.qmd"; then die 'An unrelated drive-cloud.qmd already exists.'; fi
save_file "$qrr/drive-cloud.qmd" ui-qmd

if [ ! -s "$qrr/hashtab" ]; then
    : >"$transaction/ui-hashtab-created"
    printf '%s\n' 'Preparing the firmware UI table (usually about one minute). Keep the tablet unlocked.'
    systemctl stop xochitl
    isolated="$transaction/hashtab-root"
    mkdir -p "$isolated/extensions.d" "$isolated/exthome/qt-resource-rebuilder"
    ln -s "$runtime/extensions.d/qt-resource-rebuilder.so" "$isolated/extensions.d/qt-resource-rebuilder.so"
    LD_PRELOAD="$runtime/xovi.so" XOVI_ROOT="$isolated" QMLDIFF_HASHTAB_CREATE="$qrr/hashtab" QML_DISABLE_DISK_CACHE=1 /usr/bin/xochitl >"$transaction/hashtab.log" 2>&1 &
    gui_pid=$!
    stop_gui() {
        kill -15 "$gui_pid" 2>/dev/null || true
        remaining=10
        while kill -0 "$gui_pid" 2>/dev/null && [ "$remaining" -gt 0 ]; do sleep 1; remaining=$((remaining-1)); done
        kill -9 "$gui_pid" 2>/dev/null || true
        wait "$gui_pid" 2>/dev/null || true
    }
    trap 'stop_gui; exit 1' HUP INT TERM
    ready=0
    attempt=0
    while [ "$attempt" -lt 150 ]; do
        if [ -s "$qrr/hashtab" ] && grep -Fq "Hashtab saved to $qrr/hashtab" "$transaction/hashtab.log"; then ready=1; break; fi
        kill -0 "$gui_pid" 2>/dev/null || break
        attempt=$((attempt+1))
        sleep 1
    done
    stop_gui
    trap - HUP INT TERM
    if [ -s "$qrr/hashtab" ] && grep -Fq "Hashtab saved to $qrr/hashtab" "$transaction/hashtab.log"; then ready=1; fi
    [ "$ready" = 1 ] || die "The firmware UI table was not generated; see $transaction/hashtab.log."
fi

mkdir -p "$base/ui"
cp -R "$payload/tablet-ui/." "$base/ui/"
token=$(cat "$base/control-token")
case "$token" in ''|*[!a-zA-Z0-9_-]*) die 'Invalid control token.' ;; esac
"$base/bin/drive-cloud" prepare-ui --base "$base"
chmod 600 "$base/ui/Config.js"
cp "$payload/tablet-ui/drive-cloud.qmd" "$qrr/drive-cloud.qmd"
printf 'kind=%s\nroot=%s\n' "$kind" "$runtime" >"$base/ui-runtime.conf"
chmod 600 "$base/ui-runtime.conf"
