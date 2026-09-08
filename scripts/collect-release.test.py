import importlib.util
from pathlib import Path
import tempfile
import unittest

import yaml

spec = importlib.util.spec_from_file_location('collect_release', Path(__file__).with_name('collect-release.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class CollectTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / 'artifacts'
        self.destination = self.root / 'assets'

    def artifact(self, arch, version='0.2.0', filename=None):
        folder = self.source / arch
        folder.mkdir(parents=True)
        filename = filename or f'kanivet-{arch}.zip'
        (folder / filename).write_bytes(b'app')
        (folder / 'latest-mac.yml').write_text(yaml.safe_dump({
            'version': version, 'path': filename, 'sha512': 'hash',
            'files': [{'url': filename, 'sha512': 'hash', 'size': 3}],
        }))

    def test_combines_architectures_and_checksums(self):
        self.artifact('arm64')
        self.artifact('x64')
        module.collect(self.source, self.destination)
        metadata = yaml.safe_load((self.destination / 'latest-mac.yml').read_text())
        self.assertEqual({f['url'] for f in metadata['files']}, {'kanivet-arm64.zip', 'kanivet-x64.zip'})
        self.assertEqual(len((self.destination / 'SHA256SUMS').read_text().splitlines()), 3)

    def test_rejects_different_versions(self):
        self.artifact('arm64')
        self.artifact('x64', version='0.3.0')
        with self.assertRaisesRegex(ValueError, 'Conflicting versions'):
            module.collect(self.source, self.destination)

    def test_rejects_overwriting_installers(self):
        self.artifact('arm64', filename='same.zip')
        self.artifact('x64', filename='same.zip')
        with self.assertRaisesRegex(ValueError, 'Duplicate release asset'):
            module.collect(self.source, self.destination)

    def test_rejects_missing_updater_asset(self):
        self.artifact('arm64')
        (self.source / 'arm64' / 'kanivet-arm64.zip').unlink()
        with self.assertRaisesRegex(ValueError, 'Missing updater asset'):
            module.collect(self.source, self.destination)


if __name__ == '__main__':
    unittest.main()
