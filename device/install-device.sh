#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
set -eu
umask 077
base=/home/root/drive-cloud
payload=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
unit=remarkable-drive-cloud.service
drop=/etc/systemd/system/xochitl.service.d/70-drive-cloud.conf
persistent=/home/root/xovi/services/xochitl.service/70-drive-cloud.conf
done=0
changed=0
die() { printf 'ERROR: %s\n' "$*" >&2;exit 1; }
[ "$(id -u)" = 0 ] || die 'Run as root on the tablet.'
sh "$payload/device/check-firmware.sh"
[ -f "$payload/PAYLOAD.sha256" ] || die 'Checksum manifest missing.'
(cd "$payload" && sha256sum -c PAYLOAD.sha256) || die 'Payload integrity failure.'
[ ! -L "$base" ] || die 'Refusing symlink installation root.'
free=$(df -Pk /home | awk 'NR==2 {print $4}')
[ "${free:-0}" -gt 196608 ] || die 'At least 192 MiB free space required.'
for file in "$drop" "$persistent"; do
    if [ -e "$file" ] && ! grep -q 'remarkable-drive-cloud owned' "$file"; then die "Unrelated launcher: $file";fi
done
mkdir -p "$base/bin" "$base/device" "$base/private" "$base/backups"
transaction="$base/backups/install-$(date -u +%Y%m%dT%H%M%SZ)-$$"
mkdir -p "$transaction/stage"
save() { if [ -f "$1" ];then cp -p "$1" "$transaction/$2";else : >"$transaction/$2.absent";fi; }
restore() { if [ -f "$transaction/$2.absent" ];then rm -f "$1";elif [ -f "$transaction/$2" ];then cp -p "$transaction/$2" "$1.restore-$$";mv "$1.restore-$$" "$1";fi; }
save "$drop" launcher
save "$persistent" persistent-launcher
save /etc/systemd/system/remarkable-drive-cloud.service service
save "$base/bin/drive-cloud" binary
save "$base/bin/xochitl-local-import" native-binary
save "$base/private/ui-nonce" previous-ui-nonce
save "$base/private/ui-ready" previous-ui-ready
was_enabled=$(systemctl is-enabled "$unit" 2>/dev/null || true)
was_active=$(systemctl is-active "$unit" 2>/dev/null || true)
for file in "$payload"/device/*; do name=${file##*/};save "$base/device/$name" "device-$name";done
web=$(sed -n 's/^WebInterfaceEnabled=//p' /home/root/.config/remarkable/xochitl.conf | head -n1)
[ -f "$base/private/original-web-interface" ] || printf '%s\n' "${web:-false}" >"$base/private/original-web-interface"
"$payload/bin/drive-cloud" prepare-native --input /usr/bin/xochitl --output "$transaction/stage/xochitl-local-import"
guard="/run/systemd/system/xochitl.service.d/zzzz-remarkable-drive-cloud-validation-$$.conf"
cleanup_guard() { rm -f "$guard";systemctl daemon-reload;systemctl reset-failed xochitl || true; }
rollback() {
    code=$?
    trap - EXIT
    if [ "$done" != 1 ] && [ "$changed" = 1 ];then
        systemctl stop "$unit" 2>/dev/null || true
        sh "$base/device/install-ui.sh" rollback "$transaction" || true
        restore "$drop" launcher
        restore "$persistent" persistent-launcher
        restore /etc/systemd/system/remarkable-drive-cloud.service service
        restore "$base/bin/drive-cloud" binary
        restore "$base/bin/xochitl-local-import" native-binary
        restore "$base/private/ui-nonce" previous-ui-nonce
        restore "$base/private/ui-ready" previous-ui-ready
        for file in "$payload"/device/*;do name=${file##*/};restore "$base/device/$name" "device-$name";done
        cleanup_guard
        if [ "$was_enabled" != enabled ]; then systemctl disable "$unit" >/dev/null 2>&1 || true;fi
        if [ "$was_active" = active ]; then systemctl start "$unit" || true;fi
        case "$web" in true|false) sed -i "s/^WebInterfaceEnabled=.*/WebInterfaceEnabled=$web/" /home/root/.config/remarkable/xochitl.conf;;esac
        systemctl start xochitl || true
        printf '%s\n' 'Candidate removed; previous UI restored. Native cloud account and documents were not migrated.' >&2
    fi
    exit "$code"
}
trap rollback EXIT
trap 'exit 130' HUP INT TERM
changed=1
mkdir -p /run/systemd/system/xochitl.service.d
printf '%s\n' "$guard" >"$transaction/ui-validation.guard"
printf '# remarkable-drive-cloud candidate validation\n[Service]\nRestart=no\n' >"$guard"
systemctl daemon-reload
[ "$(systemctl show xochitl -p Restart --value)" = no ] || die 'Cannot suppress restart loops.'
systemctl stop "$unit" 2>/dev/null || true
cp "$payload/bin/drive-cloud" "$base/bin/drive-cloud.new";chmod 755 "$base/bin/drive-cloud.new";mv "$base/bin/drive-cloud.new" "$base/bin/drive-cloud"
mv "$transaction/stage/xochitl-local-import" "$base/bin/xochitl-local-import"
cp "$payload"/device/* "$base/device/";chmod 700 "$base"/device/*.sh
cp "$payload/LICENSE" "$payload/THIRD_PARTY_NOTICES.md" "$base/"
mkdir -p "$base/licenses";cp -R "$payload/licenses/." "$base/licenses/"
"$base/bin/drive-cloud" init --base "$base"
cp "$payload/device/remarkable-drive-cloud.service" /etc/systemd/system/remarkable-drive-cloud.service
mkdir -p "$(dirname "$drop")"
cp "$payload/device/70-drive-cloud.conf" "$drop"
if [ -d "$(dirname "$persistent")" ];then cp "$payload/device/70-drive-cloud.conf" "$persistent";fi
cat /proc/sys/kernel/random/boot_id >"$transaction/ui-validation.boot-id"
printf '%s\n' "$transaction" >"$base/ui-validation.pending.new"
rm -f "$base/ui-validation.attempted" "$base/private/ui-ready"
mv "$base/ui-validation.pending.new" "$base/ui-validation.pending"
sh "$base/device/install-ui.sh" "$payload" "$transaction"
systemctl daemon-reload
systemctl show xochitl -p ExecStart --value | grep -q -- 'launch-xochitl.sh --system' || die 'Native system lifecycle argument missing; refusing UI restart.'
systemctl enable "$unit" >/dev/null
systemctl restart "$unit"
systemctl restart xochitl
ready=0;deadline=$(( $(date +%s) + 60 ))
while [ "$(date +%s)" -lt "$deadline" ];do
    state=$(systemctl show xochitl -p ActiveState --value)
    case "$state" in failed|inactive) die 'Candidate UI failed; rolling back.';;esac
    if systemctl is-active --quiet "$unit" && [ -s "$base/private/ui-ready" ] && cmp -s "$base/private/ui-ready" "$base/private/ui-nonce";then ready=1;break;fi
    sleep 1
done
[ "$ready" = 1 ] || die 'Cloud settings did not report readiness; rolling back.'
sh "$base/device/install-ui.sh" finish-ui-validation "$transaction"
cleanup_guard
printf '%s\n' "$transaction" >"$base/private/last-install"
done=1
printf '%s\n' 'Installed Settings > Cloud in preparation mode. Old cloud and document library unchanged.'
