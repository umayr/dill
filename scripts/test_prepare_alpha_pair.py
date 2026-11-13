"""Synthetic archive regressions only: no candidate/engine/application execution."""

import copy
import importlib.util
import io
import json
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest
import warnings
import zipfile

SPEC = importlib.util.spec_from_file_location("prepare_pair", Path(__file__).with_name("prepare-alpha-pair.py"))
q = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(q)


class PairInputTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.pin = q.json_document(q.PIN_FILE.read_bytes())
        self.pin.update(candidateCommit="a" * 40, sourceTree="b" * 40)
        self.files = {name: ("synthetic-not-executable:" + name).encode() for name in q.EXPECTED_FILES}

    def fixture(self, change_manifest=None, change_members=None, extra_zip=None):
        pin = copy.deepcopy(self.pin)
        manifest = {"version": "dill.alpha-handoff/1", "status": "verified",
                    "candidateCommit": pin["candidateCommit"], "fixtureVersion": pin["fixtureVersion"],
                    "binarySha256": q.digest(self.files["bin/dill"]),
                    "downstreamBrineAcceptance": "not-run",
                    "files": {name: q.digest(value) for name, value in self.files.items()},
                    "caller": {"contract": "dill.operation/1", "source": "brine",
                               "outcome": "succeeded", "effects": "complete",
                               "qualifiedIdentityDigest": "sha256:" + "c" * 64}}
        if change_manifest:
            change_manifest(manifest)
        manifest_bytes = json.dumps(manifest, sort_keys=True).encode()
        entries = []
        for name, data in {**self.files, "handoff.json": manifest_bytes}.items():
            info = tarfile.TarInfo(name)
            info.mode = 0o755 if name in ("bin/dill", "bin/pkl") else 0o644
            info.size = len(data)
            entries.append((info, data))
        if change_members:
            change_members(entries)
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as t:
            for info, data in entries:
                t.addfile(info, io.BytesIO(data))
        payload = buf.getvalue()
        pin.update(archiveSha256=q.digest(payload), manifestSha256=q.digest(manifest_bytes),
                   binarySha256=q.digest(self.files["bin/dill"]), pklSha256=q.digest(self.files["bin/pkl"]),
                   schemaSha256=q.digest(self.files["dill.pkl"]))
        archive = self.root / "input.zip"
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", UserWarning)
            with zipfile.ZipFile(archive, "w") as z:
                z.writestr(pin["archiveMember"], payload)
                if extra_zip:
                    name, data, mode = extra_zip
                    info = zipfile.ZipInfo(name)
                    info.external_attr = mode << 16
                    z.writestr(info, data)
        pin.update(zipSha256=q.file_digest(archive), artifactSize=archive.stat().st_size)
        metadata = {"id": pin["artifactId"], "name": pin["artifactName"],
                    "size_in_bytes": pin["artifactSize"], "digest": "sha256:" + pin["zipSha256"],
                    "expired": False, "workflow_run": {"id": pin["qualificationRun"],
                    "head_sha": pin["candidateCommit"], "head_branch": "main"}}
        return archive, metadata, pin

    def test_validation_does_not_extract_or_claim_application_acceptance(self):
        archive, metadata, pin = self.fixture()
        result = q.prepare(archive, metadata, pin)
        self.assertEqual(result["status"], "verified-dill-input")
        self.assertEqual(result["jointApplicationAcceptance"], "not-run")
        self.assertIs(result["receivingTargetQualified"], False)
        self.assertIs(result["payloadExtracted"], False)
        self.assertEqual(sorted(x.name for x in self.root.iterdir()), ["input.zip"])

    def test_explicit_extraction_is_private_new_and_byte_identical(self):
        archive, metadata, pin = self.fixture()
        output = self.root / "supply"
        result = q.prepare(archive, metadata, pin, output)
        self.assertTrue(result["payloadExtracted"])
        self.assertEqual(output.stat().st_mode & 0o777, 0o700)
        for name, data in self.files.items():
            f = output / name
            self.assertEqual(f.read_bytes(), data)
            self.assertEqual(f.stat().st_mode & 0o777, 0o500 if name in ("bin/dill", "bin/pkl") else 0o400)
        with self.assertRaises(FileExistsError):
            q.prepare(archive, metadata, pin, output)

    def test_existing_or_symlink_output_never_overwrites(self):
        archive, metadata, pin = self.fixture()
        existing = self.root / "existing"
        existing.mkdir()
        sentinel = existing / "sentinel"
        sentinel.write_text("preserve")
        link = self.root / "link"
        link.symlink_to(existing, target_is_directory=True)
        for out in (existing, link):
            with self.subTest(out=out.name), self.assertRaises(FileExistsError):
                q.prepare(archive, metadata, pin, out)
        self.assertEqual(sentinel.read_text(), "preserve")

    def test_public_or_symlink_parent_is_rejected(self):
        archive, metadata, pin = self.fixture()
        public = self.root / "public"
        public.mkdir(mode=0o755)
        public.chmod(0o755)
        link = self.root / "parent-link"
        link.symlink_to(self.root, target_is_directory=True)
        for out in (public / "out", link / "out", Path("relative")):
            with self.subTest(out=str(out)), self.assertRaises(q.Rejected):
                q.prepare(archive, metadata, pin, out)

    def test_metadata_identity_expiry_and_type_are_not_inferred(self):
        archive, metadata, pin = self.fixture()
        mutations = [lambda m: m.update(expired=True), lambda m: m.pop("expired"),
                     lambda m: m.update(id=str(pin["artifactId"])),
                     lambda m: m.update(size_in_bytes=True), lambda m: m.update(name="latest"),
                     lambda m: m.update(digest="sha256:" + "0" * 64),
                     lambda m: m["workflow_run"].update(id=True),
                     lambda m: m["workflow_run"].update(head_sha="d" * 40),
                     lambda m: m["workflow_run"].update(head_branch="feature"),
                     lambda m: m.pop("workflow_run")]
        for mutate in mutations:
            bad = copy.deepcopy(metadata)
            mutate(bad)
            with self.subTest(bad=bad), self.assertRaises(q.Rejected):
                q.prepare(archive, bad, pin)

    def test_each_external_checksum_is_required(self):
        for key in ("zipSha256", "archiveSha256", "manifestSha256", "binarySha256", "pklSha256", "schemaSha256"):
            archive, metadata, pin = self.fixture()
            pin[key] = "0" * 64
            if key == "zipSha256":
                metadata["digest"] = "sha256:" + pin[key]
            with self.subTest(key=key), self.assertRaises(q.Rejected):
                q.prepare(archive, metadata, pin)

    def test_outer_zip_paths_duplicates_and_links_reject_before_output(self):
        for name, mode in (("../escape", stat.S_IFREG | 0o644), ("/absolute", 0),
                           ("a\\b", 0), ("a//b", 0), ("a/./b", 0), ("C:drive", 0),
                           ("link", stat.S_IFLNK | 0o777), (self.pin["archiveMember"], 0)):
            archive, metadata, pin = self.fixture(extra_zip=(name, b"synthetic", mode))
            with self.subTest(name=name), self.assertRaises(q.Rejected):
                q.prepare(archive, metadata, pin, self.root / "output")
            self.assertFalse((self.root / "output").exists())

    def test_tar_allowlist_paths_duplicates_links_modes_and_metadata_reject(self):
        def extra(entries):
            info = tarfile.TarInfo("unapproved.txt")
            info.mode = 0o644
            entries.append((info, b""))
        mutations = [lambda e: e.pop(), extra, lambda e: e.append(copy.deepcopy(e[0])),
                     lambda e: setattr(e[0][0], "name", "../escape"),
                     lambda e: setattr(e[0][0], "type", tarfile.SYMTYPE),
                     lambda e: setattr(e[0][0], "type", tarfile.LNKTYPE),
                     lambda e: setattr(e[0][0], "mode", 0o777),
                     lambda e: setattr(e[0][0], "uid", 123),
                     lambda e: setattr(e[0][0], "gname", "untrusted"),
                     lambda e: setattr(e[0][0], "mtime", 1),
                     lambda e: e[0][0].pax_headers.update(comment="not-normalized")]
        for index, mutate in enumerate(mutations):
            archive, metadata, pin = self.fixture(change_members=mutate)
            with self.subTest(index=index), self.assertRaises(q.Rejected):
                q.prepare(archive, metadata, pin, self.root / "output")
            self.assertFalse((self.root / "output").exists())

    def test_manifest_never_upgrades_copied_caller_to_application_or_receiving_host(self):
        mutations = [lambda m: m.update(candidateCommit="d" * 40),
                     lambda m: m.update(status="complete"),
                     lambda m: m.update(downstreamBrineAcceptance="passed"),
                     lambda m: m["caller"].update(source="cli"),
                     lambda m: m["caller"].update(qualifiedIdentityDigest="raw-daemon-id"),
                     lambda m: m["files"].pop("dill.pkl"),
                     lambda m: m["files"].update({"private/key": "0" * 64}),
                     lambda m: m["files"].update({"dill.pkl": "0" * 64})]
        for index, mutate in enumerate(mutations):
            archive, metadata, pin = self.fixture(change_manifest=mutate)
            with self.subTest(index=index), self.assertRaises(q.Rejected):
                q.prepare(archive, metadata, pin)

    def test_json_duplicate_null_and_nonfinite_inputs_are_not_success(self):
        for data in (b'{"id":1,"id":2}', b'{"id":NaN}', b'{"id":Infinity}'):
            with self.subTest(data=data), self.assertRaises(q.Rejected):
                q.json_document(data)
        archive, _, pin = self.fixture()
        for metadata in (None, [], 1, True):
            with self.subTest(metadata=metadata), self.assertRaises(q.Rejected):
                q.prepare(archive, metadata, pin)

    def test_bad_input_creates_no_output(self):
        archive, metadata, pin = self.fixture()
        archive.write_bytes(b"not a zip and not an accepted input")
        with self.assertRaises(q.Rejected):
            q.prepare(archive, metadata, pin, self.root / "output")
        self.assertFalse((self.root / "output").exists())


if __name__ == "__main__":
    unittest.main()
