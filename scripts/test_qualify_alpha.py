"""Qualification runner regressions. These mocks never establish engine parity."""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import socket
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("qualify_alpha", Path(__file__).with_name("qualify-alpha.py"))
q = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(q)
COMMIT = "a" * 40


class QualificationTests(unittest.TestCase):
    def test_hash(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "file"
            path.write_bytes(b"abc")
            self.assertEqual(q.sha256(path), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")

    def test_private_auth_overrides_ambient_credentials_in_real_child(self):
        import subprocess
        import sys
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            ambient = root / "ambient"
            for subpath in ("containers/auth.json", ".docker/config.json", "config/containers/auth.json"):
                path = ambient / subpath
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('{"auths":{"registry.invalid":{"auth":"SYNTHETIC_OPERATOR_CREDENTIAL"}}}')
            output = root / "output"
            output.mkdir()
            inherited = dict(HOME=str(ambient), XDG_RUNTIME_DIR=str(ambient), XDG_CONFIG_HOME=str(ambient / "config"),
                             REGISTRY_AUTH_FILE=str(ambient / "containers/auth.json"), DOCKER_CONFIG=str(ambient / ".docker"))
            with patch.dict(os.environ, inherited):
                env = q.private_env(output, Path(sys.executable))
            # A real subprocess examines each documented client fallback path.
            probe = '''import json, os
from pathlib import Path
paths = [Path(os.environ['REGISTRY_AUTH_FILE']), Path(os.environ['XDG_RUNTIME_DIR'])/'containers/auth.json', Path(os.environ['XDG_CONFIG_HOME'])/'containers/auth.json', Path(os.environ['DOCKER_CONFIG'])/'config.json', Path(os.environ['HOME'])/'.docker/config.json']
for path in paths:
 assert json.loads(path.read_text()) == {'auths': {}}, 'ambient credential fallback reached'
print('private-empty-auth-only')
'''
            proc = subprocess.run([sys.executable, "-c", probe], env=env, capture_output=True, timeout=10)
            self.assertEqual(proc.returncode, 0, proc.stderr.decode())
            self.assertEqual(proc.stdout.strip(), b"private-empty-auth-only")
            for key in inherited:
                self.assertNotEqual(env[key], inherited[key])

    def test_sigterm_runner_cleans_real_child_process_group(self):
        import signal
        import subprocess
        import sys
        import time
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            pidfile = root / "child.pid"
            binary = root / "candidate"
            binary.write_text('#!/bin/sh\nsleep 60 &\necho "$!" > "' + str(pidfile) + '"\nwait\n')
            binary.chmod(0o700)
            commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=q.ROOT, text=True).strip()
            command = [sys.executable, str(q.ROOT / "scripts/qualify-alpha.py"),
                       "--expected-commit", commit, "--binary", str(binary), "--binary-sha256", q.sha256(binary),
                       "--pkl", sys.executable, "--output", str(root / "evidence"), "--allow-dirty"]
            proc = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            child = None
            try:
                deadline = time.monotonic() + 10
                while not pidfile.exists() and proc.poll() is None and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertTrue(pidfile.exists(), "candidate never reached running child")
                child = int(pidfile.read_text().strip())
                proc.send_signal(signal.SIGTERM)
                proc.communicate(timeout=10)
                self.assertNotEqual(proc.returncode, 0)
                deadline = time.monotonic() + 2
                while time.monotonic() < deadline:
                    state = subprocess.run(["ps", "-p", str(child), "-o", "stat="], capture_output=True, text=True).stdout.strip()
                    if not state or state.startswith("Z"):
                        break
                    time.sleep(0.02)
                self.assertTrue(not state or state.startswith("Z"), "SIGTERM left a live candidate descendant")
                evidence = json.loads((root / "evidence/evidence.json").read_text())
                self.assertFalse(evidence["baselinePassed"])
                self.assertEqual(evidence["error"], "KeyboardInterrupt")
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.communicate()
                if child is not None:
                    try:
                        os.kill(child, signal.SIGKILL)
                    except ProcessLookupError:
                        pass

    def test_timeout_kills_term_resistant_descendant_with_closed_output(self):
        import shlex
        import signal
        import subprocess
        import sys
        import time
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            pidfile = root / "resistant.pid"
            child_code = ("import os,signal,time; from pathlib import Path; "
                          "signal.signal(signal.SIGTERM,signal.SIG_IGN); os.close(1); os.close(2); "
                          "Path(" + repr(str(pidfile)) + ").write_text(str(os.getpid())); time.sleep(60)")
            leader = root / "leader"
            leader.write_text("#!/bin/sh\n" + shlex.quote(sys.executable) + " -c " + shlex.quote(child_code) + " &\nwait\n")
            leader.chmod(0o700)
            child = None
            try:
                recorder = q.Recorder(q.clean_env())
                _, passed = recorder.run([str(leader)], timeout=1)
                self.assertFalse(passed)
                self.assertEqual(recorder.commands[-1]["failure"], "TimeoutExpired")
                self.assertTrue(pidfile.exists(), "resistant child never reached ready state")
                child = int(pidfile.read_text().strip())
                deadline = time.monotonic() + 2
                while time.monotonic() < deadline:
                    state = subprocess.run(["ps", "-p", str(child), "-o", "stat="], capture_output=True, text=True).stdout.strip()
                    if not state or state.startswith("Z"):
                        break
                    time.sleep(0.02)
                self.assertTrue(not state or state.startswith("Z"), "closed output/leader exit hid a live descendant")
            finally:
                if child is not None:
                    try:
                        os.kill(child, signal.SIGKILL)
                    except ProcessLookupError:
                        pass

    def test_no_ambient_targets_or_credentials(self):
        with patch.dict(os.environ, {key: "untrusted" for key in q.SELECTORS}, clear=True):
            os.environ.update(PATH="/usr/bin", HOME="/credential-home", TOKEN="private", DOCKER_CONFIG="/auth")
            self.assertEqual(q.clean_env(), {"PATH": "/usr/bin"})
            docker = q.engine_env(q.clean_env(), "docker", "unix:///explicit.sock")
            self.assertEqual(docker["DILL_ENGINE_SOCKET"], docker["DOCKER_HOST"])
            self.assertNotIn("PODMAN_SOCKET", docker)
            podman = q.engine_env(q.clean_env(), "podman", "unix:///explicit.sock")
            self.assertEqual(podman["DILL_ENGINE_SOCKET"], podman["CONTAINER_HOST"])
            self.assertEqual(podman["PODMAN_SOCKET"], podman["CONTAINER_HOST"])
            self.assertNotIn("DOCKER_HOST", podman)

    def test_socket_is_explicit_existing_unix_socket(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = str(Path(tmp) / "engine.sock")
            with socket.socket(socket.AF_UNIX) as sock:
                sock.bind(path)
                self.assertEqual(q.local_socket(path), "unix://" + path)
                self.assertEqual(q.local_socket("unix://" + path), "unix://" + path)
            for value in ("tcp://localhost:2375", "unix://host/path", "relative", path + "?x", path + " ", "//host/path"):
                with self.subTest(value=value), self.assertRaises(ValueError):
                    q.local_socket(value)
            file = Path(tmp) / "ordinary"
            file.touch()
            with self.assertRaises(ValueError):
                q.local_socket(str(file))

    def test_test_output_requires_each_case_without_skips(self):
        names = ["TestOne", "TestTwo"]
        data = b"--- PASS: TestOne (0.1s)\n--- PASS: TestTwo (0.2s)\n    --- SKIP: TestTwo/sub (0.0s)\n"
        self.assertEqual(q.test_results(data, names), {"TestOne": "passed", "TestTwo": "failed"})
        self.assertEqual(q.test_results(b"PASS\n", names), {name: "failed" for name in names})
        self.assertEqual(q.test_results(b"--- PASS: TestOneOther (1s)\n", ["TestOne"]), {"TestOne": "failed"})

    def test_recorder_never_stores_raw_output(self):
        recorder = q.Recorder(q.clean_env())
        import sys
        data, passed = recorder.run([sys.executable, "-c", "print('synthetic-sensitive')"])
        self.assertTrue(passed)
        self.assertIn(b"synthetic-sensitive", data)
        self.assertNotIn("synthetic-sensitive", json.dumps({k: v for k, v in recorder.commands[0].items() if k != "argv"}))
        self.assertEqual(recorder.commands[0]["exit"], 0)
        _, passed = recorder.run(["/does-not-exist/dill"])
        self.assertFalse(passed)
        self.assertEqual(recorder.commands[-1]["failure"], "FileNotFoundError")

    def fake_candidate(self, root):
        for name in ("dill", "pkl"):
            path = root / name
            path.write_bytes(b"synthetic executable")
            path.chmod(0o700)
        (root / "dill.pkl").write_text("synthetic schema")
        (root / "internal/loader").mkdir(parents=True)
        (root / "internal/loader/pkl-toolchain.json").write_text(json.dumps({
            "version": "0.31.1", "sha256": {"synthetic": q.sha256(root / "pkl")}}))
        fixtures = root / "test/qualification/v1"
        fixtures.mkdir(parents=True)
        (fixtures / "valid.pkl").write_text("synthetic fixture")
        (fixtures / "cases.json").write_text(json.dumps({"version": "dill.alpha-qualification/1", "cases": [
            {"id": "valid", "mode": "offline", "fixture": "valid.pkl", "exit": 0},
            {"id": "concurrency", "mode": "blocked", "requires": [78], "reason": "unintegrated"}]}))
        return argparse.Namespace(expected_commit=COMMIT, binary=str(root / "dill"),
                                  binary_sha256=q.sha256(root / "dill"), pkl=str(root / "pkl"),
                                  output=str(root / "evidence"), allow_dirty=False,
                                  engine=None, socket=None, disposable=False, operations=False)

    def fake_recorder(self, revision=COMMIT, modified="false", dirty=False, pkl_version="Pkl 0.31.1 (Linux)"):
        class Fake(q.Recorder):
            def run(self, argv, timeout=120, expected=0, env=None):
                args = [str(x) for x in argv]
                self.commands.append({"argv": args, "expectedExit": expected, "exit": expected, "passed": True})
                if args[:3] == ["git", "rev-parse", "HEAD"]:
                    return COMMIT.encode(), True
                if args[:2] == ["git", "status"]:
                    return b" M file" if dirty else b"", True
                if args[:2] == ["go", "version"]:
                    return json.dumps({"Settings": [{"Key": "vcs.revision", "Value": revision},
                                                    {"Key": "vcs.modified", "Value": modified}]}).encode(), True
                if args[-1] == "--version":
                    return pkl_version.encode(), True
                return b"synthetic result", True
        return Fake

    def test_clean_candidate_still_cannot_claim_qualification(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = self.fake_candidate(root)
            with patch.object(q, "ROOT", root), patch.object(q, "FIXTURES", root / "test/qualification/v1"), patch.object(q, "Recorder", self.fake_recorder()):
                self.assertEqual(q.collect(args), 0)
            evidence = json.loads((Path(args.output) / "evidence.json").read_text())
            self.assertTrue(evidence["baselinePassed"])
            self.assertEqual(evidence["qualification"], "incomplete")
            self.assertEqual(evidence["cases"][1]["status"], "blocked")
            self.assertEqual(evidence["engine"], "not-run")
            self.assertEqual(evidence["source"]["commit"], COMMIT)

    def test_bad_provenance_fails_before_validation_or_engine(self):
        for options in ({"revision": "b" * 40}, {"modified": "true"}, {"dirty": True}, {"pkl_version": "Pkl 0.32.2"}):
            with self.subTest(options=options), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = self.fake_candidate(root)
                with patch.object(q, "ROOT", root), patch.object(q, "FIXTURES", root / "test/qualification/v1"), patch.object(q, "Recorder", self.fake_recorder(**options)):
                    self.assertEqual(q.collect(args), 1)
                evidence = json.loads((Path(args.output) / "evidence.json").read_text())
                self.assertFalse(evidence["baselinePassed"])
                self.assertFalse(any(c["argv"][-1] == "validate" for c in evidence["commands"]))

    def test_checksum_and_attestation_reject_before_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = self.fake_candidate(root)
            args.binary_sha256 = "0" * 64
            with self.assertRaises(ValueError):
                q.collect(args)
            self.assertFalse(Path(args.output).exists())
            args.binary_sha256 = q.sha256(args.binary)
            args.engine = "docker"
            with self.assertRaises(ValueError):
                q.collect(args)
            self.assertFalse(Path(args.output).exists())

    def test_real_make_recipe_preserves_single_anchored_regex_argument(self):
        # Execute the real Makefile, but a harmless fake go captures argv and
        # returns immediately. No engine, Go compiler or workload is invoked.
        import subprocess
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            fake = root / "go"
            fake.write_text('#!/bin/sh\nprintf "%s\\n" "$@" > "$CAPTURE"\n')
            fake.chmod(0o700)
            capture = root / "argv"
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ.get("PATH", ""), CAPTURE=str(capture))
            proc = subprocess.run(["make", "integration-test", q.make_test_selector(["TestAlpha", "TestBeta"])],
                                  cwd=q.ROOT, env=env, capture_output=True, timeout=10)
            self.assertEqual(proc.returncode, 0, proc.stderr.decode())
            argv = capture.read_text().splitlines()
            self.assertEqual(argv[argv.index("-run") + 1:], ["^(TestAlpha|TestBeta)$"])
            self.assertIn("./test/integration", argv)
            self.assertIn("-count=1", argv)
        with self.assertRaises(ValueError):
            q.make_test_selector(["TestBad;touch marker"])

    def test_timeout_is_recorded_as_failure(self):
        import sys
        recorder = q.Recorder(q.clean_env())
        _, passed = recorder.run([sys.executable, "-c", "import time; time.sleep(30)"], timeout=0.02)
        self.assertFalse(passed)
        self.assertEqual(recorder.commands[-1]["failure"], "TimeoutExpired")

    def test_mock_engine_wiring_and_missing_or_skipped_test_failure(self):
        # These are runner plumbing tests, explicitly NOT real runtime evidence.
        for test_output, expected in ((b"--- PASS: TestSynthetic (1s)\n", 0),
                                      (b"PASS\n", 1),
                                      (b"--- PASS: TestSynthetic (1s)\n    --- SKIP: TestSynthetic/sub (0s)\n", 1)):
            with self.subTest(output=test_output), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = self.fake_candidate(root)
                manifest_path = root / "test/qualification/v1/cases.json"
                manifest = json.loads(manifest_path.read_text())
                manifest["cases"].append({"id": "runtime", "mode": "engine", "test": "TestSynthetic"})
                manifest_path.write_text(json.dumps(manifest))
                args.engine, args.disposable = "docker", True
                args.socket = str(root / "engine.sock")
                outer = self
                class FakeEngine(self.fake_recorder()):
                    def run(self, argv, timeout=120, expected=0, env=None):
                        if str(argv[0]) == "docker":
                            outer.assertEqual(argv, ["docker", "--host", "unix://" + args.socket, "version", "--format", "{{json .}}"])
                            return b'{"Client":{"Version":"synthetic"},"Server":{"Version":"synthetic"}}', True
                        if str(argv[0]) == "make":
                            outer.assertEqual(env["DILL_BINARY"], str(Path(args.binary).resolve()))
                            outer.assertEqual(env["DILL_ENGINE_SOCKET"], env["DOCKER_HOST"])
                            outer.assertEqual(argv[-1], q.make_test_selector(["TestSynthetic"]))
                            return test_output, True
                        return super().run(argv, timeout, expected, env)
                with socket.socket(socket.AF_UNIX) as sock:
                    sock.bind(args.socket)
                    with patch.object(q, "ROOT", root), patch.object(q, "FIXTURES", root / "test/qualification/v1"), patch.object(q, "Recorder", FakeEngine):
                        self.assertEqual(q.collect(args), expected)
                evidence = json.loads((Path(args.output) / "evidence.json").read_text())
                self.assertEqual(evidence["qualification"], "incomplete")
                self.assertEqual(evidence["baselinePassed"], expected == 0)

    def test_operation_discovery_rejection_precedes_engine_or_make(self):
        for document in ([], {}, {"version": "dill.operation/999"},
                         {"version": "dill.operation/1", "operations": None, "engines": ["docker"]}):
            with self.subTest(document=document), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = self.fake_candidate(root)
                args.engine, args.disposable, args.operations = "docker", True, True
                args.socket = str(root / "engine.sock")
                outer = self
                class InvalidDiscovery(self.fake_recorder()):
                    def run(self, argv, timeout=120, expected=0, env=None):
                        if "operation-capabilities" in argv:
                            return json.dumps(document).encode(), True
                        outer.assertNotIn(str(argv[0]), {"docker", "make"})
                        return super().run(argv, timeout, expected, env)
                with socket.socket(socket.AF_UNIX) as sock:
                    sock.bind(args.socket)
                    with patch.object(q, "ROOT", root), patch.object(q, "FIXTURES", root / "test/qualification/v1"), patch.object(q, "Recorder", InvalidDiscovery):
                        self.assertEqual(q.collect(args), 1)
                evidence = json.loads((Path(args.output) / "evidence.json").read_text())
                self.assertFalse(evidence["baselinePassed"])

    def test_workflow_qualifies_exact_pr_head_and_dispatch_sha(self):
        workflow = (q.ROOT / ".github/workflows/alpha-qualification.yml").read_text()
        self.assertIn("  pull_request:\n", workflow)
        self.assertIn("  workflow_dispatch:\n", workflow)
        self.assertIn("ref: ${{ inputs.candidate_sha || github.event.pull_request.head.sha }}", workflow)
        self.assertNotIn("pull_request_target", workflow)
        self.assertIn("contents: read", workflow)
        self.assertIn("matrix:\n        engine: [docker, podman]", workflow)
        self.assertIn("--engine \"$ENGINE\" --socket \"$socket\" --disposable", workflow)
        self.assertIn('PYTHONDONTWRITEBYTECODE: "1"', workflow)
        for command in ("make build", "make test", "make vet", "make validate", "git diff --check"):
            self.assertIn(command, workflow)
        # Do not upload the private HOME or arbitrary daemon/workload diagnostics.
        artifact_paths = workflow.split("          path: |\n", 1)[1]
        self.assertNotIn("home", artifact_paths)
        self.assertNotIn(".log", artifact_paths)

    def test_engine_manifest_is_existing_bounded_test_names(self):
        manifest = json.loads((q.FIXTURES / "cases.json").read_text())
        ids = [c["id"] for c in manifest["cases"]]
        self.assertEqual(len(ids), len(set(ids)))
        source = "\n".join(p.read_text() for p in (q.ROOT / "test/integration").glob("*_test.go"))
        for case in manifest["cases"]:
            if case["mode"] in {"engine", "operation-engine"}:
                self.assertRegex(case["test"], r"^Test[A-Za-z0-9]+$")
                self.assertIn("func " + case["test"] + "(", source)


if __name__ == "__main__":
    unittest.main()
