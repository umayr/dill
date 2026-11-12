"""Static CI composition regressions; never execute daemon/setup scripts."""

from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parent.parent


class AlphaRuntimeProvisioningTests(unittest.TestCase):
    def test_pinned_source_runtime_build(self):
        script = (ROOT / 'scripts/ci-build-podman-alpha.sh').read_text()
        self.assertIn('readonly crun_version=1.26', script)
        self.assertIn('readonly crun_source_commit=3241e671f92c33b0c003cd7de319e4f32add6231', script)
        self.assertIn('readonly source_commit=e71ec6f1d94d2d97fb3afe08aae0d8adaf8bddf0', script)
        self.assertIn('test "$(git -C "$crun_source_dir" rev-parse HEAD)" = "$crun_source_commit"', script)
        self.assertIn('submodule update --init --recursive', script)
        self.assertIn("grep -Eq '^[-+U]'", script)
        self.assertIn('./configure --prefix=/usr --disable-shared --enable-seccomp --enable-systemd', script)
        self.assertIn('\n  make -j2\n', script)
        self.assertIn('sudo install -o root -g root -m 0755 "$crun_source_dir/crun" /usr/bin/crun', script)
        self.assertIn('sha256sum /usr/bin/crun', script)

    def test_actual_server_runtime_selection_and_service_contract(self):
        script = (ROOT / 'scripts/ci-alpha-secrets.sh').read_text()
        for field in ('Name', 'Path', 'Version'):
            self.assertIn('info --format \'{{.Host.OCIRuntime.' + field + '}}\'', script)
        self.assertIn('test "$alpha_runtime_name" = crun', script)
        self.assertIn('test "$alpha_runtime_path" = /usr/bin/crun', script)
        self.assertIn("= 'crun version 1.26'", script)
        self.assertIn("printf '[containers]\\nuserns = \"host\"\\n'", script)
        self.assertNotIn('[engine]', script)
        self.assertEqual(script.count('/usr/bin/podman system service --time=0 "unix://$socket"'), 2)
        # PATH repair must not change dedicated Docker bridge/firewall policy.
        self.assertIn('--bridge=none --iptables=false --ip6tables=false --ip-forward=false --ip-masq=false', script)

    def test_admin_paths_and_sourced_wrapper_interface(self):
        script = (ROOT / 'scripts/ci-alpha-secrets.sh').read_text()
        path = re.search(r'^alpha_test_path=(.+)$', script, re.M).group(1).split(':')
        self.assertEqual(path[0], '/usr/bin')
        self.assertTrue({'/usr/sbin', '/sbin'}.issubset(path))
        self.assertEqual(script.count('env -i PATH="$alpha_test_path"'), 3)
        self.assertIn('common=(GITHUB_ACTIONS=true PATH="$alpha_test_path"', script)
        self.assertIn('command -v "$tool"', script)
        self.assertIn('iptables --version; ip6tables --version; ip -Version', script)
        for assignment in ('base=/srv/dill-alpha-$mode', 'socket=$base/engine.sock', "pid=''", 'operator=0; group=0', 'engine=docker', 'engine=podman'):
            self.assertIn(assignment, script)
        self.assertRegex(script, r'(?m)^run_operator\(\)')
        self.assertIn('trap cleanup EXIT', script)


if __name__ == '__main__':
    unittest.main()
