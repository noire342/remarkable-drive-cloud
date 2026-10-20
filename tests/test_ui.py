# SPDX-License-Identifier: AGPL-3.0-only
"""Validate our QML on synthetic resources, without distributing Xochitl."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
QMLDIFF = os.environ.get('QMLDIFF_BIN')

@unittest.skipUnless(QMLDIFF, 'Set QMLDIFF_BIN to pinned upstream CLI')
class CloudSettingsTests(unittest.TestCase):
    def cli(self, *args):
        result = subprocess.run([QMLDIFF, *map(str,args)],capture_output=True,text=True,timeout=30)
        self.assertEqual(result.returncode,0,result.stdout+result.stderr)

    def test_cloud_page_preserves_native_settings(self):
        with tempfile.TemporaryDirectory() as folder:
            folder = Path(folder)
            source, output = folder/'source', folder/'output'
            fixtures = {
                'qml/device/view/main/MainView.qml':'Item { id: root; property bool existingValue: true }',
                'qml/device/view/settings/Settings.qml':'''Item {
                    id: settings
                    property int _selectedIndex: 0
                    Item { objectName: "wifi-original" }
                    Item { objectName: "quickshare-original" }
                    Loader { id: payloadLoader; sourceComponent: originalCloud }
                    Component { id: originalCloud; Item {} }
                    Item { objectName: "help-last" }
                }''',
            }
            for name, content in fixtures.items():
                path=source/name;path.parent.mkdir(parents=True,exist_ok=True);path.write_text(content)
            table=folder/'hashtab'
            self.cli('create-hashtab',source,table,'--version','3.27.3.0')
            self.cli('apply-diffs','--hashtab',table,'--version','3.27.3.0',source,output,ROOT/'tablet-ui/drive-cloud.qmd')
            result=(output/'qml/device/view/settings/Settings.qml').read_text()
            for text in ['wifi-original','quickshare-original','help-last','originalCloud','driveCloudPage','SettingsPage.Cloud']:
                self.assertIn(text,result)
            self.assertIn('existingValue',(output/'qml/device/view/main/MainView.qml').read_text())
            self.assertEqual(result.count('id: driveCloudPage'),1)
            self.assertIn('payloadLoader.active',result)

    def test_qml_components_parse(self):
        with tempfile.TemporaryDirectory() as folder:
            folder=Path(folder);patch=folder/'parse.qmd'
            patch.write_text('\n'.join('AFFECT /tablet-ui/'+name+'\nTRAVERSE '+kind+
                '\nLOCATE AFTER ALL\nINSERT { property bool parserProbe: true }\nEND TRAVERSE\nEND AFFECT'
                for name,kind in [('CloudPage.qml','Item'),('CloudButton.qml','Rectangle')]))
            self.cli('apply-diffs',ROOT,folder/'output',patch)

if __name__=='__main__':unittest.main()
