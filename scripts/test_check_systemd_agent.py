import unittest

from check_systemd_agent import ENV_EXAMPLE, UNIT, check_contract


class SystemdPackagingTest(unittest.TestCase):
    unit = UNIT.read_text(encoding="ascii")
    example = ENV_EXAMPLE.read_text(encoding="ascii")
    command_end = "--generation ${DILL_AGENT_GENERATION}"

    def test_shipped_package(self):
        check_contract(self.unit, self.example)

    def test_opt_in_and_pairing_flags_rejected(self):
        for flag in (
            "--reveal-public-key", "--https-staging-root", "--https-preview-state",
            "--https-apply-state", "--print-https-pairing",
        ):
            with self.subTest(flag=flag):
                unit = self.unit.replace(self.command_end, f"{self.command_end} {flag} placeholder")
                with self.assertRaisesRegex(ValueError, "required read-only flags"):
                    check_contract(unit, self.example)

    def test_swapped_bindings_rejected(self):
        unit = self.unit.replace("DILL_AGENT_DAEMON_ID", "SWAP")
        unit = unit.replace("DILL_AGENT_ENGINE_ID", "DILL_AGENT_DAEMON_ID")
        unit = unit.replace("SWAP", "DILL_AGENT_ENGINE_ID")
        with self.assertRaisesRegex(ValueError, "--engine-id must use"):
            check_contract(unit, self.example)

    def test_duplicate_exec_flag_rejected(self):
        unit = self.unit.replace(self.command_end, f"{self.command_end} --generation 2")
        with self.assertRaisesRegex(ValueError, "duplicate ExecStart flag"):
            check_contract(unit, self.example)

    def test_overrides_and_extra_exec_rejected(self):
        for directive in (
            "Environment=DILL_AGENT_GENERATION=2", "EnvironmentFile=/other.env",
            "ExecStartPre=/usr/bin/true", "ReadWritePaths=/var/lib/dill-agent",
        ):
            with self.subTest(directive=directive):
                unit = self.unit.replace("[Service]\n", f"[Service]\n{directive}\n")
                with self.assertRaisesRegex(ValueError, "unexpected directives|duplicate EnvironmentFile"):
                    check_contract(unit, self.example)

    def test_install_target_required(self):
        for replacement in ("", "WantedBy=graphical.target\n"):
            with self.subTest(replacement=replacement):
                unit = self.unit.replace("WantedBy=multi-user.target\n", replacement)
                with self.assertRaisesRegex(ValueError, r"\[Install\]: expected WantedBy=multi-user\.target"):
                    check_contract(unit, self.example)

    def test_restart_policy_and_delay_required(self):
        for directive, alternative in (("Restart=on-failure", "Restart=always"), ("RestartSec=5s", "RestartSec=1s")):
            for replacement in ("", alternative + "\n"):
                with self.subTest(directive=directive, replacement=replacement):
                    unit = self.unit.replace(directive + "\n", replacement)
                    with self.assertRaisesRegex(ValueError, "expected " + directive):
                        check_contract(unit, self.example)

    def test_boot_ordering_required(self):
        for directive, replacement in (
            ("Wants=network-online.target", ""),
            ("Wants=network-online.target", "Wants=network.target\n"),
            ("After=network-online.target docker.service", ""),
            ("After=network-online.target docker.service", "After=docker.service\n"),
            ("After=network-online.target docker.service", "After=network-online.target\n"),
        ):
            with self.subTest(directive=directive, replacement=replacement):
                unit = self.unit.replace(directive + "\n", replacement)
                with self.assertRaisesRegex(ValueError, r"\[Unit\]: expected " + directive):
                    check_contract(unit, self.example)

    def test_after_target_list_accepts_equivalent_order(self):
        unit = self.unit.replace("After=network-online.target docker.service", "After=docker.service network-online.target")
        check_contract(unit, self.example)

    def test_relaxed_sandbox_rejected(self):
        with self.assertRaisesRegex(ValueError, "expected ProtectSystem=strict"):
            check_contract(self.unit.replace("ProtectSystem=strict", "ProtectSystem=false"), self.example)

    def test_template_drift_rejected(self):
        for old, new, diagnostic in (
            ("DILL_AGENT_LISTEN=", "DILL_AGENT_ADDRESS=", "exactly the variables"),
            ("127.0.0.1:8443", "0.0.0.0:8443", "DILL_AGENT_LISTEN must default"),
            ("REPLACE_WITH_ENROLLED_ENGINE_UUID", "example-not-a-placeholder", "DILL_AGENT_ENGINE_ID must default"),
            ("DILL_AGENT_GENERATION=1", "DILL_AGENT_GENERATION=1\nDILL_AGENT_GENERATION=2", "duplicate"),
        ):
            with self.subTest(diagnostic=diagnostic):
                with self.assertRaisesRegex(ValueError, diagnostic):
                    check_contract(self.unit, self.example.replace(old, new))
