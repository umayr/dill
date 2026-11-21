import hashlib
from pathlib import Path
import sys
from unittest.mock import patch

from test_publish_schema import MemoryRegistry, MemoryRelease, SchemaArtifactFixture, publisher


class BinaryManifestTests(SchemaArtifactFixture):
    def setUp(self):
        super().setUp()
        dist = self.paths[0].parent / 'dist'
        dist.mkdir()
        self.binaries = [dist / f'dill-{system}-{arch}'
                         for system in ('linux', 'darwin') for arch in ('arm64', 'amd64')]
        for binary in self.binaries:
            binary.write_bytes(b'\x00\r\n' + binary.name.encode())
        (dist / 'dill-import-linux-amd64').write_bytes(b'not released')
        self.release = MemoryRelease()
        self.registry = MemoryRegistry(self.release, self.paths[2:])

    def publish(self, binaries):
        argv = ['publish-schema.py', '--tag', 'v1.2.3', '--artifacts', str(self.paths[0].parent),
                '--registry', 'unused']
        for binary in binaries:
            argv.extend(['--binary', str(binary)])
        with patch.object(sys, 'argv', argv), \
                patch.object(publisher, 'GitHubRelease', return_value=self.release), \
                patch.object(publisher, 'GitRegistry', return_value=self.registry) as registry:
            publisher.main()
            registry.assert_called_once_with(Path('unused'), self.paths[2:])

    def test_manifest_matches_only_uploaded_binaries_and_is_deterministic(self):
        self.publish(self.binaries)
        assets = {p.name: p.read_bytes() for p in self.paths + self.binaries}
        assets['SHA256SUMS'] = ''.join(
            f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.name}\n' for p in sorted(self.binaries)
        ).encode('ascii')
        self.assertEqual(self.release.objects, assets)
        self.assertEqual(self.registry.objects, {p.name: p.read_bytes() for p in self.paths[2:]})
        self.publish(reversed(self.binaries))
        self.assertEqual(self.release.objects, assets)

    def test_manifest_upload_failure_blocks_discovery_and_retry_fills_it(self):
        self.release.fail = 'SHA256SUMS'
        with self.assertRaisesRegex(RuntimeError, 'injected failure'):
            self.publish(self.binaries)
        self.assertEqual(set(self.release.objects), {p.name for p in self.paths + self.binaries})
        self.assertEqual(self.registry.objects, {})
        self.release.fail = None
        self.publish(self.binaries)
        self.assertIn('SHA256SUMS', self.release.objects)
        self.assertEqual(set(self.registry.objects), {p.name for p in self.paths[2:]})

    def test_manifest_conflict_fails_before_any_writes(self):
        manifest = publisher.binary_manifest(self.binaries)
        self.release.objects['SHA256SUMS'] = manifest
        self.binaries[0].write_bytes(b'changed binary')
        with self.assertRaisesRegex(RuntimeError, 'release asset differs: SHA256SUMS'):
            self.publish(self.binaries)
        self.assertEqual(self.release.objects, {'SHA256SUMS': manifest})
        self.assertEqual(self.registry.objects, {})

    def test_missing_binary_fails_before_any_writes(self):
        with self.assertRaisesRegex(RuntimeError, 'missing release artifact'):
            self.publish(self.binaries + [self.binaries[0].with_name('dill-test-amd64')])
        self.assertEqual(self.release.objects, {})
        self.assertEqual(self.registry.objects, {})

    def test_duplicate_asset_name_fails_before_any_writes(self):
        duplicate = self.paths[0].parent / self.binaries[0].name
        duplicate.write_bytes(b'another file with the same release name')
        with self.assertRaisesRegex(RuntimeError, 'duplicate release binary name'):
            self.publish(self.binaries + [duplicate])
        self.assertEqual(self.release.objects, {})

    def test_unsafe_asset_name_fails_before_any_writes(self):
        invalid = self.binaries[0].with_name('dill-linux-amd64\nextra')
        invalid.write_bytes(b'not an asset name')
        with self.assertRaisesRegex(RuntimeError, 'invalid release binary name'):
            self.publish([invalid])
        self.assertEqual(self.release.objects, {})

    def test_schema_only_release_has_no_binary_manifest(self):
        self.publish([])
        self.assertEqual(set(self.release.objects), {p.name for p in self.paths})
