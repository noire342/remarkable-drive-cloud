#!/usr/bin/env python3
"""Collect exact dependency license texts from the downloaded Go module cache."""
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Lorenzo Garrone (noire342)
import argparse
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
LICENSE_IDS = {
    'github.com/cenkalti/backoff': 'MIT',
    'github.com/grandcat/zeroconf': 'MIT',
    'github.com/miekg/dns': 'BSD-3-Clause',
    'github.com/google/go-cmp': 'BSD-3-Clause',
    'github.com/golang/protobuf': 'BSD-3-Clause',
    'github.com/pkg/errors': 'BSD-2-Clause',
    'google.golang.org/protobuf': 'BSD-3-Clause',
}

def objects(text):
    decoder = json.JSONDecoder()
    while text.strip():
        text = text.lstrip()
        value, end = decoder.raw_decode(text)
        yield value
        text = text[end:]

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--go', default='go')
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    def go(*command):
        return subprocess.check_output([args.go, *command], cwd=ROOT, text=True, encoding='utf-8')
    packages = list(objects(go('list', '-deps', '-json', '-tags', 'cloud', './cmd/drive-cloud', './cmd/drive-cloud-setup')))
    resolved = {p['Module']['Path']:p['Module'] for p in packages if p.get('Module')}
    modules = [resolved[k] for k in sorted(resolved)]
    reviewed = json.loads((ROOT/'licenses/module-licenses.json').read_text())
    files = {}
    manifest = []
    for module in modules:
        if module.get('Main'):
            continue
        name = module['Path']
        license_id = 'BSD-3-Clause' if name.startswith('golang.org/x/') else reviewed.get(name)
        if not license_id:
            raise SystemExit('Unreviewed dependency license: '+name)
        directory = Path(module['Dir']) if module.get('Dir') else None
        if directory is None or not directory.is_dir():
            raise SystemExit('Dependency is not present: '+name)
        notices = [p for p in directory.iterdir() if p.is_file() and (p.name.upper().startswith(('LICENSE','COPYING','NOTICE')) or p.name.upper()=='PATENTS')]
        if not any(p.name.upper().startswith(('LICENSE','COPYING')) for p in notices):
            raise SystemExit('Missing license text: '+name)
        safe = re.sub(r'[^A-Za-z0-9._-]', '_', name)
        paths = []
        for notice in sorted(notices):
            destination = f'go-modules/{safe}/{notice.name}'
            files[destination] = notice.read_bytes()
            paths.append('licenses/'+destination)
        manifest.append({'module':name, 'version':module['Version'], 'license':license_id, 'notices':paths})
    goroot = Path(go('env','GOROOT').strip())
    files['Go-BSD-3-Clause.txt'] = (goroot/'LICENSE').read_bytes()
    files['go-modules.json'] = (json.dumps(manifest, indent=2)+'\n').encode()
    changed = []
    for relative, content in files.items():
        path = ROOT/'licenses'/relative
        if not path.exists() or path.read_bytes()!=content:
            changed.append(relative)
            if not args.check:
                path.parent.mkdir(parents=True,exist_ok=True)
                path.write_bytes(content)
    expected = {str((ROOT/'licenses'/name).resolve()) for name in files}
    module_dir = ROOT/'licenses'/'go-modules'
    stale = [path for path in module_dir.rglob('*') if path.is_file() and str(path.resolve()) not in expected] if module_dir.exists() else []
    if stale:
        raise SystemExit('Stale dependency notices require review: '+', '.join(str(p.relative_to(ROOT)) for p in stale))
    if args.check and changed:
        raise SystemExit('Run collect_licenses.py; notices differ: '+', '.join(changed))
    print(f'License texts verified for {len(manifest)} Go modules and the Go runtime.')

if __name__=='__main__':
    main()
