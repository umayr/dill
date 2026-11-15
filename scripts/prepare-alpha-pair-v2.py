#!/usr/bin/env python3
"""Explicit, source-pinned v2 input preparation; NOT application qualification.

Offline only. No network, subprocess, candidate execution or fallback. Actual
Linux supply is inspected locally only with --validate-only; --output is for
approved disposable receivers (unit extraction uses synthetic nonexecutables).
The separate v1 entrypoint and pin remain unchanged.
"""
import argparse
from datetime import datetime, timezone
import gzip
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import sys
import tarfile
import zipfile
import zlib

ROOT = Path(__file__).resolve().parents[1]
PIN_FILE = ROOT / "test/qualification/pair/v2/dill-input.json"
MAX_ARCHIVE = MAX_PAYLOAD = 256 * 1024 * 1024
MAX_TAR = MAX_PAYLOAD + 128 * 1024
ARCHIVE_MEMBER = "bin/alpha-evidence-docker/handoff.tar.gz"
PIN_KEYS = frozenset("version repository candidateCommit sourceTree qualificationRun artifactId artifactName artifactSize zipSha256 expiresAt archiveMember archiveSha256 manifestSha256 binarySha256 pklVersion pklSha256 schemaSha256 fixtureVersion platform jointApplicationAcceptance".split())
OPERATION_NAMES = "manifest success failure unsupported-operation unsupported-version qualified-success qualified-cleanup-failure".split()
PREFLIGHT_NAMES = "apply-mismatch argv canonical-vectors capabilities-supported capabilities-unsupported manifest negative-wire-cases preview-down-absent preview-empty-start preview-invalid-contract preview-limit-failure preview-metadata-unknown preview-up-config-noop preview-up-create preview-up-recreate".split()
EXPECTED_FILES = frozenset({
    "bin/dill", "bin/pkl", "dill.pkl", "docs/alpha-candidate-handoff.md",
    "docs/named-preflight-v1.md", "internal/loader/pkl-toolchain.json",
    *("internal/operation/testdata/" + n + ".json" for n in OPERATION_NAMES),
    *("internal/preflight/testdata/" + n + ".json" for n in PREFLIGHT_NAMES),
    "test/qualification/v1/cases.json", "test/qualification/v1/valid.pkl",
    "test/qualification/v1/invalid-graph.pkl",
})
INTEGRATION_NAMES = "alpha_cleanup alpha_coordination_concurrency alpha_coordination alpha_guardrails alpha_handoff alpha_named_preflight alpha_named_qualification_fixture alpha_named_qualification alpha_operations cancellation capabilities_conformance conformance dill_integration failed_start operation ownership port_bindings preflight_integration resources secrets".split()
OUTER_FILES = (EXPECTED_FILES - {"bin/pkl"}) | {
    "bin/alpha-evidence-docker/evidence.json", ARCHIVE_MEMBER,
    *("test/integration/" + n + "_test.go" for n in INTEGRATION_NAMES),
    "scripts/qualify-alpha.py", "scripts/stage-alpha-artifact.py",
    "scripts/test_qualify_alpha.py", "scripts/test_stage_alpha_artifact.py",
}
EXPECTED_CASES = frozenset("offline-valid offline-invalid-graph success-repeat-config-change changed-image invalid-config-preserves-live foreign-container-collision retained-volume cancellation failed-start-cleanup operation-contract-baseline operation-outcomes no-prune bounded-storage-preflight same-stack-concurrency crash-unknown-recovery foreign-resource-inventory brine-candidate-handoff qualified-docker-profile qualified-podman-rejected different-stack-concurrency alias-concurrency corrupt-journal-rejection inspected-exact-operation-recovery qualified-image-volume-rejected named-preflight-five-actions named-preflight-mismatch-before-ensure named-preflight-store-selector-binding named-preflight-lock-cancellation named-preflight-empty-orphan named-preflight-metadata-refusal".split())
assert len(EXPECTED_FILES) == 31 and len(OUTER_FILES) == 56 and len(EXPECTED_CASES) == 30


class Rejected(ValueError):
    """Private fixed rejection, never reflected as a freeform public diagnostic."""


def require(value):
    if not value:
        raise Rejected("DILL_PAIR_INPUT_REJECTED")


def digest(data):
    return hashlib.sha256(data).hexdigest()


def json_document(data):
    require(type(data) is bytes and len(data) <= 1048576 and not data.startswith(b"\xef\xbb\xbf"))
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result)
            result[key] = value
        return result
    def invalid(_):
        raise Rejected("DILL_PAIR_INPUT_REJECTED")
    return json.loads(data.decode("utf-8"), object_pairs_hook=pairs,
                      parse_constant=invalid, parse_float=invalid)


