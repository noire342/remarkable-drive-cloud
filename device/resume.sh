#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Resume only after settle-switch has completed and verified the pending archive.
set -eu
umask 077
base=/home/root/drive-cloud
tool=$base/bin/drive-cloud
guard=/run/systemd/system/xochitl.service.d/zzzz-drive-cloud-switch.conf
watch=drive-cloud-migration-guard-$(date +%s)-$$
observer=
prepared=false
complete=false
[ ! -e "$guard" ] || { printf '%s\n' 'An earlier interface guard needs recovery.' >&2; exit 1; }
restore_ui() {
    code=$?
    trap - EXIT HUP INT TERM
    if [ -n "$observer" ]; then kill "$observer" 2>/dev/null || true; fi
    if [ "$complete" != true ]; then
        if [ "$prepared" = true ]; then "$tool" recover-switch --base "$base" --force || true; fi
        systemctl start remarkable-drive-cloud.service rm-sync.service xochitl.service remarkable-quickshare.service || true
    fi
    rm -f "$guard"
    systemctl daemon-reload || true
    systemctl stop "$watch" >/dev/null 2>&1 || true
    exit "$code"
}
trap restore_ui EXIT HUP INT TERM
[ -s "$base/private/recovery-settled.json" ] && [ -s "$base/private/recovery-needed" ] || { printf '%s\n' 'A verified offline recovery snapshot is required.' >&2; exit 1; }
systemctl stop remarkable-quickshare.service xochitl.service rm-sync.service remarkable-drive-cloud.service
systemd-run --unit="$watch" --property=Restart=no --property=RuntimeMaxSec=780 "$tool" guard-switch --base "$base"
prepared=true
"$tool" resume-switch --base "$base"
mkdir -p /run/systemd/system/xochitl.service.d
printf '%s\n' '# remarkable-drive-cloud migration guard' '[Service]' 'Restart=no' >"$guard"
systemctl daemon-reload
systemctl reset-failed remarkable-drive-cloud.service xochitl.service rm-sync.service
systemctl start remarkable-drive-cloud.service
"$tool" wait-local --base "$base"
"$tool" observe-native --base "$base" &
observer=$!
"$tool" wait-observer --base "$base"
systemctl start xochitl.service rm-sync.service
wait "$observer"
observer=
"$tool" verify-switch --base "$base"
"$tool" finish-switch --base "$base"
complete=true
systemctl restart remarkable-drive-cloud.service
systemctl start remarkable-quickshare.service
printf '%s\n' 'Native document synchronization now uses Google Drive. Previous account and native backups preserved.'
