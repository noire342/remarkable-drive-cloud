# SPDX-License-Identifier: AGPL-3.0-only
"""Check the actual tar format consumed by the tablet, including Windows builds."""
import hashlib
import importlib.util
from pathlib import Path
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('builder', ROOT/'scripts/build_development.py')
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)

class PortablePayload(unittest.TestCase):
    def test_executable_permissions_checksums_and_unmodified_license_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stage = root/'payload'
            fixtures = {
                'bin/drive-cloud': b'arm-fixture\x00',
                'device/install-device.sh': b'#!/bin/sh\nexit 0\n',
                'licenses/upstream.txt': b'Original license\r\nOriginal bytes\r\n',
            }
            for name, data in fixtures.items():
                target = stage/name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(data)
                target.chmod(0o644)  # Go output on Windows has no execute bit.
            bundle = root/'payload.tar.gz'
            builder.package_payload(stage, bundle)
            with tarfile.open(bundle, 'r:gz') as archive:
                self.assertEqual(archive.getmember('bin/drive-cloud').mode, 0o755)
                self.assertEqual(archive.getmember('device/install-device.sh').mode, 0o755)
                self.assertEqual(archive.getmember('licenses/upstream.txt').mode, 0o644)
                manifest = archive.extractfile('PAYLOAD.sha256').read()
                self.assertNotIn(b'\r', manifest)
                entries = dict(line.split('  ', 1)[::-1] for line in manifest.decode('ascii').splitlines())
                self.assertEqual(set(entries), set(fixtures))
                for name, data in fixtures.items():
                    actual = archive.extractfile(name).read()
                    self.assertEqual(actual, data)
                    self.assertEqual(entries[name], hashlib.sha256(actual).hexdigest())

if __name__ == '__main__':
    unittest.main()