def fields(value, keys):
    require(type(value) is dict and set(value) == set(keys))
    return value


def safe_name(name):
    return (type(name) is str and bool(name) and "\\" not in name and ":" not in name
            and not name.startswith("/") and all(p not in ("", ".", "..") for p in name.split("/"))
            and str(PurePosixPath(name)) == name)


def expiry(text):
    require(type(text) is str and re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", text, flags=re.ASCII))
    return datetime.strptime(text, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)


def validate_pin(pin):
    fields(pin, PIN_KEYS)
    for name in PIN_KEYS:
        value = pin[name]
        if name in {"qualificationRun", "artifactId", "artifactSize"}:
            require(type(value) is int and value > 0)
        else:
            require(type(value) is str)
    for name in ("candidateCommit", "sourceTree"):
        require(re.fullmatch(r"[0-9a-f]{40}", pin[name]))
    for name in ("zipSha256", "archiveSha256", "manifestSha256", "binarySha256", "pklSha256", "schemaSha256"):
        require(re.fullmatch(r"[0-9a-f]{64}", pin[name]))
    for name, value in {"version": "dill.brine-alpha-input/2", "repository": "umayr/dill",
                        "archiveMember": ARCHIVE_MEMBER, "pklVersion": "0.31.1",
                        "fixtureVersion": "dill.alpha-qualification/2", "platform": "linux-amd64",
                        "jointApplicationAcceptance": "not-run"}.items():
        require(pin[name] == value)
    require(pin["artifactName"] == "alpha-baseline-" + pin["candidateCommit"] + "-docker")
    expiry(pin["expiresAt"])


def metadata_matches(metadata, pin, size, now):
    require(type(metadata) is dict)
    for key, expected in (("id", pin["artifactId"]), ("name", pin["artifactName"]),
                          ("size_in_bytes", pin["artifactSize"]),
                          ("digest", "sha256:" + pin["zipSha256"]),
                          ("expired", False), ("expires_at", pin["expiresAt"])):
        require(type(metadata.get(key)) is type(expected) and metadata[key] == expected)
    require(now.tzinfo is not None and now < expiry(pin["expiresAt"]) and size == pin["artifactSize"])
    run = metadata.get("workflow_run")
    require(type(run) is dict and type(run.get("id")) is int and run["id"] == pin["qualificationRun"]
            and run.get("head_sha") == pin["candidateCommit"] and run.get("head_branch") == "main")


def verified_archive(path, metadata, pin, now):
    metadata_matches(metadata, pin, path.stat().st_size, now)
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1048576), b""):
            h.update(block)
    require(h.hexdigest() == pin["zipSha256"])
    with zipfile.ZipFile(path) as z:
        entries = z.infolist()
        require(len(entries) == 56 and {e.filename for e in entries} == OUTER_FILES)
        for entry in entries:
            require(safe_name(entry.filename) and stat.S_IFMT(entry.external_attr >> 16) in (0, stat.S_IFREG)
                    and not entry.is_dir() and not entry.flag_bits & 1)
        member = z.getinfo(ARCHIVE_MEMBER)
        require(0 < member.file_size <= MAX_ARCHIVE)
        payload = z.read(member)
    require(digest(payload) == pin["archiveSha256"])
    return payload


def canonical_archive(data):
    out = io.BytesIO()
    with gzip.GzipFile(filename="", mode="wb", fileobj=out, mtime=0) as gz:
        with tarfile.open(fileobj=gz, mode="w") as t:
            for name in sorted(data):
                info = tarfile.TarInfo(name)
                info.mode = 0o755 if name in {"bin/dill", "bin/pkl"} else 0o644
                info.size = len(data[name])
                t.addfile(info, io.BytesIO(data[name]))
    return out.getvalue()


