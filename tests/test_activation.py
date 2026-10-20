# SPDX-License-Identifier: AGPL-3.0-only
"""Exercise activation failures against an isolated service runner."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT=Path(__file__).resolve().parents[1]

@unittest.skipIf(os.name=='nt','POSIX activation runs on Linux')
class ActivationFailures(unittest.TestCase):
    def setUp(self):
        temporary=tempfile.TemporaryDirectory(prefix='cloud-activation-');self.addCleanup(temporary.cleanup)
        self.root=Path(temporary.name);self.base=self.root/'home/root/drive-cloud';self.fake=self.root/'fakebin'
        (self.base/'bin').mkdir(parents=True);self.fake.mkdir()
        (self.root/'run/systemd/system/xochitl.service.d').mkdir(parents=True)
        script=(ROOT/'device/activate.sh').read_text().replace('/home/root/drive-cloud',str(self.base)).replace('/run/systemd/system',str(self.root/'run/systemd/system'))
        self.script=self.root/'activate.sh';self.script.write_text(script)
        self.executable(self.base/'bin/drive-cloud','''#!/bin/sh
printf 'tool %s\n' "$*" >>"$CALLS"
[ "$1" != "${FAIL_AT:-none}" ] || exit 42
exit 0
''')
        self.executable(self.fake/'systemctl','''#!/bin/sh
printf 'systemctl %s\n' "$*" >>"$CALLS"
exit 0
''')
        self.executable(self.fake/'systemd-run','''#!/bin/sh
printf 'guard %s\n' "$*" >>"$CALLS"
exit 0
''')
        self.executable(self.fake/'sleep','#!/bin/sh\nexit 0\n')
        self.env=dict(os.environ,PATH=str(self.fake)+os.pathsep+os.environ['PATH'],CALLS=str(self.root/'calls.log'))
    def executable(self,path,text):path.write_text(text);path.chmod(0o755)
    def run_activation(self,fail=None):
        env=dict(self.env,FAIL_AT=fail or 'none')
        result=subprocess.run(['sh',str(self.script),str(self.base/'backup'),str(self.base/'validation.json')],env=env,capture_output=True,text=True,timeout=10)
        return result,(self.root/'calls.log').read_text()
    def test_success_verifies_source_and_remote_before_switching(self):
        result,calls=self.run_activation();self.assertEqual(result.returncode,0,result.stdout+result.stderr)
        self.assertLess(calls.index('tool check-switch'),calls.index('systemctl stop remarkable-quickshare'))
        self.assertLess(calls.index('tool merge-backup'),calls.index('tool install-switch'))
        self.assertLess(calls.index('tool wait-local'),calls.index('systemctl start xochitl'))
        self.assertLess(calls.index('tool wait-observer'),calls.index('systemctl start xochitl.service rm-sync.service'))
        self.assertNotIn('systemctl start rm-sync.service\n',calls)
        self.assertLess(calls.index('tool verify-switch'),calls.index('tool finish-switch'))
        self.assertNotIn('tool recover-switch',calls)
        self.assertFalse((self.root/'run/systemd/system/xochitl.service.d/zzzz-drive-cloud-switch.conf').exists())
    def test_invalid_preflight_never_stops_native_interface(self):
        result,calls=self.run_activation('check-switch');self.assertNotEqual(result.returncode,0)
        self.assertNotIn('systemctl stop remarkable-quickshare',calls)
        self.assertNotIn('tool install-switch',calls)
    def test_failed_remote_backup_recovers_without_installing_account(self):
        result,calls=self.run_activation('merge-backup');self.assertNotEqual(result.returncode,0)
        self.assertIn('tool recover-switch',calls);self.assertIn('--force',calls)
        self.assertNotIn('tool install-switch',calls)
        self.assertIn('systemctl start remarkable-drive-cloud.service rm-sync.service xochitl.service remarkable-quickshare.service',calls)
    def test_failed_native_verification_cannot_mark_migration_complete(self):
        result,calls=self.run_activation('verify-switch');self.assertNotEqual(result.returncode,0)
        self.assertIn('tool recover-switch',calls);self.assertNotIn('tool finish-switch',calls)
        self.assertFalse((self.root/'run/systemd/system/xochitl.service.d/zzzz-drive-cloud-switch.conf').exists())
    def test_failed_native_batch_cannot_mark_migration_complete(self):
        result,calls=self.run_activation('observe-native');self.assertNotEqual(result.returncode,0)
        self.assertIn('tool recover-switch',calls);self.assertNotIn('tool verify-switch',calls)
    def test_resume_failure_stays_on_existing_local_account(self):
        self.script.write_text((ROOT/'device/resume.sh').read_text().replace('/home/root/drive-cloud',str(self.base)).replace('/run/systemd/system',str(self.root/'run/systemd/system')))
        (self.base/'private').mkdir()
        for name in ['recovery-settled.json','recovery-needed']:(self.base/'private'/name).write_text('private verified fixture')
        result,calls=self.run_activation('verify-switch')
        self.assertNotEqual(result.returncode,0)
        self.assertIn('tool resume-switch',calls);self.assertIn('tool recover-switch',calls)
        self.assertNotIn('tool pair-local',calls);self.assertNotIn('tool install-switch',calls);self.assertNotIn('tool finish-switch',calls)
        self.assertFalse((self.root/'run/systemd/system/xochitl.service.d/zzzz-drive-cloud-switch.conf').exists())

if __name__=='__main__':unittest.main()
