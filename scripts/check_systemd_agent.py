#!/usr/bin/env python3
"""Check the shipped systemd example without installing or starting it.

Reproduce on unprivileged Linux with systemd 255 (Ubuntu 24.04):
    make build
    python3 scripts/check_systemd_agent.py bin/dill
Only --help is executed on the agent CLI. This is not boot qualification.
"""

import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


PACKAGING = Path(__file__).resolve().parents[1] / "packaging/systemd"
UNIT = PACKAGING / "dill-agent.service"
ENV_EXAMPLE = PACKAGING / "agent.env.example"
EXECUTABLE = "/usr/local/bin/dill"
ENV_FILE = "/etc/dill-agent/agent.env"

# Intentionally closed: extending the example requires reviewing the default
# surface. In particular no other commands or environment overrides are allowed.
SECTION_CONTRACT = {
    "[Unit]": ({
        "Wants": "network-online.target",
        "After": "network-online.target docker.service",
    }, {"Description"}),
    "[Service]": ({
        "Type": "exec",
        "User": "dillagent",
        "Group": "dillagent",
        "EnvironmentFile": ENV_FILE,
        "Restart": "on-failure",
        "RestartSec": "5s",
        "NoNewPrivileges": "true",
        "ProtectSystem": "strict",
        "ProtectHome": "true",
        "PrivateTmp": "true",
        "UMask": "0077",
    }, {"ExecStart"}),
    "[Install]": ({"WantedBy": "multi-user.target"}, set()),
}
# The mapping is directional: a swap can preserve the set of variables while
# passing the wrong value to each flag.
ENV_BINDINGS = {
    "--https-listen": ("DILL_AGENT_LISTEN", "127.0.0.1:8443"),
    "--engine-socket": ("DILL_AGENT_ENGINE_SOCKET", "/run/docker.sock"),
    "--engine-id": ("DILL_AGENT_ENGINE_ID", "REPLACE_WITH_ENROLLED_ENGINE_UUID"),
    "--daemon-id": ("DILL_AGENT_DAEMON_ID", "REPLACE_WITH_OBSERVED_DOCKER_ID"),
    "--generation": ("DILL_AGENT_GENERATION", "1"),
}
FLAGS = {
    "--https-dir": "/var/lib/dill-agent/https",
    **{flag: "${" + key + "}" for flag, (key, _) in ENV_BINDINGS.items()},
}
CLEAN_ENV = {"PATH": os.defpath, "LC_ALL": "C"}


def lines(text, name):
    """Only accept simple, single-line directives/assignments in the examples."""
    for number, raw in enumerate(text.split("\n"), 1):
        # A continued comment also affects the next line in systemd syntax.
        if raw.rstrip().endswith("\\"):
            raise ValueError(f"{name}:{number}: continuations are not supported")
        line = raw.strip()
        if line and not line.startswith(("#", ";")):
            yield number, line


def assignment(line, dest, where):
    key, sep, value = line.partition("=")
    if not sep or not re.fullmatch(r"[A-Za-z][A-Za-z_0-9]*", key):
        raise ValueError(f"{where}: expected a simple key=value assignment")
    if key in dest:
        raise ValueError(f"{where}: duplicate {key}")
    dest[key] = value