def verified_payload(payload, pin):
    with gzip.GzipFile(fileobj=io.BytesIO(payload)) as g:
        raw = g.read(MAX_TAR + 1)
    require(len(raw) <= MAX_TAR)
    data = {}
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as t:
        members = t.getmembers()
        require(len(members) == 32 and [m.name for m in members] == sorted(EXPECTED_FILES | {"handoff.json"}))
        total = 0
        for m in members:
            require(safe_name(m.name) and m.isfile() and m.type == tarfile.REGTYPE and not m.linkname
                    and not m.pax_headers and m.uid == m.gid == 0 and m.uname == m.gname == "" and m.mtime == 0)
            require(m.mode == (0o755 if m.name in {"bin/dill", "bin/pkl"} else 0o644))
            total += m.size
            require(0 <= m.size <= MAX_PAYLOAD and total <= MAX_PAYLOAD)
            data[m.name] = t.extractfile(m).read()
    require(canonical_archive(data) == payload)
    require(digest(data["handoff.json"]) == pin["manifestSha256"])
    m = fields(json_document(data["handoff.json"]), {"version", "status", "candidateCommit", "binarySha256", "fixtureVersion", "files", "caller", "downstreamBrineAcceptance"})
    for key, expected in {"version": "dill.alpha-handoff/2", "status": "verified",
                          "candidateCommit": pin["candidateCommit"], "binarySha256": pin["binarySha256"],
                          "fixtureVersion": pin["fixtureVersion"], "downstreamBrineAcceptance": "not-run"}.items():
        require(type(m[key]) is str and m[key] == expected)
    hashes = fields(m["files"], EXPECTED_FILES)
    for name, value in hashes.items():
        require(type(value) is str and re.fullmatch(r"[0-9a-f]{64}", value) and digest(data[name]) == value)
    for name, key in (("bin/dill", "binarySha256"), ("bin/pkl", "pklSha256"), ("dill.pkl", "schemaSha256")):
        require(hashes[name] == pin[key])
    caller = fields(m["caller"], {"contract", "source", "outcome", "effects", "qualifiedIdentityDigest"})
    for key, value in {"contract": "dill.operation/1", "source": "brine", "outcome": "succeeded", "effects": "complete"}.items():
        require(type(caller[key]) is str and caller[key] == value)
    require(type(caller["qualifiedIdentityDigest"]) is str and re.fullmatch(r"sha256:[0-9a-f]{64}", caller["qualifiedIdentityDigest"]))
    catalog = json_document(data["test/qualification/v1/cases.json"])
    require(type(catalog) is dict and catalog.get("version") == "dill.alpha-qualification/2")
    cases = catalog.get("cases")
    require(type(cases) is list and len(cases) == 30 and all(type(r) is dict and type(r.get("id")) is str for r in cases))
    require({r["id"] for r in cases} == EXPECTED_CASES)
    # Catalogue membership is consistency of already accepted bytes, NOT a new
    # runtime pass. The copied caller's old daemon identity is never exported.
    return data


def extract_verified(data, output):
    require(output.is_absolute() and output.name not in ("", ".", ".."))
    parent = output.parent
    require(parent.resolve(strict=True) == parent and parent.is_dir() and parent.stat().st_uid == os.getuid()
            and parent.stat().st_mode & 0o077 == 0)
    output.mkdir(mode=0o700)
    for name in sorted(data):
        destination = output / name
        directory = output
        for component in PurePosixPath(name).parts[:-1]:
            directory = directory / component
            directory.mkdir(mode=0o700, exist_ok=True)
        fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o400)
        with os.fdopen(fd, "wb") as f:
            f.write(data[name])
            os.fchmod(f.fileno(), 0o500 if name in {"bin/dill", "bin/pkl"} else 0o400)


def prepare(path, metadata, pin, output=None, *, now=None):
    validate_pin(pin)
    def current():
        return datetime.now(timezone.utc) if now is None else now
    payload = verified_archive(path, metadata, pin, current())
    data = verified_payload(payload, pin)
    require(current() < expiry(pin["expiresAt"]))
    if output is not None:
        extract_verified(data, output)
    return {"version": "dill.brine-alpha-input-result/2", "status": "verified-dill-input",
            **{k: pin[k] for k in ("candidateCommit", "sourceTree", "binarySha256", "archiveSha256", "manifestSha256", "pklSha256", "schemaSha256", "platform")},
            "jointApplicationAcceptance": "not-run", "receivingTargetQualified": False,
            "payloadExtracted": output is not None}


class Parser(argparse.ArgumentParser):
    def error(self, message):
        raise Rejected("DILL_PAIR_INPUT_REJECTED")


def main():
    try:
        p = Parser(description=__doc__)
        p.add_argument("--artifact-zip", type=Path, required=True)
        p.add_argument("--artifact-metadata", type=Path, required=True)
        mode = p.add_mutually_exclusive_group(required=True)
        mode.add_argument("--validate-only", action="store_true")
        mode.add_argument("--output", type=Path)
        a = p.parse_args()
        pin = json_document(PIN_FILE.read_bytes())
        metadata = json_document(a.artifact_metadata.read_bytes())
        result = prepare(a.artifact_zip, metadata, pin, a.output)
    except (OSError, ValueError, KeyError, TypeError, AttributeError, EOFError, RuntimeError, OverflowError, zlib.error, tarfile.TarError, zipfile.BadZipFile):
        print("DILL_PAIR_INPUT_REJECTED", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
