#!/usr/bin/env python3
"""Seal an explicit public file set. Never extract/execute candidates or copy HOME/state/logs."""
import argparse
import hashlib
import gzip
import io
import importlib.util
import json
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tarfile

SPEC = importlib.util.spec_from_file_location("qualify_alpha", Path(__file__).with_name("qualify-alpha.py"))
q = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(q)


def strict_json(data):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError("duplicate public JSON key")
            result[key] = value
        return result
    def invalid(_):
        raise ValueError("nonfinite public JSON")
    return json.loads(data.decode("utf-8"), object_pairs_hook=pairs, parse_constant=invalid)


def regular(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode):
        raise ValueError("public source is not a regular non-link file")
    return info


def public_sources(pkl):
    sources = q.handoff_sources(q.ROOT / "bin/dill", pkl)
    # Native Pkl is public only inside the separately verified archive.
    del sources["bin/pkl"]
    tracked = subprocess.run(["git", "ls-files", "--", "test/integration"], cwd=q.ROOT,
                             check=True, capture_output=True, timeout=10).stdout.decode().splitlines()
    actual = {str(p.relative_to(q.ROOT)) for p in (q.ROOT / "test/integration").rglob("*") if not p.is_dir()}
    if actual != set(tracked) or any(not name.endswith("_test.go") for name in tracked):
        raise ValueError("integration public directory contains extra or missing files")
    for name in tracked + ["scripts/qualify-alpha.py", "scripts/test_qualify_alpha.py",
                           "scripts/stage-alpha-artifact.py", "scripts/test_stage_alpha_artifact.py"]:
        sources[name] = q.ROOT / name
    return sources


