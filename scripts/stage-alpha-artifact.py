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


PUBLIC_ERROR_CLASSES = frozenset({
    "ValueError", "KeyError", "JSONDecodeError", "KeyboardInterrupt", "TimeoutExpired", "SystemExit",
    "OSError", "FileNotFoundError", "PermissionError", "InterruptedError", "ProcessLookupError",
    "ChildProcessError", "BlockingIOError", "IsADirectoryError", "NotADirectoryError", "BrokenPipeError",
    "ConnectionError", "ConnectionAbortedError", "ConnectionRefusedError", "ConnectionResetError", "TimeoutError", "RuntimeError",
})


def fields(value, allowed, required=()):
    if not isinstance(value, dict) or set(value) - set(allowed) or not set(required) <= set(value):
        raise ValueError("unexpected/missing nested public record fields")
    return value


def text(value, maximum=4096):
    if not isinstance(value, str) or len(value) > maximum or not re.fullmatch(r"[ -~]*", value):
        raise ValueError("public string exceeds its typed printable bound")


def integer(value, minimum=0, maximum=2**40):
    if type(value) is not int or not minimum <= value <= maximum:
        raise ValueError("public integer is null/bool/coerced/outside bound")


def hashes(value, keys):
    fields(value, keys, keys)
    if any(not isinstance(x, str) or not re.fullmatch(r"[0-9a-f]{64}", x) for x in value.values()):
        raise ValueError("invalid public checksum record")


def array(value, maximum):
    if not isinstance(value, list) or len(value) > maximum:
        raise ValueError("public array exceeds its typed bound")


def validate_commands(commands, evidence, manifest):
    array(commands, 64)
    fixed = {("git", "rev-parse", "HEAD"): 0, ("git", "status", "--porcelain", "--untracked-files=all"): 0,
             ("<candidate>", "version"): 0, ("go", "version", "-m", "-json", "<candidate>"): 0,
             ("<pkl>", "--version"): 0,
             ("<candidate>", "operation-capabilities", "--format", "json"): 0,
             ("<candidate>", "preflight-capabilities", "--format", "json"): 0}
    for name, code in q.REQUIRED_OFFLINE_FIXTURES.values():
        fixed[("<candidate>", "-f", "<repo>/test/qualification/v1/" + name, "validate")] = code
    engine = evidence["engine"]
    if evidence.get("target") is not None:
        fixed[(engine, "--host" if engine == "docker" else "--url", "<engine-socket>", "version", "--format", "{{json .}}" if engine == "docker" else "json")] = 0
    modes = {"engine"}
    if evidence.get("operationContractRequested") is True:
        modes.add("operation-engine")
    if evidence.get("qualifiedRequested") is True:
        modes.add("qualified-engine")
    # Profiles are optional observations, never authority to clear required IDs.
    for profiles in (False, True):
        selected = [c for c in manifest["cases"] if c["mode"] in modes]
        if profiles and engine == "docker":
            selected += manifest.get("preparedProfiles", [])
        names = list(dict.fromkeys(n for c in selected for n in q.runtime_tests(c)))
        fixed[("make", "integration-test", q.make_test_selector(names))] = 0
    allowed = {"argv", "expectedExit", "passed", "exit", "stdoutSha256", "stderrSha256", "stdoutBytes", "stderrBytes", "failure", "cleanupFailure"}
    for command in commands:
        fields(command, allowed, {"argv", "expectedExit", "passed"})
        array(command["argv"], 32)
        for arg in command["argv"]:
            text(arg)
        key = tuple(command["argv"])
        integer(command["expectedExit"], 0, 255)
        if key not in fixed or command["expectedExit"] != fixed[key] or type(command["passed"]) is not bool:
            raise ValueError("command is not the fixed redacted public grammar")
        for name in ("stdoutBytes", "stderrBytes"):
            if name in command: integer(command[name])
        for name in ("stdoutSha256", "stderrSha256"):
            if name in command: hashes({name: command[name]}, {name})
        if "exit" in command:
            integer(command["exit"], -128, 255)
            for field in ("stdoutBytes", "stderrBytes"):
                integer(command.get(field))
            hashes({k: command.get(k) for k in ("stdoutSha256", "stderrSha256")}, {"stdoutSha256", "stderrSha256"})
            if command["passed"] != (command["exit"] == command["expectedExit"]):
                raise ValueError("command outcome contradicts actual exit")
        elif command["passed"] or "failure" not in command:
            raise ValueError("nonterminal command cannot claim a passing observation")
        for field in ("failure", "cleanupFailure"):
            if field in command and (command[field] not in PUBLIC_ERROR_CLASSES or command["passed"]):
                raise ValueError("unknown/freeform diagnostic class")