def check_contract(unit_text, example_text):
    sections = {}
    section = None
    for number, line in lines(unit_text, UNIT.name):
        where = f"{UNIT.name}:{number}"
        if line in SECTION_CONTRACT:
            if line in sections:
                raise ValueError(f"{where}: duplicate section {line}")
            section = sections[line] = {}
        elif section is None:
            raise ValueError(f"{where}: expected a unit section")
        else:
            assignment(line, section, where)

    for name, (required, allowed) in SECTION_CONTRACT.items():
        settings = sections.get(name, {})
        extra = settings.keys() - required.keys() - allowed
        if extra:
            raise ValueError(f"{UNIT.name}: {name}: unexpected directives: {', '.join(sorted(extra))}")
        for key, expected in required.items():
            value = settings.get(key, "")
            # The order of names within After= is immaterial.
            matches = sorted(value.split()) == sorted(expected.split()) if key == "After" else value == expected
            if not matches:
                raise ValueError(f"{UNIT.name}: {name}: expected {key}={expected}")

    argv = sections["[Service]"].get("ExecStart", "").split()
    if argv[:2] != [EXECUTABLE, "agent"] or len(argv[2:]) % 2:
        raise ValueError(f"{UNIT.name}: ExecStart must invoke {EXECUTABLE} agent with flag/value pairs")
    args = dict(zip(argv[2::2], argv[3::2]))
    if len(args) * 2 != len(argv[2:]):
        raise ValueError(f"{UNIT.name}: duplicate ExecStart flag")
    if args.keys() != FLAGS.keys():
        raise ValueError(f"{UNIT.name}: ExecStart must use only the required read-only flags: {', '.join(FLAGS)}")
    for flag, expected in FLAGS.items():
        if args[flag] != expected:
            raise ValueError(f"{UNIT.name}: {flag} must use {expected}")

    values = {}
    for number, line in lines(example_text, ENV_EXAMPLE.name):
        assignment(line, values, f"{ENV_EXAMPLE.name}:{number}")
    expected_values = dict(ENV_BINDINGS.values())
    if values.keys() != expected_values.keys():
        raise ValueError(f"{ENV_EXAMPLE.name}: expected exactly the variables referenced by ExecStart")
    for key, expected in expected_values.items():
        if values[key] != expected:
            raise ValueError(f"{ENV_EXAMPLE.name}: {key} must default to {expected}")


def check_agent_help(binary):
    # Go flag's help displays a single dash, even though both forms are accepted.
    help_result = subprocess.run(
        [binary, "agent", "--help"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
        text=True, check=True, timeout=15, env=CLEAN_ENV,
    )
    options = dict(re.findall(r"^[ \t]+(-[\w-]+) (\w+)$", help_result.stdout, re.MULTILINE))
    for flag in FLAGS:
        kind = "uint" if flag == "--generation" else "string"
        if options.get(flag[1:]) != kind:
            raise ValueError(f"dill agent --help: expected {flag} ({kind})")


def verify_systemd(binary):
    if sys.platform != "linux" or os.geteuid() == 0:
        raise ValueError("systemd verification requires an unprivileged Linux user")
    analyzer = shutil.which("systemd-analyze", path=CLEAN_ENV["PATH"])
    if not analyzer:
        raise ValueError("systemd-analyze is required (CI uses Ubuntu 24.04)")
    subprocess.run([analyzer, "--version"], check=True, env=CLEAN_ENV, timeout=15)
    with tempfile.TemporaryDirectory(prefix="dill-systemd-") as temp:
        root = Path(temp)
        unit_dir = "/usr/lib/systemd/system"
        for source, dest in (
            (binary, EXECUTABLE),
            (UNIT, f"{unit_dir}/{UNIT.name}"),
            (ENV_EXAMPLE, ENV_FILE),
        ):
            staged = root / dest.lstrip("/")
            staged.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, staged)
        # Keep ExecStart unchanged and resolve the unit and binary in this root.
        # systemd 255 disables unit generators with --generators=no, but still
        # looks for environment generators. Point that lookup at an empty dir.
        empty_generators = root / "empty-environment-generators"
        empty_generators.mkdir()
        subprocess.run(
            [analyzer, f"--root={root}", "--generators=no", "--man=no",
             "--recursive-errors=no", "verify", UNIT.name],
            check=True, timeout=30, cwd=root, env={
                **CLEAN_ENV,
                "SYSTEMD_UNIT_PATH": unit_dir,
                "SYSTEMD_ENVIRONMENT_GENERATOR_PATH": str(empty_generators),
            },
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path, help="combined dill binary built from this checkout")
    parser.add_argument("--contract-only", action="store_true", help="skip Linux systemd verification")
    args = parser.parse_args()
    try:
        binary = args.binary.resolve(strict=True)
        check_contract(UNIT.read_text(encoding="ascii"), ENV_EXAMPLE.read_text(encoding="ascii"))
        check_agent_help(binary)
        print("OK: read-only package defaults, static unit policy, env-to-flag mapping, and matching CLI help", flush=True)
        if not args.contract_only:
            verify_systemd(binary)
            print("OK: systemd-analyze verify against the unchanged unit and staged binary (no boot)")
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        parser.exit(1, f"systemd packaging check failed: {exc}\n")


if __name__ == "__main__":
    main()
