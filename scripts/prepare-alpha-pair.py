#!/usr/bin/env python3
"""Verify ONE externally accepted Dill input; never qualify a Brine application.

No network, candidate execution, engine calls or latest/build fallback. The pin is
reviewed workflow-driver source, NOT supplied by the application/archive. Only an
explicit --output extracts the verified public payload into a new private folder.
Use --validate-only for local, non-extracting inspection. Native CI provisioning
must precede every eventual candidate executable, including version probes.
"""

import argparse
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

ROOT = Path(__file__).resolve().parents[1]
PIN_FILE = ROOT / "test/qualification/pair/v1/dill-input.json"
MAX_ARCHIVE = 256 * 1024 * 1024
MAX_PAYLOAD = 256 * 1024 * 1024
EXPECTED_FILES = frozenset({
    "bin/dill", "bin/pkl", "dill.pkl", "docs/alpha-candidate-handoff.md",
    "internal/loader/pkl-toolchain.json",
    *("internal/operation/testdata/" + name + ".json" for name in (
        "manifest", "success", "failure", "unsupported-operation",
        "unsupported-version", "qualified-success", "qualified-cleanup-failure")),
    "test/qualification/v1/cases.json", "test/qualification/v1/valid.pkl",
    "test/qualification/v1/invalid-graph.pkl",
})


class Rejected(ValueError):
    """Fixed, non-sensitive input rejection; no raw archive/path diagnostics."""


def require(condition, code):
    if not condition:
        raise Rejected(code)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def file_digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def unique_object(pairs):
    value = {}
    for key, entry in pairs:
        require(key not in value, "DUPLICATE_JSON_KEY")
        value[key] = entry
    return value


def json_document(data):
    def invalid_constant(_):
        raise Rejected("INVALID_JSON_CONSTANT")
    return json.loads(data, object_pairs_hook=unique_object, parse_constant=invalid_constant)


def safe_name(name):
    return (isinstance(name, str) and bool(name) and "\\" not in name
            and not name.startswith("/") and ":" not in name
            and all(part not in ("", ".", "..") for part in name.split("/"))
            and str(PurePosixPath(name)) == name)


def metadata_matches(metadata, pin, size):
    require(type(metadata) is dict, "INVALID_ARTIFACT_METADATA")
    for key, expected in (("id", pin["artifactId"]), ("name", pin["artifactName"]),
                          ("size_in_bytes", pin["artifactSize"]),
                          ("digest", "sha256:" + pin["zipSha256"]), ("expired", False)):
        require(type(metadata.get(key)) is type(expected) and metadata[key] == expected,
                "ARTIFACT_METADATA_MISMATCH")
    require(size == pin["artifactSize"], "ARTIFACT_SIZE_MISMATCH")
    run = metadata.get("workflow_run")
    require(type(run) is dict and type(run.get("id")) is int
            and run["id"] == pin["qualificationRun"]
            and run.get("head_sha") == pin["candidateCommit"]
            and run.get("head_branch") == "main", "ARTIFACT_RUN_MISMATCH")


def verified_archive(archive_path, metadata, pin):
    """Return the pinned public tar bytes only; do not extract the outer ZIP."""
    metadata_matches(metadata, pin, archive_path.stat().st_size)
    require(file_digest(archive_path) == pin["zipSha256"], "ZIP_CHECKSUM_MISMATCH")
    with zipfile.ZipFile(archive_path) as z:
        names = set()
        for entry in z.infolist():
            require(safe_name(entry.filename) and entry.filename not in names,
                    "UNSAFE_ZIP_MEMBER")
            names.add(entry.filename)
            mode = entry.external_attr >> 16
            require(stat.S_IFMT(mode) in (0, stat.S_IFREG)
                    and not entry.is_dir() and not entry.flag_bits & 1,
                    "UNSAFE_ZIP_MEMBER")
        require(pin["archiveMember"] in names, "MISSING_PUBLIC_ARCHIVE")
        entry = z.getinfo(pin["archiveMember"])
        require(0 < entry.file_size <= MAX_ARCHIVE, "ARCHIVE_SIZE_LIMIT")
        payload = z.read(entry)
    require(digest(payload) == pin["archiveSha256"], "ARCHIVE_CHECKSUM_MISMATCH")
    return payload


