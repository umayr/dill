#!/usr/bin/env python3
"""Resolve and evaluate a published schema from HTTPS using an empty Pkl cache."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import urllib.request

if len(sys.argv) != 2 or not re.fullmatch(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?', sys.argv[1]):
    sys.exit('usage: smoke-schema.py VERSION')
version = sys.argv[1]
base = f'https://pkg.ushd.ca/dill@{version}'
def fetch(url):
    with urllib.request.urlopen(url, timeout=60) as r:
        if not r.url.startswith('https://'):
            raise RuntimeError('non-HTTPS redirect')
        return r.read()
metadata = fetch(base)
if hashlib.sha256(metadata).hexdigest() != fetch(base + '.sha256').decode().strip():
    raise SystemExit('metadata checksum mismatch')
data = json.loads(metadata)
expected = f'https://github.com/umayr/dill/releases/download/v{version}/dill@{version}.zip'
if data['packageUri'] != f'package://pkg.ushd.ca/dill@{version}' or data['packageZipUrl'] != expected:
    raise SystemExit('unexpected package identity or archive URL')
archive_digest = hashlib.sha256(fetch(expected)).hexdigest()
if archive_digest != data['packageZipChecksums']['sha256'] or archive_digest != fetch(expected + '.sha256').decode().strip():
    raise SystemExit('archive checksum mismatch')
with tempfile.TemporaryDirectory() as td:
    module = Path(td) / 'smoke.pkl'
    module.write_text(f'''amends "package://pkg.ushd.ca/dill@{version}#/dill.pkl"
config {{
  name = "smoke"
  engine = "docker"
  services {{ ["web"] {{ image = "nginx:alpine" }} }}
}}
''')
    subprocess.run([os.environ.get('PKL', 'pkl'), 'eval', '--cache-dir', str(Path(td) / 'cache'), str(module)], check=True, stdout=subprocess.DEVNULL)
print(f'PASS: {base}: HTTPS metadata, archive, checksums and clean-cache evaluation')
