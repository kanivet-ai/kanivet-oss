#!/usr/bin/env python3
import base64
import hashlib
import subprocess
import sys
import tempfile
from pathlib import Path

with tempfile.TemporaryDirectory() as directory:
    root = Path(directory)
    artifact = root / 'kanivet-standalone-1.2.3-arm64.AppImage'
    output = root / 'standalone-linux.yml'
    data = b'kanivet updater manifest test'
    artifact.write_bytes(data)
    subprocess.run([sys.executable, 'scripts/create_updater_manifest.py', str(artifact), '1.2.3', str(output)], check=True)
    digest = base64.b64encode(hashlib.sha512(data).digest()).decode()
    expected = f'version: 1.2.3\nfiles:\n  - url: {artifact.name}\n    sha512: {digest}\n    size: {len(data)}\npath: {artifact.name}\nsha512: {digest}\n'
    if output.read_text() != expected:
        raise SystemExit('Updater manifest does not match the artifact metadata')
    public_name = 'kanivet-standalone-setup-1.2.3.exe'
    subprocess.run([sys.executable, 'scripts/create_updater_manifest.py', str(artifact), '1.2.3', str(output), public_name], check=True)
    expected = expected.replace(artifact.name, public_name)
    if output.read_text() != expected:
        raise SystemExit('Updater manifest does not use the public artifact name')
print('Updater manifest checks passed')