def verified_payload(payload, pin):
    """Validate exact regular members and every hash without filesystem extraction."""
    with tarfile.open(fileobj=io.BytesIO(payload), mode="r:gz") as t:
        members = t.getmembers()
        names = set()
        total = 0
        for m in members:
            require(safe_name(m.name) and m.name not in names, "UNSAFE_TAR_MEMBER")
            names.add(m.name)
            require(m.isfile() and m.type in (tarfile.REGTYPE, tarfile.AREGTYPE)
                    and m.uid == 0 and m.gid == 0 and m.uname == "" and m.gname == ""
                    and m.mtime == 0 and not m.pax_headers, "UNSAFE_TAR_METADATA")
            require(m.mode == (0o755 if m.name in ("bin/dill", "bin/pkl") else 0o644),
                    "UNSAFE_TAR_MODE")
            total += m.size
            require(0 <= m.size <= MAX_PAYLOAD and total <= MAX_PAYLOAD, "PAYLOAD_SIZE_LIMIT")
        require(names == EXPECTED_FILES | {"handoff.json"}, "PUBLIC_ALLOWLIST_MISMATCH")
        manifest_bytes = t.extractfile("handoff.json").read()
        require(digest(manifest_bytes) == pin["manifestSha256"], "MANIFEST_CHECKSUM_MISMATCH")
        manifest = json_document(manifest_bytes)
        require(type(manifest) is dict, "INVALID_HANDOFF_MANIFEST")
        for key, expected in (("version", "dill.alpha-handoff/1"), ("status", "verified"),
                              ("candidateCommit", pin["candidateCommit"]),
                              ("fixtureVersion", pin["fixtureVersion"]),
                              ("binarySha256", pin["binarySha256"]),
                              ("downstreamBrineAcceptance", "not-run")):
            require(manifest.get(key) == expected, "HANDOFF_IDENTITY_MISMATCH")
        hashes = manifest.get("files")
        require(type(hashes) is dict and set(hashes) == EXPECTED_FILES,
                "MANIFEST_ALLOWLIST_MISMATCH")
        for name in sorted(EXPECTED_FILES):
            require(type(hashes[name]) is str and re.fullmatch(r"[0-9a-f]{64}", hashes[name]),
                    "INVALID_PAYLOAD_CHECKSUM")
            data = t.extractfile(name).read()
            require(digest(data) == hashes[name], "PAYLOAD_CHECKSUM_MISMATCH")
        for name, key in (("bin/dill", "binarySha256"), ("bin/pkl", "pklSha256"),
                          ("dill.pkl", "schemaSha256")):
            require(hashes[name] == pin[key], "PINNED_PAYLOAD_MISMATCH")
        caller = manifest.get("caller")
        require(type(caller) is dict and caller.get("contract") == "dill.operation/1"
                and caller.get("source") == "brine" and caller.get("outcome") == "succeeded"
                and caller.get("effects") == "complete"
                and type(caller.get("qualifiedIdentityDigest")) is str
                and re.fullmatch(r"sha256:[0-9a-f]{64}", caller["qualifiedIdentityDigest"]),
                "INVALID_PROVIDER_RECEIPT")
        # The old CI caller receipt is provenance ONLY. Never export its daemon
        # identity as an expected receiving-host identity or execution permit.
        return manifest


def extract_verified(payload, output):
    """Called only after all pinned bytes validate; new private destination only."""
    require(output.is_absolute() and output.name not in ("", ".", ".."),
            "INVALID_OUTPUT_DIRECTORY")
    parent = output.parent
    require(parent.resolve(strict=True) == parent and parent.is_dir()
            and parent.stat().st_uid == os.getuid() and parent.stat().st_mode & 0o077 == 0,
            "OUTPUT_PARENT_NOT_PRIVATE")
    output.mkdir(mode=0o700)  # Existing path/symlink is never accepted or overwritten.
    with tarfile.open(fileobj=io.BytesIO(payload), mode="r:gz") as t:
        for name in sorted(EXPECTED_FILES | {"handoff.json"}):
            destination = output / name
            destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            with destination.open("xb") as target:
                source = t.extractfile(name)
                for chunk in iter(lambda: source.read(1024 * 1024), b""):
                    target.write(chunk)
            destination.chmod(0o500 if name in ("bin/dill", "bin/pkl") else 0o400)


def prepare(archive_path, metadata, pin, output=None):
    payload = verified_archive(archive_path, metadata, pin)
    verified_payload(payload, pin)
    if output is not None:
        extract_verified(payload, output)
    return {"version": "dill.brine-alpha-input-result/1", "status": "verified-dill-input",
            "candidateCommit": pin["candidateCommit"], "sourceTree": pin["sourceTree"],
            "binarySha256": pin["binarySha256"], "archiveSha256": pin["archiveSha256"],
            "manifestSha256": pin["manifestSha256"], "pklSha256": pin["pklSha256"],
            "schemaSha256": pin["schemaSha256"], "platform": pin["platform"],
            "jointApplicationAcceptance": "not-run", "receivingTargetQualified": False,
            "payloadExtracted": output is not None}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--artifact-zip", type=Path, required=True)
    p.add_argument("--artifact-metadata", type=Path, required=True)
    mode = p.add_mutually_exclusive_group(required=True)
    mode.add_argument("--validate-only", action="store_true")
    mode.add_argument("--output", type=Path)
    args = p.parse_args()
    try:
        pin = json_document(PIN_FILE.read_bytes())
        metadata = json_document(args.artifact_metadata.read_bytes())
        result = prepare(args.artifact_zip, metadata, pin, args.output)
    except (OSError, ValueError, KeyError, TypeError, tarfile.TarError, zipfile.BadZipFile):
        print("DILL_PAIR_INPUT_REJECTED", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