def canonical_archive_digest(originals, manifest):
    digest = hashlib.sha256()
    class Sink:
        def write(self, data):
            digest.update(data)
            return len(data)
        def flush(self):
            pass
    with gzip.GzipFile(filename="", mode="wb", fileobj=Sink(), mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w") as archive:
            for name in sorted(set(originals) | {"handoff.json"}):
                header = tarfile.TarInfo(name)
                header.mode = 0o755 if name.startswith("bin/") else 0o644
                header.size = len(manifest) if name == "handoff.json" else regular(originals[name]).st_size
                if name == "handoff.json":
                    archive.addfile(header, io.BytesIO(manifest))
                else:
                    with originals[name].open("rb") as source:
                        archive.addfile(header, source)
    return digest.hexdigest()


def verify_archive(path, evidence, pkl):
    originals = q.handoff_sources(q.ROOT / "bin/dill", pkl)
    for source in originals.values():
        regular(source)
    hashes = {name: q.sha256(source) for name, source in originals.items()}
    supply = evidence.get("dillArtifactSupply", {})
    expected = {"version": "dill.alpha-handoff/2", "status": "verified",
                "candidateCommit": evidence["source"]["commit"], "binarySha256": hashes["bin/dill"],
                "fixtureVersion": "dill.alpha-qualification/2", "files": hashes,
                "caller": {"contract": "dill.operation/1", "source": "brine", "outcome": "succeeded",
                           "effects": "complete", "qualifiedIdentityDigest": evidence["qualifiedProvisioning"]["expectedIdentityDigest"]},
                "downstreamBrineAcceptance": "not-run"}
    if (supply.get("status") != "verified" or supply.get("archiveSha256") != q.sha256(path)
            or supply.get("candidateCommit") != expected["candidateCommit"]
            or supply.get("binarySha256") != hashes["bin/dill"]):
        raise ValueError("archive is not the recorded copied candidate")
    manifest_bytes = None
    with tarfile.open(path, "r:gz") as archive:
        members = archive.getmembers()
        if len(members) != 32 or {m.name for m in members} != set(originals) | {"handoff.json"}:
            raise ValueError("archive member set is not the frozen 32 files")
        for member in members:
            if (not member.isfile() or member.linkname or member.uid or member.gid or member.uname or member.gname
                    or member.mtime != 0 or member.mode != (0o755 if member.name.startswith("bin/") else 0o644)):
                raise ValueError("archive header is not the canonical regular public form")
            stream = archive.extractfile(member)
            if member.name == "handoff.json":
                if member.size > 65536:
                    raise ValueError("handoff manifest exceeds bound")
                data = stream.read()
                manifest_bytes = data
                if strict_json(data) != expected or hashlib.sha256(data).hexdigest() != supply.get("manifestSha256"):
                    raise ValueError("manifest differs from exact source and supplied target")
            else:
                if member.size != regular(originals[member.name]).st_size:
                    raise ValueError("archive payload size differs from pinned public input")
                digest = hashlib.sha256()
                for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                    digest.update(chunk)
                if digest.hexdigest() != hashes[member.name]:
                    raise ValueError("archive payload differs from pinned public input")
    if q.sha256(path) != canonical_archive_digest(originals, manifest_bytes):
        raise ValueError("archive is not canonical or contains hidden/trailing data")


def stage(engine, commit, pkl, destination):
    if engine not in {"docker", "podman"} or not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("exact engine and immutable candidate commit required")
    output = q.ROOT / ("bin/alpha-evidence-" + engine)
    if output.is_symlink() or not output.is_dir():
        raise ValueError("bounded evidence directory missing")
    allowed = {"home", "handoff", "evidence.json", "handoff.tar.gz"}
    for entry in output.iterdir():
        if entry.name not in allowed or entry.is_symlink():
            raise ValueError("unexpected evidence entry; refusing public upload")
        if entry.name in {"home", "handoff"} and not entry.is_dir():
            raise ValueError("private directory has invalid metadata")
    bundle = output / "handoff"
    if bundle.exists():
        expected = q.PUBLIC_HANDOFF_FILES | {"handoff.json"}
        for entry in bundle.rglob("*"):
            relative = str(entry.relative_to(bundle))
            if entry.is_symlink() or (entry.is_file() and relative not in expected):
                raise ValueError("unexpected private/nonallowlisted handoff entry")
            if entry.is_dir() and not any(name.startswith(relative + "/") for name in expected):
                raise ValueError("unexpected handoff directory")
            if not entry.is_dir():
                regular(entry)
    evidence_path = output / "evidence.json"
    if regular(evidence_path).st_size > 1024 * 1024:
        raise ValueError("bounded evidence exceeds public limit")
    evidence = strict_json(evidence_path.read_bytes())
    allowed_keys = {"version", "createdAt", "qualification", "engine", "target", "disposableAttested",
                    "operationContractRequested", "qualifiedRequested", "qualifiedProvisioning", "dillArtifactSupply",
                    "downstreamBrineAcceptance", "commands", "cases", "gaps", "source", "binary", "pkl", "schema",
                    "fixtures", "fixtureVersion", "preparedProfiles", "operationCapabilities", "preflightCapabilities",
                    "engineVersion", "checksumsAfter", "integrityVerified", "baselinePassed", "acceptance"}
    if not isinstance(evidence, dict) or not set(evidence) <= allowed_keys:
        raise ValueError("unexpected public attestation fields")
    regular(q.ROOT / "bin/dill")
    if (evidence.get("version") != "dill.alpha-evidence/2" or evidence.get("source", {}).get("commit") != commit
            or evidence.get("engine") != engine or evidence.get("binary", {}).get("sha256") != q.sha256(q.ROOT / "bin/dill")):
        raise ValueError("public evidence does not bind exact engine/source/binary")
    sources = public_sources(pkl)
    sources[str(evidence_path.relative_to(q.ROOT))] = evidence_path
    archive = output / "handoff.tar.gz"
    if archive.exists():
        regular(archive)
        verify_archive(archive, evidence, pkl)
        sources[str(archive.relative_to(q.ROOT))] = archive
    elif evidence.get("qualification") == "complete":
        raise ValueError("complete claim missing its exact supply archive")
    for source in sources.values():
        regular(source)
    # Validate everything BEFORE creating an upload directory. Only explicit
    # names are copied; private HOME/state and raw stdout/stderr are never read.
    destination.mkdir(mode=0o700, parents=False, exist_ok=False)
    for name, source in sorted(sources.items()):
        target = destination / name
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        shutil.copyfile(source, target)
        target.chmod(0o500 if name == "bin/dill" else 0o400)
    actual = {str(p.relative_to(destination)) for p in destination.rglob("*") if p.is_file()}
    if actual != set(sources):
        raise ValueError("sealed public set changed")
    return actual


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--engine", required=True, choices=["docker", "podman"])
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--pkl", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        names = stage(args.engine, args.candidate_sha, args.pkl, args.output)
    except (ValueError, OSError, KeyError, TypeError, subprocess.SubprocessError, tarfile.TarError):
        parser.exit(1, "Closed public collection failed; raw/private diagnostics withheld.\n")
    print("Sealed %d explicit public files; no downstream Brine acceptance." % len(names))


if __name__ == "__main__":
    main()
