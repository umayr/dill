#!/usr/bin/env python3
"""Offline v2 input tests: only synthetic nonexecutables are ever extracted."""
from contextlib import redirect_stderr, redirect_stdout
from datetime import datetime, timedelta, timezone
import copy
import gzip
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import warnings
import zipfile

SPEC = importlib.util.spec_from_file_location("pair_v2", Path(__file__).with_name("prepare-alpha-pair-v2.py"))
q = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(q)
NOW = datetime(2026, 11, 15, tzinfo=timezone.utc)


def encoded(value):
    return (json.dumps(value, sort_keys=True, indent=2) + "\n").encode()


class PairV2Tests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve()
        self.root.chmod(0o700)
        self.path = self.root / "synthetic.zip"
        self.pin = json.loads(q.PIN_FILE.read_text())
        self.data = {name: b"synthetic nonexecutable\n" for name in q.EXPECTED_FILES}
        self.data["test/qualification/v1/cases.json"] = encoded({"version": "dill.alpha-qualification/2", "cases": [{"id": n} for n in sorted(q.EXPECTED_CASES)]})
        self.manifest = {"version": "dill.alpha-handoff/2", "status": "verified",
                         "candidateCommit": self.pin["candidateCommit"], "fixtureVersion": self.pin["fixtureVersion"],
                         "binarySha256": q.digest(self.data["bin/dill"]), "downstreamBrineAcceptance": "not-run",
                         "files": {n: q.digest(v) for n, v in self.data.items()},
                         "caller": {"contract": "dill.operation/1", "source": "brine", "outcome": "succeeded", "effects": "complete", "qualifiedIdentityDigest": "sha256:" + "1" * 64}}
        for name, field in (("bin/dill", "binarySha256"), ("bin/pkl", "pklSha256"), ("dill.pkl", "schemaSha256")):
            self.pin[field] = q.digest(self.data[name])
        self.pack()

    def pack(self, *, payload=None, outer=None):
        self.data["handoff.json"] = encoded(self.manifest)
        payload = q.canonical_archive(self.data) if payload is None else payload
        self.pin["manifestSha256"] = q.digest(self.data["handoff.json"])
        self.pin["archiveSha256"] = q.digest(payload)
        entries = {name: self.data.get(name, b"synthetic source\n") for name in q.OUTER_FILES}
        entries[q.ARCHIVE_MEMBER] = payload
        if outer is not None:
            outer(entries)
        with zipfile.ZipFile(self.path, "w") as z:
            for name, value in entries.items():
                z.writestr(name, value)
        self.rebind()

    def rebind(self):
        self.pin["zipSha256"] = q.digest(self.path.read_bytes())
        self.pin["artifactSize"] = self.path.stat().st_size
        self.metadata = {"id": self.pin["artifactId"], "name": self.pin["artifactName"],
                         "size_in_bytes": self.pin["artifactSize"], "digest": "sha256:" + self.pin["zipSha256"],
                         "expired": False, "expires_at": self.pin["expiresAt"],
                         "workflow_run": {"id": self.pin["qualificationRun"], "head_sha": self.pin["candidateCommit"], "head_branch": "main"}}

    def prepare(self, *, now=NOW, output=None):
        return q.prepare(self.path, self.metadata, self.pin, output, now=now)

    def rejected(self):
        output = self.root / "must-not-exist"
        with self.assertRaises((q.Rejected, ValueError, OSError, tarfile.TarError, zipfile.BadZipFile)):
            self.prepare(output=output)
        self.assertFalse(output.exists())

    def test_valid_result_is_closed_and_never_target_authority(self):
        result = self.prepare()
        self.assertEqual(set(result), {"version", "status", "candidateCommit", "sourceTree", "binarySha256", "archiveSha256", "manifestSha256", "pklSha256", "schemaSha256", "platform", "jointApplicationAcceptance", "receivingTargetQualified", "payloadExtracted"})
        self.assertEqual(result["version"], "dill.brine-alpha-input-result/2")
        self.assertIs(result["receivingTargetQualified"], False)
        self.assertIs(result["payloadExtracted"], False)
        self.assertEqual(result["jointApplicationAcceptance"], "not-run")
        self.assertNotIn("qualifiedIdentityDigest", json.dumps(result))
        self.assertEqual(sorted(p.name for p in self.root.iterdir()), ["synthetic.zip"])

    def test_private_synthetic_extraction_exact_bytes_modes_no_overwrite(self):
        out = self.root / "output"
        self.assertIs(self.prepare(output=out)["payloadExtracted"], True)
        self.assertEqual({str(p.relative_to(out)) for p in out.rglob("*") if p.is_file()}, set(self.data))
        for p in [out, *out.rglob("*")]:
            expected = 0o700 if p.is_dir() else (0o500 if str(p.relative_to(out)) in {"bin/dill", "bin/pkl"} else 0o400)
            self.assertEqual(stat.S_IMODE(p.stat().st_mode), expected)
            if p.is_file():
                self.assertEqual(p.read_bytes(), self.data[str(p.relative_to(out))])
        with self.assertRaises(FileExistsError):
            self.prepare(output=out)

    def test_expiry_absolute_and_exact_boundary(self):
        end = q.expiry(self.pin["expiresAt"])
        self.prepare(now=end - timedelta(microseconds=1))
        for now in (end, end + timedelta(seconds=1), end + timedelta(days=50)):
            with self.subTest(now=now), self.assertRaises(q.Rejected):
                self.prepare(now=now)  # API expired remains false: local UTC still wins.

    def test_expiry_is_rechecked_before_any_output(self):
        end = q.expiry(self.pin["expiresAt"])
        out = self.root / "no-create"
        with patch.object(q, "datetime") as clock:
            clock.strptime.side_effect = datetime.strptime
            clock.now.side_effect = [end - timedelta(seconds=1), end]
            with self.assertRaises(q.Rejected):
                q.prepare(self.path, self.metadata, self.pin, out)
        self.assertFalse(out.exists())

    def test_metadata_exact_types_identity_run_and_expiry(self):
        original = copy.deepcopy(self.metadata)
        for key in original:
            values = [None, True, 0, 1.0, [], {}, "wrong"]
            if key == "expired":
                values += [1, "false"]
            for value in values:
                if type(value) is type(original[key]) and value == original[key]:
                    continue
                with self.subTest(key=key, value=value):
                    self.metadata = copy.deepcopy(original)
                    self.metadata[key] = value
                    self.rejected()
        for key in original["workflow_run"]:
            for value in (None, True, 1.0, "wrong"):
                self.metadata = copy.deepcopy(original)
                self.metadata["workflow_run"][key] = value
                with self.subTest(run=key, value=value):
                    self.rejected()
        self.metadata = copy.deepcopy(original)
        self.metadata["expires_at"] = "2026-11-30T02:16:09Z"
        self.rejected()
        for key in original:
            self.metadata = copy.deepcopy(original)
            del self.metadata[key]
            with self.subTest(missing=key):
                self.rejected()

    def test_pin_closed_required_types_and_formats(self):
        original = copy.deepcopy(self.pin)
        for key in original:
            for value in (None, True, 0, 1.0, [], {}):
                self.pin = copy.deepcopy(original)
                self.pin[key] = value
                with self.subTest(key=key, value=value):
                    self.rejected()
            self.pin = copy.deepcopy(original)
            del self.pin[key]
            with self.subTest(missing=key):
                self.rejected()
        self.pin = {**original, "extra": "bad"}
        self.rejected()
        for key, value in (("version", "dill.brine-alpha-input/1"), ("fixtureVersion", "dill.alpha-qualification/1"), ("expiresAt", "2026-11-29T02:16:09+00:00"), ("expiresAt", "2026-02-30T00:00:00Z"), ("candidateCommit", "A" * 40), ("artifactId", "14143195067"), ("platform", "darwin-arm64")):
            self.pin = {**original, key: value}
            with self.subTest(key=key, value=value):
                self.rejected()

    def test_all_pinned_hash_anchors(self):
        original = copy.deepcopy(self.pin)
        for key in ("zipSha256", "archiveSha256", "manifestSha256", "binarySha256", "pklSha256", "schemaSha256"):
            self.pin = {**original, key: "f" * 64}
            with self.subTest(key=key):
                self.rejected()

    def test_manifest_and_caller_closed_fields_types_versions(self):
        original = copy.deepcopy(self.manifest)
        for location in (None, "caller"):
            subject = original if location is None else original[location]
            for key in subject:
                for value in (None, True, [], {}, "wrong"):
                    self.manifest = copy.deepcopy(original)
                    target = self.manifest if location is None else self.manifest[location]
                    target[key] = value
                    self.pack()
                    with self.subTest(location=location, key=key, value=value):
                        self.rejected()
                self.manifest = copy.deepcopy(original)
                del (self.manifest if location is None else self.manifest[location])[key]
                self.pack()
                self.rejected()
            self.manifest = copy.deepcopy(original)
            (self.manifest if location is None else self.manifest[location])["extra"] = "bad"
            self.pack()
            self.rejected()
        for key, value in (("version", "dill.alpha-handoff/1"), ("fixtureVersion", "dill.alpha-qualification/1")):
            self.manifest = {**copy.deepcopy(original), key: value}
            self.pack()
            self.rejected()

    def test_catalog_old_24_wrong_version_duplicates_unknown_case(self):
        for version, cases in (("dill.alpha-qualification/1", sorted(q.EXPECTED_CASES)), ("dill.alpha-qualification/2", sorted(q.EXPECTED_CASES)[:24]), ("dill.alpha-qualification/2", ["offline-valid"] * 30), ("dill.alpha-qualification/2", ["unknown"] + sorted(q.EXPECTED_CASES)[1:])):
            name = "test/qualification/v1/cases.json"
            self.data[name] = encoded({"version": version, "cases": [{"id": n} for n in cases]})
            self.manifest["files"][name] = q.digest(self.data[name])
            self.pack()
            self.rejected()

    def test_outer_missing_extra_unsafe_names_and_duplicate(self):
        for name in ("extra", "../escape", "/absolute", "a//b", "a/./b", "a\\b", "a:b", "dir/"):
            self.pack(outer=lambda entries: entries.update({name: b"bad"}))
            with self.subTest(name=name):
                self.rejected()
        self.pack(outer=lambda entries: entries.pop("docs/named-preflight-v1.md"))
        self.rejected()
        self.pack()
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", UserWarning)
            with zipfile.ZipFile(self.path, "a") as z:
                z.writestr("dill.pkl", b"duplicate")
        self.rebind()
        self.rejected()

    def test_outer_symlink(self):
        with zipfile.ZipFile(self.path, "a") as z:
            info = zipfile.ZipInfo("symlink")
            info.create_system = 3
            info.external_attr = (stat.S_IFLNK | 0o777) << 16
            z.writestr(info, "target")
        self.rebind()
        self.rejected()

    def test_inner_metadata_links_modes_order_and_hidden_trailer(self):
        for change in ("uid", "gid", "uname", "mtime", "mode", "link", "symlink", "pax", "reverse", "extra", "missing"):
            out = io.BytesIO()
            with gzip.GzipFile(filename="", mode="wb", fileobj=out, mtime=0) as gz:
                with tarfile.open(fileobj=gz, mode="w") as t:
                    names = sorted(self.data, reverse=change == "reverse")
                    if change == "extra":
                        names += ["extra"]
                    if change == "missing":
                        names.remove("docs/named-preflight-v1.md")
                    for name in names:
                        b = self.data.get(name, b"bad")
                        info = tarfile.TarInfo(name)
                        info.size = len(b)
                        info.mode = 0o755 if name in {"bin/dill", "bin/pkl"} else 0o644
                        if name == "dill.pkl":
                            if change in ("uid", "gid", "mtime", "mode"):
                                setattr(info, change, 123)
                            elif change == "uname":
                                info.uname = "other"
                            elif change in ("link", "symlink"):
                                info.type = tarfile.LNKTYPE if change == "link" else tarfile.SYMTYPE
                                info.linkname = "bin/dill"
                            elif change == "pax":
                                info.pax_headers = {"comment": "bad"}
                        t.addfile(info, io.BytesIO(b))
            self.pack(payload=out.getvalue())
            with self.subTest(change=change):
                self.rejected()
        self.pack(payload=q.canonical_archive(self.data) + b"hidden")
        self.rejected()
        self.pack(payload=q.canonical_archive(self.data) + gzip.compress(b"hidden", mtime=0))
        self.rejected()

    def test_payload_bytes_manifest_members_and_size_bounds(self):
        self.data["bin/dill"] = b"tampered"
        self.pack()
        self.rejected()
        with patch.object(q, "MAX_TAR", 20):
            self.rejected()
        with patch.object(q, "MAX_ARCHIVE", 20):
            self.rejected()
        for name in ("dill.pkl", "extra"):
            manifest = copy.deepcopy(self.manifest)
            if name == "dill.pkl":
                del self.manifest["files"][name]
            else:
                self.manifest["files"][name] = "0" * 64
            self.pack()
            self.rejected()
            self.manifest = manifest

    def test_json_duplicate_nonfinite_float_non_utf8(self):
        for b in (b'{"id":1,"id":2}', b'{"n":NaN}', b'{"n":Infinity}', b'{"n":1.5}', b'\xef\xbb\xbf{}', b'\xff', '{}'.encode('utf-16')):
            with self.subTest(data=b), self.assertRaises((q.Rejected, ValueError, UnicodeError)):
                q.json_document(b)

    def test_output_parent_and_existing_symlink_refused(self):
        self.root.chmod(0o755)
        with self.assertRaises(q.Rejected):
            self.prepare(output=self.root / "public")
        self.root.chmod(0o700)
        alias = self.root / "alias"
        alias.symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(q.Rejected):
            self.prepare(output=alias / "output")
        out = self.root / "existing"
        out.symlink_to(self.root / "missing", target_is_directory=True)
        with self.assertRaises(FileExistsError):
            self.prepare(output=out)
        with self.assertRaises(q.Rejected):
            self.prepare(output=Path("relative"))

    def test_v1_and_v2_cannot_auto_accept_other_versions(self):
        spec = importlib.util.spec_from_file_location("pair_v1", Path(__file__).with_name("prepare-alpha-pair.py"))
        v1 = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(v1)
        with self.assertRaises(v1.Rejected):
            v1.verified_payload(q.canonical_archive(self.data), self.pin)
        with self.assertRaises(q.Rejected):
            q.validate_pin(json.loads(v1.PIN_FILE.read_text()))

    def test_cli_success_fixed_errors_no_output_or_arbitrary_pin(self):
        pin_path, meta_path = self.root / "pin.json", self.root / "metadata.json"
        pin_path.write_bytes(encoded(self.pin))
        meta_path.write_bytes(encoded(self.metadata))
        args = ["tool", "--artifact-zip", str(self.path), "--artifact-metadata", str(meta_path), "--validate-only"]
        with patch.object(q, "PIN_FILE", pin_path), patch.object(q, "datetime") as clock:
            clock.now.return_value = NOW
            clock.strptime.side_effect = datetime.strptime
            for suffix in ([], ["--pin", "do-not-read"], ["--output", str(self.root / "no-create")]):
                stdout, stderr = io.StringIO(), io.StringIO()
                with patch("sys.argv", args + suffix), redirect_stdout(stdout), redirect_stderr(stderr):
                    code = q.main()
                if not suffix:
                    self.assertEqual(code, 0)
                    self.assertEqual(json.loads(stdout.getvalue())["version"], "dill.brine-alpha-input-result/2")
                    self.assertEqual(stderr.getvalue(), "")
                else:
                    self.assertEqual(code, 1)
                    self.assertEqual(stdout.getvalue(), "")
                    self.assertEqual(stderr.getvalue(), "DILL_PAIR_INPUT_REJECTED\n")
                    self.assertFalse((self.root / "no-create").exists())


if __name__ == "__main__":
    unittest.main()
