#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Build private development artifacts from an explicit source allowlist."""
import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]

def sha256(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--go', default='go')
    args = parser.parse_args()
    output = ROOT/'build'
    output.mkdir(exist_ok=True)
    def build(target, package, system, arch, arm=''):
        env = dict(os.environ, CGO_ENABLED='0', GOOS=system, GOARCH=arch)
        if arm:
            env['GOARM'] = arm
        subprocess.run([args.go, 'build', '-tags', 'cloud', '-trimpath', '-ldflags=-s -w',
                        '-o', str(target), package], cwd=ROOT, env=env, check=True)
    with tempfile.TemporaryDirectory(prefix='drive-cloud-build-') as folder:
        stage = Path(folder)
        (stage/'bin').mkdir()
        build(stage/'bin/drive-cloud', './cmd/drive-cloud', 'linux', 'arm', '7')
        for name in ['device', 'tablet-ui', 'licenses']:
            shutil.copytree(ROOT/name, stage/name)
        for name in ['LICENSE', 'THIRD_PARTY_NOTICES.md']:
            shutil.copyfile(ROOT/name, stage/name)
        files = sorted(p for p in stage.rglob('*') if p.is_file())
        (stage/'PAYLOAD.sha256').write_text(''.join(
            sha256(p)+'  '+p.relative_to(stage).as_posix()+'\n' for p in files), encoding='ascii')
        bundle = output/'remarkable-drive-cloud-linux-armv7.tar.gz'
        with tarfile.open(bundle, 'w:gz', format=tarfile.PAX_FORMAT) as archive:
            for p in sorted(stage.rglob('*')):
                if p.is_file():
                    archive.add(p, arcname=p.relative_to(stage).as_posix(), recursive=False)
    for system, arch in [('windows','amd64'), ('windows','arm64'), ('darwin','amd64'),
                         ('darwin','arm64'), ('linux','amd64'), ('linux','arm64')]:
        name = f'drive-cloud-setup-{system}-{arch}' + ('.exe' if system=='windows' else '')
        build(output/name, './cmd/drive-cloud-setup', system, arch)
    outputs = [bundle] + sorted(output.glob('drive-cloud-setup-*'))
    (output/'SHA256SUMS').write_text(''.join(sha256(p)+'  '+p.name+'\n' for p in outputs), encoding='ascii')
    print('Development artifacts only. Physical acceptance is required before release.')

if __name__ == '__main__':
    main()
