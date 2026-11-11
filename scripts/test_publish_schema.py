import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import subprocess
import unittest
import zipfile

spec = importlib.util.spec_from_file_location('publisher', Path(__file__).with_name('publish-schema.py'))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class MemoryRelease:
    def __init__(self):
        self.objects = {}
        self.fail = None
    def read(self, name):
        return self.objects.get(name)
    def write(self, path):
        if path.name == self.fail:
            raise RuntimeError('injected failure')
        if path.name in self.objects:
            raise RuntimeError('replacement attempted')
        self.objects[path.name] = path.read_bytes()


class MemoryRegistry:
    def __init__(self, release, files):
        self.release, self.files, self.objects = release, files, {}
    def check(self):
        for p in self.files:
            if p.name in self.objects and self.objects[p.name] != p.read_bytes():
                raise RuntimeError('registry conflict')
    def publish(self):
        self.check()
        assert all(self.release.read(p.name) == p.read_bytes() for p in self.files)
        self.objects.update({p.name: p.read_bytes() for p in self.files})


class ArtifactValidationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        self.paths = [root / ('dill@1.2.3' + suffix) for suffix in ('.zip', '.zip.sha256', '', '.sha256')]
        with zipfile.ZipFile(self.paths[0], 'w') as z:
            z.writestr('dill.pkl', 'schema')
        self.metadata = {
            'name': 'dill', 'version': '1.2.3',
            'packageUri': 'package://pkg.ushd.ca/dill@1.2.3',
            'packageZipUrl': 'https://github.com/umayr/dill/releases/download/v1.2.3/dill@1.2.3.zip',
        }
        self.refresh_checksums()

    def refresh_checksums(self):
        archive, archive_checksum, metadata, metadata_checksum = self.paths
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        archive_checksum.write_text(digest)
        self.metadata['packageZipChecksums'] = {'sha256': digest}
        metadata.write_text(json.dumps(self.metadata))
        metadata_checksum.write_text(hashlib.sha256(metadata.read_bytes()).hexdigest())

    def test_valid_artifacts(self):
        publisher.validate_schema_artifacts('1.2.3', self.paths)

    def test_checksum_mismatches_rejected(self):
        for index in (1, 3):
            with self.subTest(checksum=index):
                self.refresh_checksums()
                self.paths[index].write_text('incorrect')
                with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'):
                    publisher.validate_schema_artifacts('1.2.3', self.paths)

    def test_wrong_metadata_rejected_even_with_matching_checksum(self):
        for key in ('name', 'version', 'packageUri', 'packageZipUrl'):
            with self.subTest(key=key):
                original = self.metadata[key]
                self.metadata[key] = 'wrong'
                self.refresh_checksums()
                with self.assertRaisesRegex(RuntimeError, 'metadata identity'):
                    publisher.validate_schema_artifacts('1.2.3', self.paths)
                self.metadata[key] = original
        self.refresh_checksums()
        self.metadata['packageZipChecksums']['sha256'] = 'wrong'
        self.paths[2].write_text(json.dumps(self.metadata))
        self.paths[3].write_text(hashlib.sha256(self.paths[2].read_bytes()).hexdigest())
        with self.assertRaisesRegex(RuntimeError, 'metadata identity'):
            publisher.validate_schema_artifacts('1.2.3', self.paths)

    def test_extra_archive_file_rejected_even_with_matching_checksums(self):
        with zipfile.ZipFile(self.paths[0], 'a') as z:
            z.writestr('secret.key', 'synthetic secret')
        self.refresh_checksums()
        with self.assertRaisesRegex(RuntimeError, 'archive contents'):
            publisher.validate_schema_artifacts('1.2.3', self.paths)


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.files = [Path(self.tmp.name) / name for name in ('dill@1.zip', 'dill@1.zip.sha256', 'dill@1', 'dill@1.sha256')]
        for p in self.files:
            p.write_bytes(p.name.encode())
        self.release = MemoryRelease()
        self.registry = MemoryRegistry(self.release, self.files[2:])
    def test_partial_retry_and_idempotence(self):
        self.release.fail = self.files[1].name
        with self.assertRaises(RuntimeError):
            publisher.publish(self.files, self.release, self.registry)
        self.assertEqual(self.registry.objects, {})
        self.release.fail = None
        publisher.publish(self.files, self.release, self.registry)
        publisher.publish(self.files, self.release, self.registry)
        self.assertEqual(len(self.release.objects), 4)
        self.assertEqual(len(self.registry.objects), 2)
    def test_replacement_rejected_before_writes(self):
        self.release.objects[self.files[2].name] = b'other'
        with self.assertRaisesRegex(RuntimeError, 'differs'):
            publisher.publish(self.files, self.release, self.registry)
        self.assertEqual(len(self.release.objects), 1)
        self.assertEqual(self.registry.objects, {})
    def test_registry_conflict_rejected_before_writes(self):
        self.registry.objects[self.files[2].name] = b'other'
        with self.assertRaisesRegex(RuntimeError, 'conflict'):
            publisher.publish(self.files, self.release, self.registry)
        self.assertEqual(self.release.objects, {})

    def test_git_registry_atomic_commit_and_retry(self):
        root = Path(self.tmp.name)
        remote, work = root / 'remote.git', root / 'work'
        def git(*args):
            subprocess.run(['git', *map(str, args)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        git('init', '--bare', remote)
        git('clone', remote, work)
        git('-C', work, 'config', 'user.name', 'Test')
        git('-C', work, 'config', 'user.email', 'test@example.invalid')
        registry = publisher.GitRegistry(work, self.files[2:])
        git('-C', work, 'remote', 'set-url', 'origin', root / 'missing.git')
        with self.assertRaises(subprocess.CalledProcessError):
            publisher.publish(self.files, self.release, registry)
        git('-C', work, 'remote', 'set-url', 'origin', remote)
        publisher.publish(self.files, self.release, registry)
        publisher.publish(self.files, self.release, registry)
        self.assertEqual(subprocess.check_output(['git', '--git-dir', str(remote), 'show', 'main:' + self.files[2].name]), self.files[2].read_bytes())
        self.assertEqual((work / self.files[2].name).read_bytes(), self.files[2].read_bytes())
        self.files[2].write_bytes(b'changed')
        with self.assertRaisesRegex(RuntimeError, 'differs'):
            registry.check()


if __name__ == '__main__':
    unittest.main()
