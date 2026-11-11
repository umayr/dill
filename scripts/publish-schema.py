#!/usr/bin/env python3
"""Publish immutable release assets, then atomically publish registry discovery files."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import zipfile


def validate_schema_artifacts(version, paths):
    archive, archive_checksum, metadata, metadata_checksum = paths
    archive_bytes = archive.read_bytes()
    metadata_bytes = metadata.read_bytes()
    archive_digest = hashlib.sha256(archive_bytes).hexdigest()
    if archive_checksum.read_bytes().strip() != archive_digest.encode():
        raise RuntimeError('archive checksum mismatch')
    if metadata_checksum.read_bytes().strip() != hashlib.sha256(metadata_bytes).hexdigest().encode():
        raise RuntimeError('metadata checksum mismatch')
    data = json.loads(metadata_bytes)
    expected_url = f'https://github.com/umayr/dill/releases/download/v{version}/dill@{version}.zip'
    if (data.get('name') != 'dill' or data.get('version') != version
            or data.get('packageUri') != f'package://pkg.ushd.ca/dill@{version}'
            or data.get('packageZipUrl') != expected_url
            or data.get('packageZipChecksums', {}).get('sha256') != archive_digest):
        raise RuntimeError('schema metadata identity or checksum mismatch')
    with zipfile.ZipFile(archive) as z:
        if z.namelist() != ['dill.pkl']:
            raise RuntimeError('unexpected schema archive contents')
        if z.testzip() is not None:
            raise RuntimeError('corrupt schema archive')


def publish(files, release, registry):
    # Fail before writing anything if any already-published object differs.
    for path in files:
        previous = release.read(path.name)
        if previous is not None and previous != path.read_bytes():
            raise RuntimeError(f'release asset differs: {path.name}')
    registry.check()
    for path in files:
        if release.read(path.name) is None:
            release.write(path)
        if release.read(path.name) != path.read_bytes():
            raise RuntimeError(f'release asset verification failed: {path.name}')
    registry.publish()


class GitHubRelease:
    def __init__(self, tag):
        self.tag = tag
    def assets(self):
        data = subprocess.check_output(['gh', 'release', 'view', self.tag, '--json', 'assets'], text=True)
        return {item['name'] for item in json.loads(data)['assets']}
    def read(self, name):
        if name not in self.assets():
            return None
        with tempfile.TemporaryDirectory() as td:
            subprocess.run(['gh', 'release', 'download', self.tag, '--pattern', name, '--dir', td], check=True)
            return (Path(td) / name).read_bytes()
    def write(self, path):
        # Never use --clobber. A concurrent upload fails closed.
        subprocess.run(['gh', 'release', 'upload', self.tag, str(path)], check=True)


class GitRegistry:
    def __init__(self, directory, files):
        self.directory, self.files = directory, files
    def check(self):
        for path in self.files:
            dest = self.directory / path.name
            if dest.exists() and dest.read_bytes() != path.read_bytes():
                raise RuntimeError(f'registry object differs: {path.name}')
    def publish(self):
        self.check()
        for path in self.files:
            dest = self.directory / path.name
            if not dest.exists():
                dest.write_bytes(path.read_bytes())
        subprocess.run(['git', '-C', str(self.directory), 'add', '--', *[p.name for p in self.files]], check=True)
        result = subprocess.run(['git', '-C', str(self.directory), 'diff', '--cached', '--quiet'])
        if result.returncode == 1:
            subprocess.run(['git', '-C', str(self.directory), 'commit', '-m', 'Publish immutable Pkl metadata'], check=True)
        elif result.returncode != 0:
            raise RuntimeError('cannot inspect registry index')
        # Retry an already-committed but previously failed push too.
        subprocess.run(['git', '-C', str(self.directory), 'push', 'origin', 'HEAD:refs/heads/main'], check=True)


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--tag', required=True)
    p.add_argument('--artifacts', type=Path, required=True)
    p.add_argument('--registry', type=Path, required=True)
    p.add_argument('--binary', action='append', type=Path, default=[])
    args = p.parse_args()
    version = args.tag.removeprefix('v')
    if args.tag != 'v' + version or not version:
        p.error('expected a v-prefixed tag')
    base = 'dill@' + version
    paths = [args.artifacts / (base + suffix) for suffix in ('.zip', '.zip.sha256', '', '.sha256')]
    files = paths + args.binary
    if any(not path.is_file() for path in files):
        raise RuntimeError('missing release artifact')
    validate_schema_artifacts(version, paths)
    publish(files, GitHubRelease(args.tag), GitRegistry(args.registry, paths[2:]))


if __name__ == '__main__':
    main()
