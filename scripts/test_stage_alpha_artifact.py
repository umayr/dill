"""Pure closed collector tests. No candidate, Pkl, Docker or Brine execution."""
import importlib.util
import json
import hashlib
import argparse
import socket
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("stage_alpha_artifact", Path(__file__).with_name("stage-alpha-artifact.py"))
s = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(s)
COMMIT = "a" * 40


class CollectorTests(unittest.TestCase):
    def fixture(self, root, archive=False):
        for name in s.q.PUBLIC_HANDOFF_FILES:
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"synthetic public bytes: " + name.encode())
        catalog = Path(__file__).resolve().parent.parent / "test/qualification/v1/cases.json"
        (root / "test/qualification/v1/cases.json").write_bytes(catalog.read_bytes())
        (root / "internal/loader/pkl-toolchain.json").write_text(json.dumps({"version": "0.31.1", "sha256": {"synthetic": s.q.sha256(root / "bin/pkl")}}))
        output = root / "bin/alpha-evidence-docker"
        output.mkdir()
        (output / "home").mkdir()
        (output / "home/identity.key").write_text("PRIVATE-SENTINEL-NOT-FOR-UPLOAD")
        evidence = {"version": "dill.alpha-evidence/2", "engine": "docker", "qualification": "incomplete",
                    "source": {"commit": COMMIT, "dirty": False, "statusSha256": hashlib.sha256(b"").hexdigest()},
                    "binary": {"sha256": s.q.sha256(root / "bin/dill"), "version": "synthetic", "goVersion": "go1.25.0", "goOS": "linux", "goArch": "amd64", "buildRevision": COMMIT, "buildModified": "false"},
                    "qualifiedProvisioning": {"source": s.q.PROVISIONING_SOURCE, "layout": "native-local-docker-v1", "expectedIdentityDigest": "sha256:" + "b" * 64, "privateStateRootVerified": True}}
        if archive:
            bundle = output / "handoff"
            sources = s.q.handoff_sources(root / "bin/dill", root / "bin/pkl")
            hashes = {}
            for name, source in sources.items():
                target = bundle / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(source, target)
                hashes[name] = s.q.sha256(source)
            manifest = {"version": "dill.alpha-handoff/2", "status": "verified", "candidateCommit": COMMIT,
                        "binarySha256": hashes["bin/dill"], "fixtureVersion": "dill.alpha-qualification/2", "files": hashes,
                        "caller": {"contract": "dill.operation/1", "source": "brine", "outcome": "succeeded", "effects": "complete",
                                   "qualifiedIdentityDigest": evidence["qualifiedProvisioning"]["expectedIdentityDigest"]},
                        "downstreamBrineAcceptance": "not-run"}
            (bundle / "handoff.json").write_text(json.dumps(manifest))
            evidence["dillArtifactSupply"] = s.q.verify_handoff(output, root / "bin/dill", root / "bin/pkl", COMMIT,
                                                               evidence["qualifiedProvisioning"]["expectedIdentityDigest"])
        (output / "evidence.json").write_text(json.dumps(evidence))
        return output, evidence

    def test_private_home_is_never_read_or_copied(self):
        for archive in (False, True):
            with self.subTest(archive=archive), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                    output, _ = self.fixture(root, archive)
                    with patch.object(s, "public_sources", return_value={"bin/dill": root / "bin/dill"}):
                        names = s.stage("docker", COMMIT, root / "bin/pkl", root / "sealed")
                    self.assertEqual(len(names), 3 if archive else 2)
                    self.assertTrue((output / "home/identity.key").exists())
                    for name in names:
                        self.assertNotIn(b"PRIVATE-SENTINEL", (root / "sealed" / name).read_bytes())
                        self.assertFalse((root / "sealed" / name).is_symlink())

    def test_extras_links_wrong_binding_and_missing_supply_fail_before_staging(self):
        for variant in ("extra-output", "extra-bundle", "link-source", "wrong-commit", "complete-no-archive", "extra-source"):
            with self.subTest(variant=variant), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                    output, evidence = self.fixture(root)
                    if variant == "extra-output":
                        (output / "engine.log").write_text("PRIVATE")
                    elif variant == "extra-bundle":
                        (output / "handoff").mkdir()
                        (output / "handoff/identity.key").write_text("PRIVATE")
                    elif variant == "link-source":
                        (root / "bin/dill").unlink()
                        (root / "bin/dill").symlink_to(output / "home/identity.key")
                    elif variant == "wrong-commit":
                        evidence["source"]["commit"] = "c" * 40
                    elif variant == "complete-no-archive":
                        evidence["qualification"] = "complete"
                    elif variant == "extra-source":
                        (root / "internal/preflight/testdata/private.json").write_text("PRIVATE")
                    (output / "evidence.json").write_text(json.dumps(evidence))
                    # Frozen source-set validation remains real for extra-source.
                    sources = {"bin/dill": root / "bin/dill"}
                    with patch.object(s, "public_sources", side_effect=(lambda _: s.q.handoff_sources(root / "bin/dill", root / "bin/pkl")) if variant == "extra-source" else None,
                                      return_value=sources):
                        with self.assertRaises((ValueError, OSError)):
                            s.stage("docker", COMMIT, root / "bin/pkl", root / "sealed")
                    self.assertFalse((root / "sealed").exists())

    def test_archive_payload_tamper_is_not_public(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                output, evidence = self.fixture(root, True)
                evidence["dillArtifactSupply"]["archiveSha256"] = "0" * 64
                (output / "evidence.json").write_text(json.dumps(evidence))
                with patch.object(s, "public_sources", return_value={"bin/dill": root / "bin/dill"}), self.assertRaises(ValueError):
                    s.stage("docker", COMMIT, root / "bin/pkl", root / "sealed")
                self.assertFalse((root / "sealed").exists())

    def test_hidden_trailing_archive_bytes_are_rejected_even_with_updated_hash(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                output, evidence = self.fixture(root, True)
                archive = output / "handoff.tar.gz"
                with archive.open("ab") as target:
                    target.write(b"PRIVATE-HIDDEN-TRAILER")
                evidence["dillArtifactSupply"]["archiveSha256"] = s.q.sha256(archive)
                (output / "evidence.json").write_text(json.dumps(evidence))
                with patch.object(s, "public_sources", return_value={"bin/dill": root / "bin/dill"}), self.assertRaises((ValueError, OSError)):
                    s.stage("docker", COMMIT, root / "bin/pkl", root / "sealed")
                self.assertFalse((root / "sealed").exists())

    def test_public_source_namespace_rejects_untracked_extras(self):
        from types import SimpleNamespace
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                self.fixture(root)
                directory = root / "test/integration"
                directory.mkdir()
                (directory / "public_test.go").write_text("package integration")
                with patch.object(s.subprocess, "run", return_value=SimpleNamespace(stdout=b"test/integration/public_test.go\n")):
                    self.assertIn("test/integration/public_test.go", s.public_sources(root / "bin/pkl"))
                    (directory / "private.log").write_text("PRIVATE")
                    with self.assertRaises(ValueError):
                        s.public_sources(root / "bin/pkl")

    def test_workflow_uploads_only_a_successfully_sealed_directory(self):
        workflow = (s.q.ROOT / ".github/workflows/alpha-qualification.yml").read_text()
        self.assertIn("id: qualify", workflow)
        self.assertIn("steps.qualify.outcome == 'failure'", workflow)
        self.assertIn("steps.seal_public.outcome == 'success'", workflow)
        paths = workflow.split("          path: |\n", 1)[1].strip().splitlines()
        self.assertEqual(paths, ["${{ runner.temp }}/dill-public-${{ matrix.engine }}/"])
        self.assertNotIn("*", paths[0])

    def test_nested_diagnostics_and_typed_integers_fail_before_sealing(self):
        valid = {"argv": ["git", "rev-parse", "HEAD"], "expectedExit": 0, "passed": True, "exit": 0,
                 "stdoutSha256": "b" * 64, "stderrSha256": "c" * 64, "stdoutBytes": 40, "stderrBytes": 0}
        variants = [("stdout", "PRIVATE"), ("stderr", "PRIVATE"), ("env", {"TOKEN": "PRIVATE"}),
                    ("expectedExit", True), ("exit", None), ("stdoutBytes", True), ("stderrBytes", 1.5),
                    ("failure", "PRIVATE-freeform"), ("cleanupFailure", "PRIVATE-freeform")]
        for key, value in variants:
            with self.subTest(key=key), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                    output, evidence = self.fixture(root)
                    evidence["commands"] = [dict(valid, **{key: value})]
                    (output / "evidence.json").write_text(json.dumps(evidence))
                    with patch.object(s, "public_sources", return_value={"bin/dill": root / "bin/dill"}), self.assertRaises((ValueError, TypeError)):
                        s.stage("docker", COMMIT, root / "bin/pkl", root / "sealed")
                    self.assertFalse((root / "sealed").exists())
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                output, evidence = self.fixture(root)
                evidence.update(commands=[valid], error="ValueError")
                (output / "evidence.json").write_text(json.dumps(evidence))
                with patch.object(s, "public_sources", return_value={"bin/dill": root / "bin/dill"}):
                    s.stage("docker", COMMIT, root / "bin/pkl", root / "sealed")

    def test_nested_record_extensions_are_rejected_at_every_allowed_object(self):
        for name in ("source", "binary", "pkl", "schema", "qualifiedProvisioning", "dillArtifactSupply", "engineVersion", "acceptance"):
            with self.subTest(record=name), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                    output, evidence = self.fixture(root)
                    value = dict(evidence.get(name, {}), stdout="PRIVATE-REJECT-AT-ANY-DEPTH")
                    evidence[name] = value
                    (output / "evidence.json").write_text(json.dumps(evidence))
                    with patch.object(s, "public_sources", return_value={"bin/dill": root / "bin/dill"}), self.assertRaises((ValueError, TypeError)):
                        s.stage("docker", COMMIT, root / "bin/pkl", root / "sealed")
                    self.assertFalse((root / "sealed").exists())

    def test_genuine_producer_metadata_collects_with_fake_transport_only(self):
        # Real collect/Recorder/redaction/version projection and real stage.
        # Popen is fake; no executable or daemon runs, and complete is FALSE.
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()  # Actual ROOT is always __file__.resolve().
            with patch.object(s.q, "ROOT", root), patch.object(s.q, "FIXTURES", root / "test/qualification/v1"):
                output, _ = self.fixture(root)
                shutil.rmtree(output)
                for name in ("dill", "pkl"):
                    (root / "bin" / name).chmod(0o500)
                manifest = json.loads((s.q.FIXTURES / "cases.json").read_text())
                names = [n for c in manifest["cases"] if c["mode"] == "engine" for n in s.q.runtime_tests(c)]
                class FakeProcess:
                    def __init__(self, argv, **kwargs):
                        self.returncode = 0
                        self.output = b""
                        if argv[:3] == ["git", "rev-parse", "HEAD"]: self.output = COMMIT.encode()
                        elif argv[:2] == ["go", "version"]:
                            self.output = json.dumps({"GoVersion": "go1.25.0", "Settings": [{"Key": k, "Value": v} for k, v in {"GOOS": "linux", "GOARCH": "amd64", "vcs.revision": COMMIT, "vcs.modified": "false"}.items()]}).encode()
                        elif argv[-1] == "--version": self.output = b"Pkl 0.31.1 (synthetic)"
                        elif argv[-1] == "version": self.output = b"synthetic-version"
                        elif argv[-1] == "validate": self.returncode = 2 if argv[-2].endswith("invalid-graph.pkl") else 0
                        elif argv[0] == "docker": self.output = b'{"Client":{"Version":"synthetic-client"},"Server":{"Version":"synthetic-server","Details":{"private":"NOT-PUBLISHED"}}}'
                        elif argv[0] == "make": self.output = "".join("--- PASS: " + name + " (0s)\n" for name in names).encode()
                    def communicate(self, timeout=None): return self.output, b""
                with socket.socket(socket.AF_UNIX) as sock:
                    endpoint = root / "s"
                    sock.bind(str(endpoint))
                    args = argparse.Namespace(expected_commit=COMMIT, binary=str(root / "bin/dill"), binary_sha256=s.q.sha256(root / "bin/dill"), pkl=str(root / "bin/pkl"), output=str(output), allow_dirty=False, engine="docker", socket=str(endpoint), disposable=True, operations=False, profiles=False, require_complete=False, qualified=False, coordination_daemon_id=None, coordination_state_root=None, native_layout_provisioned=False, provisioning_source=None)
                    with patch.object(s.q.subprocess, "Popen", FakeProcess):
                        self.assertEqual(s.q.collect(args), 0)
                evidence = json.loads((output / "evidence.json").read_text())
                self.assertEqual(evidence["qualification"], "incomplete")
                self.assertFalse(evidence["acceptance"]["complete"])
                self.assertEqual(evidence["engineVersion"], {"engine": "docker", "clientVersion": "synthetic-client", "serverVersion": "synthetic-server"})
                with patch.object(s, "public_sources", return_value={"bin/dill": root / "bin/dill"}):
                    s.stage("docker", COMMIT, root / "bin/pkl", root / "sealed")
                public = (root / "sealed/bin/alpha-evidence-docker/evidence.json").read_bytes()
                self.assertNotIn(b"NOT-PUBLISHED", public)
                self.assertNotIn(str(root).encode(), json.dumps(evidence["commands"]).encode())

    def test_strict_json(self):
        for data in (b'{"a":1,"a":2}', b'{"a":NaN}', b'{} {}', b'\xff', b'\xef\xbb\xbf{}'):
            with self.subTest(data=data), self.assertRaises((ValueError, UnicodeError)):
                s.strict_json(data)


if __name__ == "__main__":
    unittest.main()
