# SPDX-License-Identifier: AGPL-3.0-only
"""Run the real installer scripts against a fake tablet, never host systemd."""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[1]

@unittest.skipIf(os.name=='nt','POSIX fake tablet runs on Linux')
class InstallerTransactions(unittest.TestCase):
    def setUp(self):
        temp=tempfile.TemporaryDirectory(prefix='drive-cloud-test-');self.addCleanup(temp.cleanup)
        self.root=Path(temp.name);self.base=self.root/'home/root/drive-cloud';self.payload=self.root/'payload';self.fake=self.root/'fakebin'
        for name in ['payload/device','payload/bin','payload/tablet-ui','payload/licenses','fakebin','home/root/drive-cloud/bin','home/root/drive-cloud/private','home/root/xovi/extensions.d','home/root/xovi/exthome/qt-resource-rebuilder','home/root/xovi/services/xochitl.service','home/root/quickshare','home/root/.config/remarkable','etc/systemd/system','run/systemd/system/xochitl.service.d','usr/bin']:
            (self.root/name).mkdir(parents=True,exist_ok=True)
        self.conf=self.root/'home/root/.config/remarkable/xochitl.conf';self.conf.write_text('[General]\nWebInterfaceEnabled=false\nUserToken=original-cloud-fixture\n')
        self.qrr=self.root/'home/root/xovi/exthome/qt-resource-rebuilder'
        for name in ['xovi.so','extensions.d/qt-resource-rebuilder.so','extensions.d/other.so']:(self.root/'home/root/xovi'/name).write_text('existing owner runtime')
        (self.qrr/'hashtab').write_text('existing table')
        (self.qrr/'quickshare.qmd').write_text('keep Quick Share')
        (self.base/'bin/drive-cloud').write_text('previous daemon')
        (self.base/'bin/xochitl-local-import').write_text('previous native copy')
        (self.base/'private/keep-receipts').write_text('owner receipts')
        (self.root/'boot-id').write_text('boot-fixture')
        (self.root/'usr/bin/xochitl').write_text('owner firmware')
        mapping={p:str(self.root/p.lstrip('/')) for p in ['/home/root/drive-cloud','/home/root/xovi','/home/root/quickshare','/home/root/.config/remarkable/xochitl.conf','/etc/systemd/system','/run/systemd/system','/usr/bin/xochitl']}
        mapping['/proc/sys/kernel/random/boot_id']=str(self.root/'boot-id')
        for p in (ROOT/'device').iterdir():
            if not p.is_file():continue
            text=p.read_text()
            for before,after in mapping.items():text=text.replace(before,after)
            (self.payload/'device'/p.name).write_text(text)
        self.executable(self.payload/'device/check-firmware.sh','#!/bin/sh\n[ "${FAIL_FIRMWARE:-0}" = 0 ]\n')
        self.executable(self.payload/'bin/drive-cloud','''#!/bin/sh
case "$1" in
 prepare-native) while [ "$#" -gt 0 ];do if [ "$1" = --output ];then shift;printf new-native >"$1";chmod 755 "$1";break;fi;shift;done;;
 init) mkdir -p "$TEST_BASE/private" "$TEST_BASE/ui";printf token-fixture >"$TEST_BASE/control-token";;
 prepare-ui) printf candidate-fixture >"$TEST_BASE/private/ui-nonce";printf config-fixture >"$TEST_BASE/ui/Config.js";;
esac
''')
        for name in ['LICENSE','THIRD_PARTY_NOTICES.md']:(self.payload/name).write_text('fixture notice')
        (self.payload/'licenses/notice').write_text('dependency notice')
        (self.payload/'tablet-ui/CloudPage.qml').write_text('Item {}')
        (self.payload/'tablet-ui/drive-cloud.qmd').write_text(str(self.base)+'/ui/CloudPage.qml')
        commands={
            'id':'#!/bin/sh\necho 0\n',
            'df':'#!/bin/sh\nprintf "Filesystem 1024-blocks Used Available Capacity Mounted on\\nfixture 999999 1 999998 1%% /home\\n"\n',
            'sleep':'#!/bin/sh\nexit 0\n',
            'date':'''#!/bin/sh
if [ "$*" = +%s ];then n=1000;[ ! -f "$TEST_ROOT/clock" ]||n=$(cat "$TEST_ROOT/clock");n=$((n+5));echo "$n" >"$TEST_ROOT/clock";echo "$n";else /bin/date "$@";fi
''',
            'systemctl':'''#!/bin/sh
printf '%s\\n' "$*" >>"$TEST_ROOT/systemctl.log"
case "$1" in
 is-enabled) echo disabled;exit 1;;
 is-active) if [ "$2" = --quiet ];then exit 0;fi;echo inactive;exit 3;;
 show) case "$*" in
  *'Restart --value') if find "$TEST_ROOT/run" -name 'zzzz-remarkable-drive-cloud-validation-*.conf' | grep -q .;then echo no;else echo on-failure;fi;;
  *'ActiveState --value') if [ -f "$TEST_ROOT/crashed" ];then echo failed;else echo active;fi;;esac;;
 restart) if [ "$2" = xochitl ];then
  if [ -f "$TEST_ROOT/home/root/xovi/exthome/qt-resource-rebuilder/drive-cloud.qmd" ];then
   find "$TEST_ROOT/run" -name 'zzzz-remarkable-drive-cloud-validation-*.conf' | grep -q . || exit 1
  fi
  [ "${CRASH_UI:-0}" = 0 ]||touch "$TEST_ROOT/crashed"
  if [ "${FAIL_UI:-0}" = 0 ] && [ -f "$TEST_BASE/private/ui-nonce" ];then cp "$TEST_BASE/private/ui-nonce" "$TEST_BASE/private/ui-ready";fi
 fi;;
 reset-failed) rm -f "$TEST_ROOT/crashed";;
esac
'''}
        for name,body in commands.items():self.executable(self.fake/name,body)
        self.env=dict(os.environ,PATH=str(self.fake)+os.pathsep+os.environ['PATH'],TEST_ROOT=str(self.root),TEST_BASE=str(self.base))
        self.checksums()

    @staticmethod
    def executable(path,text):path.write_text(text);path.chmod(0o755)

    def checksums(self):
        files=sorted(p for p in self.payload.rglob('*') if p.is_file() and p.name!='PAYLOAD.sha256')
        (self.payload/'PAYLOAD.sha256').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.relative_to(self.payload).as_posix()+'\n' for p in files))

    def run_install(self,**flags):return subprocess.run(['sh',str(self.payload/'device/install-device.sh')],env=dict(self.env,**flags),capture_output=True,text=True,timeout=20)

    def assert_owner_data(self):
        self.assertIn('UserToken=original-cloud-fixture',self.conf.read_text())
        self.assertEqual((self.base/'private/keep-receipts').read_text(),'owner receipts')
        self.assertEqual((self.qrr/'quickshare.qmd').read_text(),'keep Quick Share')
        self.assertEqual((self.root/'home/root/xovi/extensions.d/other.so').read_text(),'existing owner runtime')
        self.assertEqual((self.root/'usr/bin/xochitl').read_text(),'owner firmware')

    def test_success_reuses_xovi_and_preparation_uninstall(self):
        r=self.run_install();self.assertEqual(r.returncode,0,r.stdout+r.stderr)
        self.assertTrue((self.qrr/'drive-cloud.qmd').is_file())
        self.assertFalse((self.base/'ui-validation.pending').exists())
        r=subprocess.run(['sh',str(self.base/'device/uninstall.sh')],env=self.env,capture_output=True,text=True,timeout=20)
        self.assertEqual(r.returncode,0,r.stdout+r.stderr)
        self.assertFalse((self.qrr/'drive-cloud.qmd').exists());self.assert_owner_data()

    def test_crashed_ui_rolls_back_without_restart_loop(self):
        r=self.run_install(CRASH_UI='1');self.assertNotEqual(r.returncode,0)
        self.assertEqual((self.base/'bin/drive-cloud').read_text(),'previous daemon')
        self.assertEqual((self.base/'bin/xochitl-local-import').read_text(),'previous native copy')
        self.assertFalse((self.qrr/'drive-cloud.qmd').exists())
        self.assertFalse(list((self.root/'run').rglob('zzzz-remarkable-drive-cloud-validation-*.conf')))
        self.assertEqual((self.root/'systemctl.log').read_text().splitlines().count('restart xochitl'),1)
        self.assert_owner_data()

    def test_missing_readiness_rolls_back(self):
        r=self.run_install(FAIL_UI='1');self.assertNotEqual(r.returncode,0)
        self.assertFalse((self.qrr/'drive-cloud.qmd').exists());self.assert_owner_data()

    def test_firmware_mismatch_does_not_change_installation(self):
        r=self.run_install(FAIL_FIRMWARE='1');self.assertNotEqual(r.returncode,0)
        self.assertEqual((self.base/'bin/drive-cloud').read_text(),'previous daemon');self.assert_owner_data()

if __name__=='__main__':unittest.main()
