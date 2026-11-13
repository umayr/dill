#!/usr/bin/env python3
"""Collect bounded, synthetic Alpha evidence. Never claims complete qualification."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import shlex
import signal
import stat
import subprocess
import sys
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parent.parent
FIXTURES = ROOT / "test/qualification/v1"
SELECTORS = {
    "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY",
    "DOCKER_CERT_PATH", "DOCKER_API_VERSION", "CONTAINER_HOST",
    "CONTAINER_CONNECTION", "CONTAINER_SSHKEY", "PODMAN_SOCKET",
    "DILL_ENGINE", "DILL_TEST_ENGINE", "DILL_ENGINE_SOCKET", "DILL_BINARY",
    "DILL_PKL_PATH", "DILL_TEST_RUN", "DILL_TEST_KEEP", "DILL_TEST_ALPHA_SECRETS", "DILL_TEST_ALPHA_OPERATIONS",
    "DILL_SECRET_POLICY", "DILL_SECRET_TARGET", "GOFLAGS", "DILL_TEST_ALPHA_GUARDRAILS",
}


# Independent review policy, never inferred from editable manifest modes.
REQUIRED_CASE_EVIDENCE = {
    "offline-valid": ("offline", "offline"),
    "offline-invalid-graph": ("offline", "offline"),
    "success-repeat-config-change": ("engine", "real-engine"),
    "changed-image": ("engine", "real-engine"),
    "invalid-config-preserves-live": ("engine", "real-engine"),
    "foreign-container-collision": ("engine", "real-engine"),
    "retained-volume": ("engine", "real-engine"),
    "cancellation": ("engine", "real-engine"),
    "failed-start-cleanup": ("engine", "real-engine"),
    "operation-contract-baseline": ("operation-engine", "real-engine"),
    "operation-outcomes": ("qualified-engine", "real-qualified-engine"),
    # Explicit positive isolated CLI --no-prune, never qualified orphan rejection.
    "no-prune": ("engine", "real-engine"),
    "bounded-storage-preflight": ("qualified-engine", "real-qualified-engine"),
    "same-stack-concurrency": ("qualified-engine", "real-qualified-engine"),
    "crash-unknown-recovery": ("qualified-engine", "real-qualified-engine"),
    "foreign-resource-inventory": ("qualified-engine", "real-qualified-engine"),
    "brine-candidate-handoff": ("qualified-engine", "real-qualified-engine"),
    "qualified-docker-profile": ("qualified-engine", "real-qualified-engine"),
    "qualified-podman-rejected": ("qualified-engine", "real-qualified-engine"),
    "different-stack-concurrency": ("qualified-engine", "real-qualified-engine"),
    "alias-concurrency": ("qualified-engine", "real-qualified-engine"),
    "corrupt-journal-rejection": ("qualified-engine", "real-qualified-engine"),
    "inspected-exact-operation-recovery": ("qualified-engine", "real-qualified-engine"),
    "qualified-image-volume-rejected": ("qualified-engine", "real-qualified-engine"),
}
FINAL_REQUIRED_CASES = frozenset(REQUIRED_CASE_EVIDENCE)
QUALIFIED_CASES = frozenset(case for case, (mode, _) in REQUIRED_CASE_EVIDENCE.items() if mode == "qualified-engine")
PROFILE_LIMITATIONS = frozenset({
    "docker-only", "native-linux-only", "prepared-local-images-only", "effective-pull-never", "all-image-volumes-denied",
    "dedicated-named-volumes-only", "no-binds", "no-secrets", "no-host-namespace",
    "no-devices-or-capabilities", "no-orphans-or-uninventoried-mounts", "qualified-teardown-unsupported",
})


def final_acceptance(evidence, manifest):
    """Evaluate only this run's pinned evidence; no waiver/optional-case mechanism.

    Synthetic unit tests of this evaluator are not runtime evidence. Runtime
    records remain trustworthy only within the reviewed candidate/test boundary.
    """
    reasons = []
    definitions = manifest.get("cases", [])
    results = evidence.get("cases", [])
    declared = [case.get("id") for case in definitions]
    observed = [case.get("id") for case in results]
    if (len(declared) != len(set(declared)) or set(declared) != FINAL_REQUIRED_CASES
            or len(observed) != len(set(observed)) or set(observed) != FINAL_REQUIRED_CASES):
        reasons.append("required-case-set-mismatch")
    if manifest.get("version") != "dill.alpha-qualification/1" or evidence.get("fixtureVersion") != manifest.get("version"):
        reasons.append("fixture-version-mismatch")
    source, binary = evidence.get("source", {}), evidence.get("binary", {})
    commit = source.get("commit", "")
    if (not re.fullmatch(r"[0-9a-f]{40}", commit) or source.get("dirty") is not False
            or binary.get("buildRevision") != commit or binary.get("buildModified") != "false"):
        reasons.append("candidate-not-clean-and-pinned")
    if (evidence.get("engine") != "docker" or binary.get("goOS") != "linux" or evidence.get("disposableAttested") is not True
            or not str(evidence.get("target", "")).startswith("unix:///")
            or not isinstance(evidence.get("engineVersion"), dict) or not evidence["engineVersion"]):
        reasons.append("disposable-docker-runtime-missing")
    expected_hashes = {key: evidence.get(key, {}).get("sha256") for key in ("binary", "schema", "pkl")}
    if (not all(isinstance(value, str) and re.fullmatch(r"[0-9a-f]{64}", value) for value in expected_hashes.values())
            or evidence.get("checksumsAfter") != expected_hashes
            or evidence.get("integrityVerified") is not True):
        reasons.append("final-integrity-evidence-missing")
    profile = manifest.get("qualifiedProfile", {})
    if (profile.get("engine") != "docker" or profile.get("layout") != "native-local-docker-v1"
            or set(profile.get("limitations", [])) != PROFILE_LIMITATIONS):
        reasons.append("supported-profile-limitations-missing")
    by_id = {case.get("id"): case for case in results}
    for case in definitions:
        case_id = case.get("id")
        result = by_id.get(case_id, {})
        mode = case.get("mode")
        expected_mode, kind = REQUIRED_CASE_EVIDENCE.get(case_id, (None, None))
        if expected_mode is None or mode != expected_mode:
            reasons.append("required-case-not-implemented")
        if result.get("status") != "passed" or mode == "blocked":
            reasons.append("required-case-not-passed")
            continue
        if result.get("evidenceKind") != kind:
            reasons.append("required-case-runtime-evidence-missing")
        if (result.get("candidateCommit") != commit or result.get("binarySha256") != binary.get("sha256")
                or result.get("target") != evidence.get("target")):
            reasons.append("required-case-candidate-or-target-mismatch")
    if (evidence.get("baselinePassed") is not True or not evidence.get("commands")
            or any(command.get("passed") is not True for command in evidence["commands"])):
        reasons.append("baseline-or-command-failed")
    return {"complete": not reasons, "reasons": sorted(set(reasons)),
            "requiredCases": len(FINAL_REQUIRED_CASES), "profile": profile}


def sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def clean_env():
    # No ambient target selectors or credentials are inherited by the candidate.
    # A private HOME also prevents CLI contexts/registry auth and Pkl config reuse.
    return {key: value for key, value in os.environ.items()
            if key in {"PATH", "TMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT",
                       "SYSTEMROOT"} and key not in SELECTORS}


def private_env(output, pkl):
    home = output / "home"
    home.mkdir(mode=0o700)
    runtime, config, docker = home / "runtime", home / "config", home / ".docker"
    for directory in (runtime, config, docker, runtime / "containers", config / "containers"):
        directory.mkdir(mode=0o700)
    auth = runtime / "containers/auth.json"
    for path in (auth, config / "containers/auth.json", docker / "config.json"):
        path.write_text('{"auths":{}}\n')
        path.chmod(0o600)
    return dict(clean_env(), HOME=str(home), DILL_PKL_PATH=str(pkl),
                XDG_RUNTIME_DIR=str(runtime), XDG_CONFIG_HOME=str(config),
                REGISTRY_AUTH_FILE=str(auth), DOCKER_CONFIG=str(docker))


def cancellation_signal(_signum, _frame):
    raise KeyboardInterrupt


def stop_process_group(proc):
    try:
        os.killpg(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        proc.communicate(timeout=5)
    except subprocess.TimeoutExpired:
        pass
    finally:
        # The leader may have exited while a descendant ignored TERM.
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        proc.communicate()


def engine_env(base, engine, endpoint):
    env = dict(base, DILL_TEST_ENGINE=engine, DILL_ENGINE_SOCKET=endpoint,
               DILL_TEST_TIMEOUT="2m")
    if engine == "docker":
        env["DOCKER_HOST"] = endpoint
    else:
        env.update(PODMAN_SOCKET=endpoint, CONTAINER_HOST=endpoint)
    return env


def local_socket(value):
    path = value.removeprefix("unix://")
    if (not path.startswith("/") or path.startswith("//")
            or any(c in path for c in "?#%\\")
            or any(c.isspace() or ord(c) < 32 for c in path)):
        raise ValueError("socket must be an absolute local Unix socket without URI extras")
    if not stat.S_ISSOCK(os.stat(path).st_mode):
        raise ValueError("target is not a Unix socket")
    return "unix://" + path


def make_test_selector(names):
    if not names or any(not re.fullmatch(r"Test[A-Za-z0-9]+", name) for name in names):
        raise ValueError("invalid qualification test selector")
    selector = "^(" + "|".join(names) + ")$"
    # Make expands command-line variables before /bin/sh parses its recipe.
    # Preserve the dollar anchor through Make, then quote shell metacharacters.
    return "DILL_TEST_RUN=" + shlex.quote(selector.replace("$", "$$"))


def operation_capabilities(document, engine):
    """Accept only reviewed dill.operation/1 discovery fields, not arbitrary extensions."""
    base = {"version", "operations", "engines", "features"}
    additions = {"qualifiedCoordinationOperations", "qualifiedCoordinationEngines", "qualifiedCoordinationLayouts"}
    operations = {"validate", "plan", "diff", "up", "down", "stop", "start", "restart", "rm", "teardown"}
    features = {"explicitLocalSelector", "attended", "redactedSummary", "conservativeEffects"}
    qualified_feature = "optInQualifiedLocalCoordination"
    if (not isinstance(document, dict) or not base.issubset(document)
            or set(document) - base - additions or document.get("version") != "dill.operation/1"):
        raise ValueError("unsupported operation discovery fields or contract")
    for field in (base - {"version"}) | (set(document) & additions):
        values = document[field]
        if (not isinstance(values, list) or not all(isinstance(value, str) for value in values)
                or len(values) != len(set(values))):
            raise ValueError("invalid operation discovery array")
    if (set(document["operations"]) != operations or not set(document["engines"]).issubset({"docker", "podman"})
            or engine not in document["engines"] or not features.issubset(document["features"])
            or set(document["features"]) - features - {qualified_feature}):
        raise ValueError("unsupported operation capabilities")
    advertised = qualified_feature in document["features"]
    if advertised or set(document) & additions:
        if (not advertised or not additions.issubset(document)
                or set(document["qualifiedCoordinationOperations"]) != {"up", "down", "stop", "start", "restart", "rm"}
                or document["qualifiedCoordinationEngines"] != ["docker"]
                or document["qualifiedCoordinationLayouts"] != ["native-local-docker-v1"]):
            raise ValueError("unsupported or unnegotiated qualified coordination capabilities")
    return document


def test_results(output, names):
    """Require every selected top-level test to pass with no skipped subtests."""
    lines = output.decode("utf-8", errors="replace").splitlines()
    results = {}
    for name in names:
        states = []
        for line in lines:
            match = re.match(r"\s*--- (PASS|FAIL|SKIP): (\S+)", line)
            if match and (match[2] == name or match[2].startswith(name + "/")):
                states.append(match[1])
        top_pass = any(re.match(r"--- PASS: " + re.escape(name) + r"\s", line)
                       for line in lines)
        results[name] = "passed" if top_pass and states and set(states) == {"PASS"} else "failed"
    return results


class Recorder:
    def __init__(self, env):
        self.env = env
        self.commands = []

    def run(self, argv, timeout=120, expected=0, env=None):
        record = {"argv": [str(x) for x in argv], "expectedExit": expected}
        self.commands.append(record)
        try:
            proc = subprocess.Popen(record["argv"], cwd=ROOT, env=env or self.env,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                    start_new_session=True)
            try:
                stdout, stderr = proc.communicate(timeout=timeout)
            except (subprocess.TimeoutExpired, KeyboardInterrupt):
                # Stop the complete make/go/candidate process group, not just make.
                try:
                    stop_process_group(proc)
                except BaseException as cleanup_error:
                    # Preserve the already-active cancellation/timeout even if
                    # cleanup fails or is interrupted (including SystemExit(0)).
                    # Never let a cleanup failure turn cancellation into success.
                    # The run still fails; retain only a bounded error class.
                    record.update(passed=False, cleanupFailure=type(cleanup_error).__name__[:64])
                raise
            record.update(exit=proc.returncode, stdoutSha256=hashlib.sha256(stdout).hexdigest(),
                          stderrSha256=hashlib.sha256(stderr).hexdigest(),
                          stdoutBytes=len(stdout), stderrBytes=len(stderr))
            record["passed"] = proc.returncode == expected
            return stdout, record["passed"]
        except (OSError, subprocess.TimeoutExpired) as error:
            # Deliberately do not persist raw candidate/engine diagnostics.
            record.update(passed=False, failure=type(error).__name__)
            return b"", False


def resolve_executable(value):
    result = shutil.which(value)
    if result is None:
        raise ValueError("required executable not found")
    return Path(result).resolve(strict=True)


def collect(args):
    if not re.fullmatch(r"[0-9a-f]{40}", args.expected_commit):
        raise ValueError("expected-commit must be a full lowercase Git SHA")
    if not re.fullmatch(r"[0-9a-f]{64}", args.binary_sha256):
        raise ValueError("binary-sha256 must be a lowercase SHA-256 digest")
    binary, pkl = resolve_executable(args.binary), resolve_executable(args.pkl)
    if sha256(binary) != args.binary_sha256:
        raise ValueError("candidate binary checksum mismatch")
    if args.engine and (not args.disposable or not args.socket):
        raise ValueError("engine mode requires --disposable and --socket; no ambient target fallback")
    if not args.engine and (args.socket or args.disposable or args.operations or args.profiles):
        raise ValueError("socket/disposable/operations/profiles require an engine")
    if args.profiles and not args.operations:
        raise ValueError("profile cases require explicit operation-contract discovery opt-in")
    endpoint = local_socket(args.socket) if args.engine else None
    # A new directory prevents clobbering or accidentally mixing prior evidence.
    output = Path(args.output).resolve()
    output.mkdir(mode=0o700, parents=False, exist_ok=False)
    env = private_env(output, pkl)
    recorder = Recorder(env)
    evidence = {"version": "dill.alpha-evidence/1", "createdAt": datetime.now(timezone.utc).isoformat(),
                "qualification": "incomplete", "engine": args.engine or "not-run",
                "target": endpoint, "disposableAttested": args.disposable,
                "operationContractRequested": args.operations,
                "downstreamBrineAcceptance": {"status": "not-run", "scope": "Dill-side artifact supply is not Brine application E2E"},
                "commands": recorder.commands, "cases": [], "gaps": []}
    ok = True
    manifest = {}
    try:
        commit, passed = recorder.run(["git", "rev-parse", "HEAD"])
        commit = commit.decode().strip()
        if not passed or commit != args.expected_commit:
            raise ValueError("checkout does not match expected commit")
        # Untracked fixtures/scripts count too; ignored build/evidence dirs do not.
        status, passed = recorder.run(["git", "status", "--porcelain", "--untracked-files=all"])
        dirty = bool(status.strip())
        evidence["source"] = {"commit": commit, "dirty": dirty,
                              "statusSha256": hashlib.sha256(status).hexdigest()}
        if not passed or (dirty and not args.allow_dirty):
            raise ValueError("candidate checkout must be clean (use allow-dirty only for development)")
        version, passed = recorder.run([binary, "version"])
        ok &= passed
        evidence["binary"] = {"sha256": sha256(binary), "version": version.decode(errors="replace").strip()[:256]}
        info, passed = recorder.run(["go", "version", "-m", "-json", binary])
        if not passed:
            raise ValueError("cannot inspect candidate Go build provenance")
        build_info = json.loads(info)
        settings = {x["Key"]: x.get("Value", "") for x in build_info.get("Settings", [])}
        evidence["binary"].update(goVersion=build_info.get("GoVersion"),
                                  goOS=settings.get("GOOS"), goArch=settings.get("GOARCH"))
        evidence["binary"]["buildRevision"] = settings.get("vcs.revision")
        evidence["binary"]["buildModified"] = settings.get("vcs.modified")
        if settings.get("vcs.revision") != commit:
            raise ValueError("binary build revision does not match candidate commit")
        if settings.get("vcs.modified") != "false" and not args.allow_dirty:
            raise ValueError("binary built from a modified source tree")
        pin = json.loads((ROOT / "internal/loader/pkl-toolchain.json").read_text())
        pkl_version, passed = recorder.run([pkl, "--version"])
        text = pkl_version.decode(errors="replace").strip()
        if not passed or not re.fullmatch(r"Pkl " + re.escape(pin["version"]) + r"(?: \([^\n]*\))?", text):
            raise ValueError("Pkl version differs from pinned toolchain")
        if sha256(pkl) not in pin["sha256"].values():
            raise ValueError("Pkl executable checksum differs from pinned release assets")
        evidence["pkl"] = {"version": text, "sha256": sha256(pkl),
                           "toolchainSha256": sha256(ROOT / "internal/loader/pkl-toolchain.json")}
        evidence["schema"] = {"version": "unversioned dill.pkl", "sha256": sha256(ROOT / "dill.pkl")}
        evidence["fixtures"] = {str(p.relative_to(ROOT)): sha256(p)
                                for p in sorted(FIXTURES.rglob("*")) if p.is_file()}
        manifest = json.loads((FIXTURES / "cases.json").read_text())
        evidence["fixtureVersion"] = manifest["version"]
        for case in manifest["cases"]:
            result = {"id": case["id"], "status": "not-run"}
            if case["mode"] == "offline":
                _, passed = recorder.run([binary, "-f", FIXTURES / case["fixture"], "validate"],
                                         expected=case["exit"])
                result.update(status="passed" if passed else "failed", evidenceKind="offline",
                              candidateCommit=commit, binarySha256=evidence["binary"]["sha256"], target=endpoint)
                ok &= passed
            elif case["mode"] == "blocked" or (case["mode"] == "operation-engine" and not args.operations):
                result.update(status="blocked", requires=case["requires"], reason=case["reason"])
            evidence["cases"].append(result)
        # Deliberately separate narrower #79 observations from the 24 required
        # combined cases: --alpha-disposable must not hide qualified-only gaps.
        evidence["preparedProfiles"] = [{"id": case["id"], "status": "not-run"}
                                        for case in manifest.get("preparedProfiles", [])]
        runtime_cases = manifest["cases"] + manifest.get("preparedProfiles", [])
        runtime_results = evidence["cases"] + evidence["preparedProfiles"]
        if not ok:
            raise ValueError("offline fixture baseline failed; engine execution withheld")
        if args.engine:
            target_env = engine_env(env, args.engine, endpoint)
            target_env["DILL_BINARY"] = str(binary)
            # Required isolated CLI no-prune parity is covered by this engine
            # opt-in, independently of narrower optional profile observations.
            target_env["DILL_TEST_ALPHA_GUARDRAILS"] = "1"
            modes = {"engine"}
            if args.profiles:
                modes.add("guardrail-engine")
                if args.engine == "docker":
                    modes.add("profile-engine")
            if args.operations:
                capabilities, passed = recorder.run([binary, "operation-capabilities", "--format", "json"])
                if not passed or len(capabilities) > 64 * 1024:
                    raise ValueError("operation discovery failed or exceeded its bound")
                capabilities = operation_capabilities(json.loads(capabilities), args.engine)
                evidence["operationCapabilities"] = capabilities
                target_env["DILL_TEST_ALPHA_OPERATIONS"] = "1"
                modes.add("operation-engine")
            cli = [args.engine, "--host" if args.engine == "docker" else "--url", endpoint]
            version_format = "{{json .}}" if args.engine == "docker" else "json"
            engine_version, passed = recorder.run(cli + ["version", "--format", version_format], env=target_env)
            if not passed:
                raise ValueError("explicit engine target version probe failed")
            # Only the version response, never engine info/inspect/config/environment.
            if len(engine_version) > 64 * 1024:
                raise ValueError("unexpected engine version response size")
            evidence["engineVersion"] = json.loads(engine_version)
            tests = list(dict.fromkeys(c["test"] for c in runtime_cases if c["mode"] in modes))
            data, passed = recorder.run(["make", "integration-test", make_test_selector(tests)],
                                        timeout=1800, env=target_env)
            states = test_results(data, tests)
            for case, result in zip(runtime_cases, runtime_results):
                if case["mode"] in modes:
                    kind = "real-profile-engine" if case["mode"] == "profile-engine" else "real-engine"
                    result.update(status=states[case["test"]], test=case["test"], evidenceKind=kind,
                                  candidateCommit=commit, binarySha256=evidence["binary"]["sha256"], target=endpoint)
            ok &= passed and all(s == "passed" for s in states.values())
        if dirty or settings.get("vcs.modified") != "false":
            evidence["gaps"].append("development-only dirty source/binary; not transferable candidate evidence")
        evidence["baselinePassed"] = bool(ok)
        evidence["checksumsAfter"] = {"binary": sha256(binary), "schema": sha256(ROOT / "dill.pkl"),
                                      "pkl": sha256(pkl)}
        final_commit, final_commit_ok = recorder.run(["git", "rev-parse", "HEAD"])
        final_status, final_status_ok = recorder.run(["git", "status", "--porcelain", "--untracked-files=all"])
        final_fixtures = {str(p.relative_to(ROOT)): sha256(p)
                          for p in sorted(FIXTURES.rglob("*")) if p.is_file()}
        if (not final_commit_ok or not final_status_ok or final_commit.decode().strip() != commit
                or hashlib.sha256(final_status).hexdigest() != evidence["source"]["statusSha256"]
                or final_fixtures != evidence["fixtures"]):
            raise ValueError("source or fixtures changed during qualification")
        if evidence["checksumsAfter"] != {"binary": evidence["binary"]["sha256"],
                                           "schema": evidence["schema"]["sha256"],
                                           "pkl": evidence["pkl"]["sha256"]}:
            raise ValueError("candidate files changed during qualification")
        evidence["integrityVerified"] = True
    except (ValueError, KeyError, OSError, json.JSONDecodeError, KeyboardInterrupt) as error:
        evidence.update(baselinePassed=False, error=type(error).__name__)
        ok = False
    finally:
        evidence["acceptance"] = final_acceptance(evidence, manifest)
        evidence["qualification"] = "complete" if evidence["acceptance"]["complete"] else "incomplete"
        if not evidence["acceptance"]["complete"]:
            evidence["gaps"].append("Final required-case acceptance is incomplete; baseline success is not issue #80 completion.")
        (output / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
    print(str(output / "evidence.json"))
    return 0 if ok and (not args.require_complete or evidence["acceptance"]["complete"]) else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--expected-commit", required=True)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--binary-sha256", required=True)
    parser.add_argument("--pkl", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--allow-dirty", action="store_true", help="development only, never qualification")
    parser.add_argument("--require-complete", action="store_true", help="fail unless every final required case has pinned disposable runtime evidence")
    parser.add_argument("--engine", choices=("docker", "podman"))
    parser.add_argument("--operations", action="store_true", help="require discovered dill.operation/1 and run its real-engine consumer cases")
    parser.add_argument("--profiles", action="store_true", help="run no-prune parity and Docker prepared-image profile cases; not qualified coordination")
    parser.add_argument("--socket")
    parser.add_argument("--disposable", action="store_true", help="attest target is exclusively disposable")
    args = parser.parse_args()
    previous = signal.signal(signal.SIGTERM, cancellation_signal)
    try:
        return collect(args)
    except (ValueError, OSError, KeyboardInterrupt):
        print("qualification rejected or interrupted; check pinned inputs and disposable target", file=sys.stderr)
        return 1
    finally:
        signal.signal(signal.SIGTERM, previous)


if __name__ == "__main__":
    sys.exit(main())
