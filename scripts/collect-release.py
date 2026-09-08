"""Combine matrix artifacts without losing per-architecture updater entries."""
import hashlib
from pathlib import Path
import shutil
import sys

import yaml


def collect(source, destination):
    destination.mkdir(parents=True, exist_ok=True)
    metadata = {}
    for path in sorted(source.glob('*/*')):
        if path.suffix == '.yml':
            incoming = yaml.safe_load(path.read_text())
            if not incoming.get('version') or not incoming.get('files'):
                raise ValueError(f'Invalid updater metadata: {path}')
            if path.name in metadata:
                current = metadata[path.name]
                if current['version'] != incoming['version']:
                    raise ValueError(f'Conflicting versions: {path.name}')
                for entry in incoming['files']:
                    if entry not in current['files']:
                        current['files'].append(entry)
            else:
                metadata[path.name] = incoming
        else:
            target = destination / path.name
            if target.exists():
                raise ValueError(f'Duplicate release asset: {path.name}')
            shutil.copyfile(path, target)
    if not metadata:
        raise ValueError('No updater metadata found')
    for name, data in metadata.items():
        for entry in data['files']:
            if not (destination / entry['url']).is_file():
                raise ValueError(f'Missing updater asset: {entry["url"]}')
        (destination / name).write_text(yaml.safe_dump(data, sort_keys=False))
    with (destination / 'SHA256SUMS').open('w') as checksums:
        for path in sorted(destination.iterdir()):
            if path.name != 'SHA256SUMS':
                digest = hashlib.sha256()
                with path.open('rb') as asset:
                    for chunk in iter(lambda: asset.read(1024 * 1024), b''):
                        digest.update(chunk)
                checksums.write(f'{digest.hexdigest()}  {path.name}\n')


if __name__ == '__main__':
    collect(Path(sys.argv[1]), Path(sys.argv[2]))
