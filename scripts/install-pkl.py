#!/usr/bin/env python3
"""Install the checked-in Pkl release, verifying the upstream release asset digest."""
import hashlib
import json
import os
from pathlib import Path
import platform
import sys
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
manifest = json.loads((ROOT / 'internal/loader/pkl-toolchain.json').read_text())
version = manifest['version']
if len(sys.argv) == 2 and sys.argv[1] == '--version':
    print(version)
    sys.exit(0)
osname = {'Linux': 'linux', 'Darwin': 'macos', 'Windows': 'windows'}.get(platform.system())
arch = {'x86_64': 'amd64', 'AMD64': 'amd64', 'arm64': 'aarch64', 'aarch64': 'aarch64'}.get(platform.machine())
asset = f'pkl-{osname}-{arch}' + ('.exe' if osname == 'windows' else '')
digest = manifest['sha256'].get(asset)
if not digest or len(sys.argv) != 2:
    sys.exit(f'usage: {sys.argv[0]} DESTINATION (unsupported platform or missing destination: {asset})')
dest = Path(sys.argv[1])
dest.parent.mkdir(parents=True, exist_ok=True)
url = f'https://github.com/apple/pkl/releases/download/{version}/{asset}'
with tempfile.NamedTemporaryFile(dir=dest.parent, delete=False) as f:
    tmp = Path(f.name)
    try:
        with urllib.request.urlopen(url, timeout=120) as response:
            while chunk := response.read(1024 * 1024):
                f.write(chunk)
    except BaseException:
        tmp.unlink(missing_ok=True)
        raise
try:
    actual = hashlib.sha256(tmp.read_bytes()).hexdigest()
    if actual != digest:
        sys.exit(f'Pkl checksum mismatch: {actual} != {digest}')
    tmp.chmod(0o755)
    os.replace(tmp, dest)
finally:
    tmp.unlink(missing_ok=True)
