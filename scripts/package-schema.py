#!/usr/bin/env python3
"""Package only the public schema, never the checkout or its untracked files."""
import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import zipfile

root = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser()
parser.add_argument('--version', required=True)
parser.add_argument('--output', required=True, type=Path)
args = parser.parse_args()
if not re.fullmatch(r'(?:0|[1-9]\d*)(?:\.(?:0|[1-9]\d*)){2}(?:-[0-9A-Za-z.-]+)?', args.version):
    parser.error('version must be a semantic version without v')
pkl = os.environ.get('PKL', 'pkl')
pinned = subprocess.check_output(['python3', str(root / 'scripts/install-pkl.py'), '--version'], text=True).strip()
if not subprocess.check_output([pkl, '--version'], text=True).startswith('Pkl ' + pinned + ' '):
    raise SystemExit(f'expected Pkl {pinned}')
args.output.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory() as td:
    stage = Path(td)
    project = (root / 'PklProject').read_text()
    project, count = re.subn(r'version = "[^"]*"', f'version = "{args.version}"', project, count=1)
    if count != 1:
        raise SystemExit('cannot stamp PklProject version')
    (stage / 'PklProject').write_text(project)
    shutil.copyfile(root / 'dill.pkl', stage / 'dill.pkl')
    subprocess.run([pkl, 'project', 'package', str(stage), '--output-path', str(args.output.resolve()), '--skip-publish-check'], check=True)
archive = args.output / f'dill@{args.version}.zip'
with zipfile.ZipFile(archive) as z:
    if z.namelist() != ['dill.pkl']:
        raise SystemExit(f'unexpected archive contents: {z.namelist()}')