def validate_public_evidence(evidence):
    allowed = {"version", "createdAt", "qualification", "engine", "target", "disposableAttested",
               "operationContractRequested", "qualifiedRequested", "qualifiedProvisioning", "dillArtifactSupply",
               "downstreamBrineAcceptance", "commands", "cases", "gaps", "source", "binary", "pkl", "schema",
               "fixtures", "fixtureVersion", "preparedProfiles", "operationCapabilities", "preflightCapabilities",
               "engineVersion", "checksumsAfter", "integrityVerified", "baselinePassed", "acceptance", "error"}
    fields(evidence, allowed, {"version", "qualification", "engine", "source", "binary"})
    manifest = strict_json((q.FIXTURES / "cases.json").read_bytes())
    for name in ("disposableAttested", "operationContractRequested", "qualifiedRequested", "integrityVerified", "baselinePassed"):
        if name in evidence and type(evidence[name]) is not bool:
            raise ValueError("null/coerced public boolean")
    if "error" in evidence and evidence["error"] not in PUBLIC_ERROR_CLASSES:
        raise ValueError("unknown public error class")
    if evidence.get("qualification") not in {"complete", "incomplete"}:
        raise ValueError("unknown qualification marker")
    for name in ("version", "createdAt", "fixtureVersion", "target"):
        if name in evidence:
            text(evidence[name], 1024 if name == "target" else 128)
    if "createdAt" in evidence and not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,6})?\+00:00", evidence["createdAt"]): raise ValueError("invalid public timestamp")
    if "fixtureVersion" in evidence and evidence["fixtureVersion"] != "dill.alpha-qualification/2": raise ValueError("wrong fixture policy")
    for name, spec in {
        "source": {"commit": "str", "dirty": "bool", "statusSha256": "hash"},
        "binary": {"sha256": "hash", "version": "str", "goVersion": "str", "goOS": "str", "goArch": "str", "buildRevision": "str", "buildModified": "str"},
        "pkl": {"version": "str", "sha256": "hash", "toolchainSha256": "hash"},
        "schema": {"version": "str", "sha256": "hash"},
        "qualifiedProvisioning": {"source": "str", "layout": "str", "expectedIdentityDigest": "digest", "privateStateRootVerified": "bool"},
    }.items():
        if name not in evidence or (name == "qualifiedProvisioning" and evidence[name] is None and not evidence.get("qualifiedRequested")):
            continue
        value = fields(evidence[name], spec, spec)
        for key, kind in spec.items():
            item = value[key]
            if kind == "bool":
                if type(item) is not bool: raise ValueError("null/coerced metadata boolean")
            elif kind in {"hash", "digest"}:
                pattern = r"[0-9a-f]{64}" if kind == "hash" else r"sha256:[0-9a-f]{64}"
                if not isinstance(item, str) or not re.fullmatch(pattern, item): raise ValueError("invalid metadata digest")
            else:
                text(item, 256)
    if not re.fullmatch(r"[0-9a-f]{40}", evidence["source"]["commit"]): raise ValueError("invalid source commit")
    if "buildRevision" in evidence["binary"] and not re.fullmatch(r"[0-9a-f]{40}", evidence["binary"]["buildRevision"]): raise ValueError("invalid embedded revision")
    if "buildModified" in evidence["binary"] and evidence["binary"]["buildModified"] not in {"true", "false"}: raise ValueError("invalid embedded modified flag")
    if "goOS" in evidence["binary"] and evidence["binary"]["goOS"] not in {"linux", "darwin"}: raise ValueError("unsupported recorded OS")
    if "goArch" in evidence["binary"] and evidence["binary"]["goArch"] not in {"amd64", "arm64"}: raise ValueError("unsupported recorded architecture")
    if "engineVersion" in evidence:
        value = fields(evidence["engineVersion"], {"engine", "clientVersion", "serverVersion"}, {"engine", "clientVersion", "serverVersion"})
        if value["engine"] != evidence["engine"]: raise ValueError("wrong engine version origin")
        for key in ("clientVersion", "serverVersion"):
            if not isinstance(value[key], str) or not re.fullmatch(r"[ -~]{1,128}", value[key]): raise ValueError("actual bounded versions required")
    if "preflightCapabilities" in evidence:
        q.preflight_capabilities(evidence["preflightCapabilities"])
    if "operationCapabilities" in evidence:
        q.operation_capabilities(evidence["operationCapabilities"], evidence["engine"])
    if "fixtures" in evidence:
        hashes(evidence["fixtures"], {n for n in q.PUBLIC_HANDOFF_FILES if n.startswith("test/qualification/v1/")})
    if "checksumsAfter" in evidence:
        hashes(evidence["checksumsAfter"], {"binary", "schema", "pkl"})
    if "dillArtifactSupply" in evidence:
        supply = evidence["dillArtifactSupply"]
        keys = {"status"} if supply.get("status") == "not-run" else {"status", "candidateCommit", "binarySha256", "manifestSha256", "archiveSha256", "scope"}
        fields(supply, keys, keys)
        if supply["status"] not in {"not-run", "verified"}: raise ValueError("invalid supply status")
        if supply["status"] == "verified":
            hashes({k: supply[k] for k in ("binarySha256", "manifestSha256", "archiveSha256")}, {"binarySha256", "manifestSha256", "archiveSha256"})
            if supply["candidateCommit"] != evidence["source"]["commit"] or supply["scope"] != "Dill-side byte-identical copied qualified caller; downstream Brine application not run": raise ValueError("unknown supply scope/origin")
    if "downstreamBrineAcceptance" in evidence:
        if evidence["downstreamBrineAcceptance"] != {"status": "not-run", "scope": "Dill-side artifact supply is not Brine application E2E"}: raise ValueError("unknown downstream acceptance")
    definitions = {c["id"]: c for c in manifest["cases"] + manifest.get("preparedProfiles", [])}
    for name in ("cases", "preparedProfiles"):
        if name not in evidence: continue
        array(evidence[name], 30)
        for result in evidence[name]:
            fields(result, {"id", "status", "requires", "reason", "test", "tests", "evidenceKind", "candidateCommit", "binarySha256", "target", "scope"}, {"id", "status"})
            definition = definitions.get(result["id"])
            if definition is None or result["status"] not in {"passed", "failed", "blocked", "not-run"}: raise ValueError("unknown public case/status")
            for key in ("reason", "scope"):
                if key in result and result[key] != definition.get(key, "selected executable disposable-engine consumer"): raise ValueError("noncatalog case metadata")
            if "requires" in result:
                array(result["requires"], 3)
                for value in result["requires"]: integer(value, 77, 79)
                if result["requires"] != definition.get("requires"): raise ValueError("wrong prerequisites")
            if "test" in result and result["test"] != definition.get("test"): raise ValueError("wrong runtime selector")
            if "tests" in result and result["tests"] != q.runtime_tests(definition): raise ValueError("wrong test array")
            if "evidenceKind" in result and result["evidenceKind"] not in {"offline", "real-engine", "real-qualified-engine", "real-profile-engine"}: raise ValueError("unknown evidence kind")
            for key, expected in (("candidateCommit", evidence["source"]["commit"]), ("binarySha256", evidence["binary"]["sha256"]), ("target", evidence.get("target"))):
                if key in result and result[key] != expected: raise ValueError("wrong case source/target")
    if "gaps" in evidence:
        array(evidence["gaps"], 2)
        if any(x not in {"development-only dirty source/binary; not transferable candidate evidence", "Final required-case acceptance is incomplete; baseline success is not issue #80 completion."} for x in evidence["gaps"]): raise ValueError("freeform public gap")
    if "commands" in evidence:
        validate_commands(evidence["commands"], evidence, manifest)
    if "acceptance" in evidence:
        acceptance = fields(evidence["acceptance"], {"complete", "reasons", "requiredCases", "profile"}, {"complete", "reasons", "requiredCases", "profile"})
        if type(acceptance["complete"]) is not bool: raise ValueError("invalid complete boolean")
        integer(acceptance["requiredCases"], 30, 30)
        profile = fields(acceptance["profile"], {"engine", "layout", "limitations"}, {"engine", "layout", "limitations"})
        array(profile["limitations"], len(q.PROFILE_LIMITATIONS))
        if profile != manifest["qualifiedProfile"]: raise ValueError("unknown supported profile")
        array(acceptance["reasons"], 32)
        if acceptance != q.final_acceptance(evidence, manifest): raise ValueError("acceptance differs from independent fixed policy")
    elif evidence["qualification"] == "complete":
        raise ValueError("complete evidence missing closed acceptance record")
    if evidence["qualification"] == "complete" and not evidence["acceptance"]["complete"]:
        raise ValueError("complete marker contradicts required observations")


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
    validate_public_evidence(evidence)
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
        if source == evidence_path:
            target.write_text(json.dumps(evidence, sort_keys=True, indent=2) + "\n")
        else:
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
