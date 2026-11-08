#!/usr/bin/env python3
"""Evaluate experimental authoring fixtures, NOT Dill runtime conformance.

Usage: PKL=/path/to/pkl-0.31.1 python3 docs/design/check_examples.py
Requires Python 3 and Pkl. No engine, network, or repository writes are needed.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

PKL = os.environ.get("PKL", "pkl")
EXAMPLES = Path(__file__).resolve().parent / "examples"


def evaluate(path, succeeds=True):
    result = subprocess.run(
        [PKL, "eval", str(path)], capture_output=True, text=True, timeout=30
    )
    if succeeds:
        if result.returncode:
            raise AssertionError(f"{path.name}: {result.stderr}")
        return json.loads(result.stdout)
    if result.returncode == 0:
        raise AssertionError(f"{path.name}: expected Pkl constraint failure")
    if "Constraint" not in result.stderr and "constraint" not in result.stderr:
        raise AssertionError(f"{path.name}: wrong failure: {result.stderr}")
    return None


def main():
    version = subprocess.check_output([PKL, "--version"], text=True).strip()
    print(version)
    composed = evaluate(EXAMPLES / "composed.pkl")
    services = composed["application"]["services"]
    assert set(services) == {"catalog", "metrics"}
    assert services["catalog"]["environment"]["POSTGRES_DB"] == "catalog"
    assert services["metrics"]["environment"]["POSTGRES_DB"] == "metrics"
    assert services["catalog"]["resources"]["memory"] == {"value": 512, "unit": "mib"}
    assert "memory" not in services["metrics"]["resources"]
    assert services["catalog"]["secretEnvironment"]["POSTGRES_PASSWORD"] == {
        "secret": "catalog-password"
    }
    assert services["metrics"]["mounts"][0]["storage"] == "metrics-data"
    assert composed["application"]["jobs"]["initialize"]["trigger"] == "manual"
    assert composed == evaluate(EXAMPLES / "composed.pkl")

    downloads = evaluate(EXAMPLES / "downloads.pkl")
    torrent = downloads["application"]["services"]["torrent"]
    assert torrent["network"] == {
        "kind": "shared", "service": "vpn", "condition": "healthy",
        "onOwnerReplacement": "recreate",
    }
    assert torrent["temporaryStorage"][0]["size"] == {"value": 2, "unit": "gib"}
    assert downloads["bindings"]["storage"]["downloads"]["missing"] == "fail"
    assert downloads["application"]["services"]["vpn"]["readiness"]["timeout"] == {
        "value": 2, "unit": "min"
    }
    assert downloads["policy"]["storageDeletion"] == "deny"

    cases = [
        ('port = 8080', 'port = 70000'),
        ('size = 2.gib', 'size = -1.gib'),
        ('path = "/srv/media/downloads"', 'path = "relative/path"'),
    ]
    with tempfile.TemporaryDirectory(prefix="dill-design-") as tmp:
        target = Path(tmp)
        for source in EXAMPLES.glob("*.pkl"):
            shutil.copyfile(source, target / source.name)
        original = (target / "downloads.pkl").read_text()
        for old, new in cases:
            assert original.count(old) == 1, old
            invalid = target / "invalid.pkl"
            invalid.write_text(original.replace(old, new))
            evaluate(invalid, succeeds=False)
    print("PASS: composition, amendment, reference rendering, units, deterministic values")
    print("PASS: invalid port, negative size and relative host path rejected by Pkl")
    print("NOT TESTED: semantic DAG/reference validation, packages, policy, targets, runtime execution")


if __name__ == "__main__":
    main()
