#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
set -eu
umask 077
base=/home/root/drive-cloud
backup=${1:?A private verified native backup is required}
evidence=${2:?Private physical validation is required}
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
"$tool" check-switch --base "$base" --backup "$backup" --evidence "$evidence"
"$tool" prepare-recovery --base "$base"
"$tool" pair-local --base "$base"
systemctl daemon-reload
systemctl enable remarkable-drive-cloud-recover.service
printf '%s\n' 'Verified backup ready. Switching the native document connection.'
systemctl stop remarkable-quickshare.service xochitl.service rm-sync.service
"$tool" begin-switch --base "$base" --backup "$backup" --evidence "$evidence"
prepared=true
systemd-run --unit="$watch" --property=Restart=no --property=RuntimeMaxSec=780 "$tool" guard-switch --base "$base"
"$tool" backup-switch --base "$base"
systemctl stop remarkable-drive-cloud.service
"$tool" merge-backup --base "$base" --backup "$backup" --evidence "$evidence"
"$tool" install-switch --base "$base"
mkdir -p /run/systemd/system/xochitl.service.d
printf '%s\n' '# remarkable-drive-cloud migration guard' '[Service]' 'Restart=no' >"$guard"
systemctl daemon-reload
systemctl reset-failed remarkable-drive-cloud.service xochitl.service rm-sync.service
systemctl start remarkable-drive-cloud.service
"$tool" wait-local --base "$base"
"$tool" start-switch --base "$base"
"$tool" observe-native --base "$base" &
observer=$!
"$tool" wait-observer --base "$base"
# rm-sync BindsTo/After xochitl: either service can start the GUI. Subscribe
# before starting both, so the first native result cannot be missed.
systemctl start xochitl.service rm-sync.service
wait "$observer"
observer=
"$tool" verify-switch --base "$base"
"$tool" finish-switch --base "$base"
complete=true
systemctl restart remarkable-drive-cloud.service
systemctl start remarkable-quickshare.service
printf '%s\n' 'Native document synchronization now uses Google Drive. Previous account and native backups preserved.'
