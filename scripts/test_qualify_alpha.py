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
    def synthetic_complete_gate_inputs(self):
        # Pure predicate fixtures only. Never write these as qualification output.
        from copy import deepcopy
        manifest = deepcopy(json.loads((q.FIXTURES / "cases.json").read_text()))
        endpoint, digest = "unix:///synthetic/disposable.sock", "b" * 64
        results = []
        for case in manifest["cases"]:
            case["mode"], kind = q.REQUIRED_CASE_EVIDENCE[case["id"]]
            results.append({"id": case["id"], "status": "passed", "evidenceKind": kind,
                            "candidateCommit": COMMIT, "binarySha256": digest, "target": endpoint,
                            **({"tests": list(q.REQUIRED_RUNTIME_TESTS[case["id"]])} if case["mode"] != "offline" else {})})
        evidence = {"fixtureVersion": manifest["version"], "cases": results,
                    "source": {"commit": COMMIT, "dirty": False},
                    "binary": {"buildRevision": COMMIT, "buildModified": "false", "sha256": digest, "goOS": "linux"},
                    "schema": {"sha256": "c" * 64}, "pkl": {"sha256": "d" * 64},
                    "checksumsAfter": {"binary": digest, "schema": "c" * 64, "pkl": "d" * 64},
                    "integrityVerified": True, "engine": "docker", "disposableAttested": True,
                    "target": endpoint, "engineVersion": {"Server": {"Version": "synthetic"}},
                    "baselinePassed": True, "commands": [{"passed": True}], "qualifiedRequested": True,
                    "qualifiedProvisioning": {"source": q.PROVISIONING_SOURCE, "layout": "native-local-docker-v1",
                        "expectedIdentityDigest": "sha256:" + "e" * 64, "privateStateRootVerified": True},
                    "dillArtifactSupply": {"status": "verified", "candidateCommit": COMMIT, "binarySha256": digest,
                        "manifestSha256": "f" * 64, "archiveSha256": "f" * 64}}
        return evidence, manifest

    def test_final_gate_is_not_permanently_incomplete(self):
        evidence, manifest = self.synthetic_complete_gate_inputs()
        self.assertEqual(set(c["id"] for c in manifest["cases"]), q.FINAL_REQUIRED_CASES)
        result = q.final_acceptance(evidence, manifest)
        self.assertTrue(result["complete"], result["reasons"])
        self.assertEqual(result["reasons"], [])

    def test_final_gate_missing_failed_or_unqualified_evidence_never_passes(self):
        mutations = [
            lambda e, m: e["cases"].pop(),
            lambda e, m: m["cases"].pop(),
            lambda e, m: e["cases"].append(e["cases"][0]),
            lambda e, m: e["cases"][0].update(status="blocked"),
            lambda e, m: e["cases"][0].update(status="not-run"),
            lambda e, m: e["cases"][0].update(status="skipped"),
            lambda e, m: e["cases"][0].update(status="failed"),
            lambda e, m: e["cases"][0].pop("evidenceKind"),
            lambda e, m: e["cases"][-1].update(evidenceKind="real-engine"),
            lambda e, m: e["cases"][-1].update(candidateCommit="e" * 40),
            lambda e, m: e["cases"][-1].update(binarySha256="e" * 64),
            lambda e, m: e["cases"][-1].update(target="unix:///other.sock"),
            lambda e, m: m["cases"][-1].update(mode="blocked"),
            lambda e, m: m["cases"][-1].update(mode="profile-engine"),
            lambda e, m: e["cases"][-1].update(evidenceKind="real-profile-engine"),
            lambda e, m: e["source"].update(dirty=True),
            lambda e, m: e["binary"].update(buildModified="true"),
            lambda e, m: e["binary"].update(goOS="darwin"),
            lambda e, m: e.update(engine="podman"),
            lambda e, m: e.update(disposableAttested=False),
            lambda e, m: e.pop("engineVersion"),
            lambda e, m: e.pop("integrityVerified"),
            lambda e, m: e["checksumsAfter"].update(schema="e" * 64),
            lambda e, m: e.update(baselinePassed=False),
            lambda e, m: e["commands"].clear(),
            lambda e, m: e["commands"][0].update(passed=False),
            lambda e, m: m["qualifiedProfile"]["limitations"].pop(),
            lambda e, m: e.update(qualifiedRequested=False),
            lambda e, m: e.pop("qualifiedProvisioning"),
            lambda e, m: e["qualifiedProvisioning"].update(source="self-derived-endpoint"),
            lambda e, m: e["qualifiedProvisioning"].update(expectedIdentityDigest="raw-daemon-id"),
            lambda e, m: e["qualifiedProvisioning"].update(privateStateRootVerified=False),
            lambda e, m: e.pop("dillArtifactSupply"),
            lambda e, m: e["dillArtifactSupply"].update(status="not-run"),
            lambda e, m: e["dillArtifactSupply"].update(candidateCommit="e" * 40),
            lambda e, m: e["dillArtifactSupply"].update(binarySha256="e" * 64),
            lambda e, m: e["dillArtifactSupply"].pop("archiveSha256"),
            lambda e, m: m["cases"][-1].update(test="TestAlphaQualifiedProcessHelpers"),
            lambda e, m: e["cases"][-1].update(tests=["TestAlphaQualifiedProcessHelpers"]),
            lambda e, m: m["cases"][0].update(fixture="invalid-graph.pkl"),
        ]
        for index, mutate in enumerate(mutations):
            with self.subTest(index=index):
                evidence, manifest = self.synthetic_complete_gate_inputs()
                mutate(evidence, manifest)
                self.assertFalse(q.final_acceptance(evidence, manifest)["complete"])

    def test_each_runtime_case_rejects_offline_downgrade_and_unknown_mode(self):
        offline = {case for case, (mode, _) in q.REQUIRED_CASE_EVIDENCE.items() if mode == "offline"}
        self.assertEqual(offline, {"offline-valid", "offline-invalid-graph"})
        for case_id in q.FINAL_REQUIRED_CASES - offline:
            for mode, kind in (("offline", "offline"), ("unrecognized-mode", "real-engine")):
                with self.subTest(case=case_id, mode=mode):
                    evidence, manifest = self.synthetic_complete_gate_inputs()
                    next(case for case in manifest["cases"] if case["id"] == case_id)["mode"] = mode
                    next(case for case in evidence["cases"] if case["id"] == case_id)["evidenceKind"] = kind
                    result = q.final_acceptance(evidence, manifest)
                    self.assertFalse(result["complete"])
                    self.assertIn("required-case-not-implemented", result["reasons"])
                    if mode == "offline":
                        self.assertIn("required-case-runtime-evidence-missing", result["reasons"])

    def test_no_prune_requires_positive_isolated_cli_not_qualified_rejection(self):
        self.assertEqual(q.REQUIRED_CASE_EVIDENCE["no-prune"], ("engine", "real-engine"))
        evidence, manifest = self.synthetic_complete_gate_inputs()
        definition = next(case for case in manifest["cases"] if case["id"] == "no-prune")
        self.assertEqual(definition["test"], "TestAlphaNoPruneBothEngines")
        definition["mode"] = "qualified-engine"
        next(case for case in evidence["cases"] if case["id"] == "no-prune")["evidenceKind"] = "real-qualified-engine"
        self.assertFalse(q.final_acceptance(evidence, manifest)["complete"])

    def test_prepared_profile_observation_cannot_clear_required_combined_case(self):
        evidence, manifest = self.synthetic_complete_gate_inputs()
        observed = dict(evidence["cases"][-1])
        evidence["cases"][-1].update(status="blocked")
        evidence["preparedProfiles"] = [observed]
        self.assertFalse(q.final_acceptance(evidence, manifest)["complete"])

    def test_require_complete_rejects_green_incomplete_baseline(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = self.fake_candidate(root)
            args.require_complete = True
            with patch.object(q, "ROOT", root), patch.object(q, "FIXTURES", root / "test/qualification/v1"), patch.object(q, "Recorder", self.fake_recorder()):
                self.assertEqual(q.collect(args), 1)
            evidence = json.loads((Path(args.output) / "evidence.json").read_text())
            self.assertTrue(evidence["baselinePassed"])
            self.assertEqual(evidence["qualification"], "incomplete")
            self.assertFalse(evidence["acceptance"]["complete"])

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

    def test_cleanup_error_cannot_mask_primary_cancellation(self):
        from unittest.mock import MagicMock
        for cleanup_error in (PermissionError(), ValueError(), RuntimeError(), q.subprocess.TimeoutExpired("synthetic", 1), SystemExit(0), KeyboardInterrupt()):
            with self.subTest(cleanup=type(cleanup_error).__name__):
                primary = KeyboardInterrupt()
                proc = MagicMock()
                proc.communicate.side_effect = primary
                recorder = q.Recorder({})
                with patch.object(q.subprocess, "Popen", return_value=proc), patch.object(q, "stop_process_group", side_effect=cleanup_error):
                    with self.assertRaises(KeyboardInterrupt) as caught:
                        recorder.run(["synthetic-interrupted-candidate"])
                self.assertIs(caught.exception, primary)
                self.assertFalse(recorder.commands[0]["passed"])
                self.assertEqual(recorder.commands[0]["cleanupFailure"], type(cleanup_error).__name__)

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
                self.assertEqual(evidence["error"], "KeyboardInterrupt", json.dumps(evidence["commands"]))
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

    def test_qualified_inputs_reject_untrusted_layout_before_any_command(self):
        changes = [{"native_layout_provisioned": False}, {"provisioning_source": "self-derived"},
                   {"coordination_daemon_id": "id\\nINJECT=1"}, {"coordination_daemon_id": None},
                   {"coordination_state_root": "relative"}, {"engine": "podman"}, {"operations": False}]
        for change in changes:
            with self.subTest(change=change), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = self.fake_candidate(root)
                state = root / "state"
                state.mkdir(mode=0o700)
                args.engine, args.disposable, args.operations, args.qualified = "docker", True, True, True
                args.coordination_daemon_id, args.coordination_state_root = "synthetic-daemon-id", str(state)
                args.native_layout_provisioned, args.provisioning_source = True, q.PROVISIONING_SOURCE
                args.socket = "unix:///synthetic-unopened.sock"
                for key, value in change.items():
                    setattr(args, key, value)
                with patch.object(q.sys, "platform", "linux"), patch.object(q, "Recorder", side_effect=AssertionError("must reject before execution")):
                    with self.assertRaises((ValueError, OSError)):
                        q.collect(args)
                self.assertFalse(Path(args.output).exists())

    def test_qualified_plumbing_is_mock_only_and_passes_explicit_provisioning(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            args = self.fake_candidate(root)
            state = root / "state"
            state.mkdir(mode=0o700)
            args.engine, args.disposable, args.operations, args.qualified = "docker", True, True, True
            args.coordination_daemon_id, args.coordination_state_root = "synthetic-daemon-id", str(state)
            args.native_layout_provisioned, args.provisioning_source = True, q.PROVISIONING_SOURCE
            path = root / "test/qualification/v1/cases.json"
            path.write_text(json.dumps({"version": "dill.alpha-qualification/1", "cases": [
                {"id": "mock-only", "mode": "qualified-engine", "test": "TestNativePrimary", "additionalTests": ["TestNativeExtra"], "requires": [78], "reason": "mock"}]}))
            observed = []
            class NativeFake(self.fake_recorder()):
                def run(self, argv, timeout=120, expected=0, env=None):
                    if "operation-capabilities" in argv:
                        return json.dumps({"version": "dill.operation/1", "engines": ["docker", "podman"],
                            "features": ["explicitLocalSelector", "attended", "redactedSummary", "conservativeEffects", "optInQualifiedLocalCoordination"],
                            "operations": ["validate", "plan", "diff", "up", "down", "stop", "start", "restart", "rm", "teardown"],
                            "qualifiedCoordinationOperations": ["up", "down", "stop", "start", "restart", "rm"],
                            "qualifiedCoordinationEngines": ["docker"], "qualifiedCoordinationLayouts": ["native-local-docker-v1"]}).encode(), True
                    if argv[0] == "docker":
                        return b'{"Server":{"Version":"synthetic"}}', True
                    if argv[0] == "make":
                        observed.append((argv, env.copy()))
                        return b"--- PASS: TestNativePrimary (0s)\n--- PASS: TestNativeExtra (0s)\n", True
                    return super().run(argv, timeout, expected, env)
            sock = socket.socket(socket.AF_UNIX)
            try:
                endpoint = root / "s"
                sock.bind(str(endpoint))
                args.socket = str(endpoint)
                with patch.object(q.sys, "platform", "linux"), patch.object(q, "ROOT", root), patch.object(q, "FIXTURES", root / "test/qualification/v1"), patch.object(q, "Recorder", NativeFake):
                    self.assertEqual(q.collect(args), 0)
            finally:
                sock.close()
            argv, env = observed[0]
            self.assertEqual(argv[-1], q.make_test_selector(["TestNativePrimary", "TestNativeExtra"]))
            self.assertEqual(env["DILL_TEST_ALPHA_DAEMON_ID"], args.coordination_daemon_id)
            self.assertEqual(env["DILL_TEST_ALPHA_STATE_ROOT"], str(state))
            self.assertEqual(env["DILL_TEST_ALPHA_NATIVE_LAYOUT"], "native-local-docker-v1")
            self.assertEqual(env["DILL_TEST_ALPHA_COORDINATION"], "1")
            evidence = json.loads((Path(args.output) / "evidence.json").read_text())
            self.assertEqual(evidence["cases"][0]["evidenceKind"], "real-qualified-engine")
            self.assertEqual(evidence["cases"][0]["tests"], ["TestNativePrimary", "TestNativeExtra"])
            self.assertEqual(evidence["qualification"], "incomplete")  # Synthetic, not runtime qualification.

    def test_copied_public_archive_hashes_and_privacy_pure_only(self):
        # Byte/archive validation only: no actual executable, daemon or Brine app.
        import shutil
        import tarfile
        for variant in ("valid", "changed", "private", "symlink", "wrong-commit"):
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = self.fake_candidate(root)
                (root / "docs").mkdir()
                (root / "docs/alpha-candidate-handoff.md").write_text("synthetic public guide")
                (root / "internal/operation/testdata").mkdir(parents=True)
                (root / "internal/operation/testdata/public.json").write_text("{}")
                output = root / "out"
                bundle = output / "handoff"
                bundle.mkdir(parents=True)
                identity = "sha256:" + "e" * 64
                with patch.object(q, "ROOT", root), patch.object(q, "FIXTURES", root / "test/qualification/v1"):
                    sources = q.handoff_sources(Path(args.binary), Path(args.pkl))
                    digests = {}
                    for name, source in sources.items():
                        target = bundle / name
                        target.parent.mkdir(parents=True, exist_ok=True)
                        shutil.copyfile(source, target)
                        digests[name] = q.sha256(source)
                    document = {"version": "dill.alpha-handoff/1", "status": "verified", "candidateCommit": COMMIT,
                                "binarySha256": digests["bin/dill"], "fixtureVersion": "dill.alpha-qualification/1", "files": digests,
                                "caller": {"contract": "dill.operation/1", "source": "brine", "outcome": "succeeded", "effects": "complete", "qualifiedIdentityDigest": identity},
                                "downstreamBrineAcceptance": "not-run"}
                    if variant == "wrong-commit":
                        document["candidateCommit"] = "f" * 40
                    (bundle / "handoff.json").write_text(json.dumps(document))
                    if variant == "changed":
                        (bundle / "bin/dill").write_text("changed")
                    if variant == "private":
                        (bundle / "identity.key").write_text("synthetic private sentinel")
                    if variant == "symlink":
                        (bundle / "dill.pkl").unlink()
                        (bundle / "dill.pkl").symlink_to(root / "dill.pkl")
                    if variant != "valid":
                        with self.assertRaises(ValueError):
                            q.verify_handoff(output, Path(args.binary), Path(args.pkl), COMMIT, identity)
                        self.assertFalse((output / "handoff.tar.gz").exists())
                    else:
                        result = q.verify_handoff(output, Path(args.binary), Path(args.pkl), COMMIT, identity)
                        self.assertEqual(result["archiveSha256"], q.sha256(output / "handoff.tar.gz"))
                        with tarfile.open(output / "handoff.tar.gz") as archive:
                            self.assertEqual(set(archive.getnames()), set(digests) | {"handoff.json"})
                            self.assertTrue(all(member.isfile() for member in archive.getmembers()))

    def test_exact_test_bindings_cannot_replace_positive_no_prune_with_rejection(self):
        evidence, manifest = self.synthetic_complete_gate_inputs()
        definition = next(c for c in manifest["cases"] if c["id"] == "no-prune")
        definition["test"] = "TestAlphaQualifiedOrphanRejection"  # Keep the approved ordinary engine MODE.
        result = q.final_acceptance(evidence, manifest)
        self.assertFalse(result["complete"])
        self.assertIn("required-runtime-test-bindings-mismatch", result["reasons"])
        self.assertEqual(set(q.REQUIRED_RUNTIME_TESTS), q.FINAL_REQUIRED_CASES - set(q.REQUIRED_OFFLINE_FIXTURES))

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
                                  engine=None, socket=None, disposable=False, operations=False, profiles=False, require_complete=False,
                                  qualified=False, coordination_daemon_id=None, coordination_state_root=None,
                                  native_layout_provisioned=False, provisioning_source=None)

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
                                                    {"Key": "vcs.modified", "Value": modified},
                                                    {"Key": "GOOS", "Value": "linux"}]}).encode(), True
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

    def test_profile_selection_and_evidence_are_not_qualified_coordination(self):
        # Mock plumbing only; no engine evidence is produced by this unit test.
        for engine in ("docker", "podman"):
            with self.subTest(engine=engine), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                args = self.fake_candidate(root)
                args.engine, args.disposable, args.operations, args.profiles = engine, True, True, True
                args.socket = str(root / "engine.sock")
                path = root / "test/qualification/v1/cases.json"
                manifest = json.loads(path.read_text())
                manifest["preparedProfiles"] = [
                    {"id": "guardrail", "mode": "guardrail-engine", "test": "TestGuardrail"},
                    {"id": "profile", "mode": "profile-engine", "test": "TestDockerProfile"}]
                path.write_text(json.dumps(manifest))
                outer = self
                class FakeProfile(self.fake_recorder()):
                    def run(self, argv, timeout=120, expected=0, env=None):
                        if "operation-capabilities" in argv:
                            return json.dumps({"version": "dill.operation/1", "engines": [engine],
                                "features": ["explicitLocalSelector", "attended", "redactedSummary", "conservativeEffects"],
                                "operations": ["validate", "plan", "diff", "up", "down", "stop", "start", "restart", "rm", "teardown"]}).encode(), True
                        if str(argv[0]) == engine:
                            return b'{"Server":{"Version":"synthetic"}}', True
                        if str(argv[0]) == "make":
                            names = ["TestGuardrail"] + (["TestDockerProfile"] if engine == "docker" else [])
                            outer.assertEqual(argv[-1], q.make_test_selector(names))
                            outer.assertEqual(env["DILL_TEST_ALPHA_GUARDRAILS"], "1")
                            return "".join("--- PASS: " + name + " (1s)\n" for name in names).encode(), True
                        return super().run(argv, timeout, expected, env)
                with socket.socket(socket.AF_UNIX) as sock:
                    sock.bind(args.socket)
                    with patch.object(q, "ROOT", root), patch.object(q, "FIXTURES", root / "test/qualification/v1"), patch.object(q, "Recorder", FakeProfile):
                        self.assertEqual(q.collect(args), 0)
                evidence = json.loads((Path(args.output) / "evidence.json").read_text())
                results = {case["id"]: case for case in evidence["preparedProfiles"]}
                self.assertFalse({"guardrail", "profile"} & {case["id"] for case in evidence["cases"]})
                self.assertEqual(results["guardrail"]["evidenceKind"], "real-engine")
                if engine == "docker":
                    self.assertEqual(results["profile"]["evidenceKind"], "real-profile-engine")
                else:
                    self.assertEqual(results["profile"]["status"], "not-run")
                self.assertEqual(evidence["qualification"], "incomplete")

    def test_negotiated_qualified_discovery_is_closed_and_optional(self):
        baseline = {"version": "dill.operation/1", "engines": ["docker", "podman"],
                    "features": ["explicitLocalSelector", "attended", "redactedSummary", "conservativeEffects"],
                    "operations": ["validate", "plan", "diff", "up", "down", "stop", "start", "restart", "rm", "teardown"]}
        qualified = json.loads(json.dumps(baseline))
        qualified["features"].append("optInQualifiedLocalCoordination")
        qualified.update(qualifiedCoordinationOperations=["up", "down", "stop", "start", "restart", "rm"],
                         qualifiedCoordinationEngines=["docker"], qualifiedCoordinationLayouts=["native-local-docker-v1"])
        unsupported_host = dict(baseline, qualifiedCoordinationOperations=[], qualifiedCoordinationEngines=[], qualifiedCoordinationLayouts=[])
        with self.assertRaises(ValueError):
            q.operation_capabilities(unsupported_host, "docker")
        for document in (baseline, qualified):
            for engine in ("docker", "podman"):
                self.assertEqual(q.operation_capabilities(document, engine), document)
        mutations = [
            lambda doc: doc.update(unnegotiatedFutureField=True),
            lambda doc: doc.update(features=baseline["features"]),
            lambda doc: doc.update(qualifiedCoordinationOperations=["teardown"]),
            lambda doc: doc.update(qualifiedCoordinationEngines=["podman"]),
            lambda doc: doc.update(qualifiedCoordinationLayouts=["containerized"]),
        ]
        for field in qualified:
            mutations.extend([lambda doc, field=field: doc.pop(field),
                              lambda doc, field=field: doc.update({field: None})])
        for field in ("operations", "engines", "features", "qualifiedCoordinationOperations", "qualifiedCoordinationEngines", "qualifiedCoordinationLayouts"):
            mutations.extend([lambda doc, field=field: doc[field].append(doc[field][0]),
                              lambda doc, field=field: doc[field].append(42)])
        for index, mutate in enumerate(mutations):
            with self.subTest(mutation=index):
                document = json.loads(json.dumps(qualified))
                mutate(document)
                with self.assertRaises(ValueError):
                    q.operation_capabilities(document, "docker")

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
        for case in manifest["cases"] + manifest.get("preparedProfiles", []):
            if case["mode"] in {"engine", "operation-engine", "guardrail-engine", "profile-engine", "qualified-engine"}:
                self.assertRegex(case["test"], r"^Test[A-Za-z0-9]+$")
                self.assertIn("func " + case["test"] + "(", source)


if __name__ == "__main__":
    unittest.main()
