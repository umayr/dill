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


def named(entries):
    """Fixture-only indexing: never silently overwrite duplicate names.

    The future Dill semantic validator must implement this rule too; this helper
    is not that runtime implementation and does not validate references or DAGs.
    """
    assert isinstance(entries, list), "public declarations must render as arrays"
    result = {}
    for entry in entries:
        name = entry["name"]
        if name in result:
            raise ValueError(f"duplicate name: {name}")
        result[name] = entry
    return result


def main():
    version = subprocess.check_output([PKL, "--version"], text=True).strip()
    print(version)
    composed = evaluate(EXAMPLES / "composed.pkl")
    services = named(composed["application"]["services"])
    assert named(list(reversed(composed["application"]["services"]))) == services
    assert set(named(composed["application"]["storage"])) == {"catalog-data", "metrics-data"}
    assert set(named(composed["application"]["secrets"])) == {"catalog-password", "metrics-password"}
    assert named(composed["bindings"]["storage"])["catalog-data"]["volume"] == "existing-catalog-data"
    assert named(composed["bindings"]["secrets"])["catalog-password"]["key"] == "catalog/password"
    assert set(services) == {"catalog", "metrics"}
    assert services["catalog"]["environment"]["POSTGRES_DB"] == "catalog"
    assert services["metrics"]["environment"]["POSTGRES_DB"] == "metrics"
    assert services["catalog"]["resources"]["memory"] == {"value": 512, "unit": "mib"}
    assert "memory" not in services["metrics"]["resources"]
    assert services["catalog"]["secretEnvironment"]["POSTGRES_PASSWORD"] == {
        "secret": "catalog-password"
    }
    assert services["metrics"]["mounts"][0]["storage"] == "metrics-data"
    assert named(composed["application"]["jobs"])["initialize"]["trigger"] == "manual"
    assert composed == evaluate(EXAMPLES / "composed.pkl")

    downloads = evaluate(EXAMPLES / "downloads.pkl")
    download_services = named(downloads["application"]["services"])
    torrent = download_services["torrent"]
    assert named(download_services["vpn"]["ports"])["web"]["target"] == 8080
    assert named(download_services["vpn"]["devices"])["tun"]["target"] == "/dev/net/tun"
    assert named(downloads["bindings"]["ports"])["vpn/web"]["port"] == 8080
    assert named(downloads["bindings"]["devices"])["tun"]["path"] == "/dev/net/tun"
    assert torrent["network"] == {
        "kind": "shared", "service": "vpn", "condition": "healthy",
        "onOwnerReplacement": "recreate",
    }
    assert torrent["temporaryStorage"][0]["size"] == {"value": 2, "unit": "gib"}
    assert named(downloads["bindings"]["storage"])["downloads"]["missing"] == "fail"
    assert download_services["vpn"]["readiness"]["timeout"] == {
        "value": 2, "unit": "min"
    }
    assert downloads["policy"]["storageDeletion"] == "deny"

    cases = [
        ('port = 8080', 'port = 70000'),
        ('name = "torrent"', 'name = ""'),
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
        # Pkl shape checking alone does not establish name uniqueness.
        duplicate = target / "duplicate.pkl"
        duplicate.write_text(original.replace('name = "torrent"', 'name = "vpn"'))
        duplicate_value = evaluate(duplicate)
        try:
            named(duplicate_value["application"]["services"])
        except ValueError as error:
            assert str(error) == "duplicate name: vpn"
        else:
            raise AssertionError("fixture index accepted duplicate names")
    print("PASS: named listings, composition, amendment, bindings, units, repeatable values")
    print("PASS: declaration indexing ignores order and rejects duplicates (fixture helper only)")
    print("PASS: invalid port/name, negative size and relative host path rejected by Pkl")
    print("NOT TESTED: semantic DAG/reference validation, packages, policy, targets, runtime execution")


if __name__ == "__main__":
    main()
