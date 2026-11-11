import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parent.parent


class PackageTests(unittest.TestCase):
    def test_allowlist_reproducibility_and_evaluation(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td) / 'checkout'
            (root / 'scripts').mkdir(parents=True)
            (root / 'internal/loader').mkdir(parents=True)
            for file in ('scripts/package-schema.py', 'scripts/install-pkl.py', 'PklProject', 'dill.pkl', 'internal/loader/pkl-toolchain.json'):
                shutil.copyfile(ROOT / file, root / file)
            (root / 'secret.key').write_text('synthetic secret')
            (root / '.worktree').mkdir()
            (root / '.worktree/secret').write_text('synthetic secret')
            for name in ('one', 'two'):
                subprocess.run(['python3', str(root / 'scripts/package-schema.py'), '--version', '9.9.9', '--output', str(root / name)], check=True)
            for suffix in ('', '.sha256', '.zip', '.zip.sha256'):
                self.assertEqual((root / 'one' / ('dill@9.9.9' + suffix)).read_bytes(), (root / 'two' / ('dill@9.9.9' + suffix)).read_bytes())
            spec = importlib.util.spec_from_file_location('publisher', ROOT / 'scripts/publish-schema.py')
            publisher = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(publisher)
            publisher.validate_schema_artifacts('9.9.9', [root / 'one' / ('dill@9.9.9' + suffix) for suffix in ('.zip', '.zip.sha256', '', '.sha256')])
            archive = root / 'one/dill@9.9.9.zip'
            with zipfile.ZipFile(archive) as z:
                self.assertEqual(z.namelist(), ['dill.pkl'])
            module = root / 'test.pkl'
            module.write_text('amends "modulepath:/dill.pkl"\nconfig { name = "smoke"; engine = "docker"; services { ["web"] { image = "nginx" } } }\n')
            subprocess.run([os.environ.get('PKL', 'pkl'), 'eval', '--module-path', str(archive), str(module)], check=True, stdout=subprocess.DEVNULL)


if __name__ == '__main__':
    unittest.main()
