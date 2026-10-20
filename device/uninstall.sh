#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
set -eu
base=/home/root/drive-cloud
if [ -f "$base/private/active" ];then
    printf '%s\n' 'Active Drive cloud detected. Reconcile/export your documents before disconnecting it; automatic return to the old cloud is blocked.' >&2
    exit 1
fi
systemctl disable --now remarkable-drive-cloud.service 2>/dev/null || true
for runtime in /home/root/xovi /home/root/quickshare/runtime/xovi "$base/runtime/xovi";do
    qmd="$runtime/exthome/qt-resource-rebuilder/drive-cloud.qmd"
    if [ -f "$qmd" ] && grep -q '/home/root/drive-cloud/' "$qmd";then rm -f "$qmd";fi
done
for drop in /etc/systemd/system/xochitl.service.d/70-drive-cloud.conf /home/root/xovi/services/xochitl.service/70-drive-cloud.conf;do
    if [ -f "$drop" ] && grep -q 'remarkable-drive-cloud owned' "$drop";then rm -f "$drop";fi
done
rm -f /etc/systemd/system/remarkable-drive-cloud.service
if [ ! -x /home/root/quickshare/launch-xochitl.sh ] && [ -f "$base/private/original-web-interface" ];then
    before=$(cat "$base/private/original-web-interface");case "$before" in true|false) sed -i "s/^WebInterfaceEnabled=.*/WebInterfaceEnabled=$before/" /home/root/.config/remarkable/xochitl.conf;;esac
fi
systemctl daemon-reload
systemctl restart xochitl
printf '%s\n' 'Drive cloud disabled. Native UI restored; credentials, backups and documents preserved.'
