#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
set -eu
base=/home/root/drive-cloud
disable_ui() {
    for runtime in /home/root/xovi /home/root/quickshare/runtime/xovi "$base/runtime/xovi"; do
        qmd="$runtime/exthome/qt-resource-rebuilder/drive-cloud.qmd"
        if [ -f "$qmd" ] && grep -q '/home/root/drive-cloud/' "$qmd"; then rm -f "$qmd"; fi
    done
}
if [ -f "$base/ui-validation.pending" ]; then
    transaction=$(cat "$base/ui-validation.pending")
    boot=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || true)
    prior=$(cat "$transaction/ui-validation.boot-id" 2>/dev/null || true)
    if [ -f "$base/ui-validation.attempted" ] || [ -z "$boot" ] || [ "$boot" != "$prior" ]; then
        sh "$base/device/install-ui.sh" recover-ui-validation || disable_ui
    else
        printf '%s\n' "$boot" >"$base/ui-validation.attempted"
    fi
fi
if ! sh "$base/device/check-firmware.sh"; then
    disable_ui
    exec /usr/bin/xochitl "$@"
fi
if [ -f "$base/ui-runtime.conf" ]; then
    runtime=$(sed -n 's/^root=//p' "$base/ui-runtime.conf")
    case "$runtime" in /home/root/xovi|/home/root/quickshare/runtime/xovi|"$base/runtime/xovi")
        if [ -f "$runtime/xovi.so" ]; then
            case "${LD_PRELOAD:-}" in *"$runtime/xovi.so"*) ;; *) LD_PRELOAD="$runtime/xovi.so${LD_PRELOAD:+:$LD_PRELOAD}" ;; esac
            [ -n "${XOVI_ROOT:-}" ] || XOVI_ROOT=$runtime
            export LD_PRELOAD XOVI_ROOT
            export QML_DISABLE_DISK_CACHE=1
        fi
        ;;
    esac
fi
# Keep Quick Share's own recovery and import preparation intact.
if [ -x /home/root/quickshare/launch-xochitl.sh ]; then exec /home/root/quickshare/launch-xochitl.sh "$@"; fi
expected=72deb4483772142ab12d08d12f1d25dbd4e7b1f449a9ead63df103f0ac612fe4
actual=$(sha256sum "$base/bin/xochitl-local-import" 2>/dev/null || true);actual=${actual%% *}
if [ "$actual" != "$expected" ]; then disable_ui;exec /usr/bin/xochitl "$@";fi
conf=/home/root/.config/remarkable/xochitl.conf
if grep -q '^WebInterfaceEnabled=' "$conf"; then sed -i 's/^WebInterfaceEnabled=.*/WebInterfaceEnabled=true/' "$conf";else sed -i '/^\[General\]$/a\WebInterfaceEnabled=true' "$conf";fi
if ip link show usb0 >/dev/null 2>&1; then
    ip addr show usb0 | grep -q '10.11.99.1/' || ip addr add 10.11.99.1/32 dev usb0 || true
    ip link set usb0 up || true
fi
exec "$base/bin/xochitl-local-import" "$@"
