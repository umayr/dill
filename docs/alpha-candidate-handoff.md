# Copied Dill Alpha candidate handoff (v1)

This bundle supplies **Dill-side artifacts and a usable copied caller contract**.
It does not run, qualify or approve the external Brine application/browser, an
application database, production deployment, secrets or a release. A caller's
`--coordination-source brine` is metadata, not authentication or application proof.
`downstreamBrineAcceptance` remains **not-run**, even after Dill-side completion.

## Verify before execution

Obtain the exact source commit, `evidence.json`, archive SHA256 and manifest SHA256
from the independently reviewed, immutable hosted CI artifact. An archive alone
cannot authenticate itself: compare its checksum against that trusted evidence
**before extracting or executing its contents**. Extract into a new disposable
location, never over an existing operator installation/state directory.

The `handoff.tar.gz` allowlist contains `bin/dill`, checksum-pinned `bin/pkl`,
`dill.pkl`, `internal/loader/pkl-toolchain.json`, `test/qualification/v1/`, public
`internal/operation/testdata/` goldens, this guide and `handoff.json`. There is no
HOME, registry auth, coordination key/journal, private state or raw engine log.
The manifest binds the exact candidate commit and every public copied file hash.
Check its externally supplied checksum first, then verify every listed file:

```python
import hashlib, json, pathlib
root = pathlib.Path("extracted-bundle")
manifest = json.loads((root / "handoff.json").read_text())
assert manifest["version"] == "dill.alpha-handoff/1"
for name, expected in manifest["files"].items():
    path = root / name
    assert not path.is_symlink() and path.is_file()
    assert hashlib.sha256(path.read_bytes()).hexdigest() == expected
```

Compare `candidateCommit`, `binarySha256`, fixture version, schema and Pkl hashes
with the reviewed evidence/source. Inspect the binary's embedded Go VCS metadata
using a trusted Go installation if available. The packaged executable and Pkl
are platform-specific; use only the recorded native Linux architecture.

## Independently provision the target BEFORE candidate execution

This is not a production trust policy or automatic layout detector. The #80
hosted path requires a fresh GitHub-hosted Linux VM, no job container/proxy/remote
context, known root-owned local Docker service and fixed root-owned
`/var/run/docker.sock`, with CLI and daemon in the same native guest/namespaces.
A GHA VM is acceptable; a separate daemon VM/namespace/proxy is not. The trusted
workflow step runs the **preinstalled** Docker CLI with an explicit fixed socket,
empty private config and cleared ambient selectors, independently captures and
bounds the expected daemon ID, and creates an operator-owned 0700 state root.
It precedes candidate binary/version/tests. No qualifier obtains its expectation
by querying its chosen arbitrary endpoint. Logs/private provisioning data are
not published. Unsupported or unknown layouts fail closed, not a fallback.

A recipient must independently establish a new supported disposable target and
private state directory BEFORE first candidate execution. Never reuse CI's raw
identity, copy a coordination key/journal between hosts, infer attestation from
`endpointDigest`, or derive authority by asking an untrusted socket who it is.
All contending callers for the case share the same private operator state store.
Prepare/inspect allowed local no-VOLUME images outside qualified execution.

## Copied caller contract

After trusted provisioning, use the copied binary, copied Pkl and unchanged
copied fixture. Supply independently provisioned `EXPECTED_DAEMON_ID`, explicit
`SOCKET=unix:///var/run/docker.sock`, and a fresh private `STATE` directory.
Use a synthetic unique stack name; no real secrets or valuable storage:

```sh
export DILL_PKL_PATH="$BUNDLE/bin/pkl"
"$BUNDLE/bin/dill" operation-capabilities --format json
"$BUNDLE/bin/dill" operation up --contract dill.operation/1 \
  --engine docker --engine-socket "$SOCKET" -p "$SYNTHETIC_STACK" \
  -f "$BUNDLE/test/qualification/v1/valid.pkl" --pull never \
  --coordination-state-dir "$STATE" \
  --coordination-layout native-local-docker-v1 \
  --coordination-daemon-id "$EXPECTED_DAEMON_ID" --coordination-source brine
```

Discovery must advertise the complete reviewed qualified lists/feature. No
fallback to `--alpha-disposable` or ordinary CLI is permitted for this proof.
The fixture remains byte-identical; explicit `--pull never` overrides its legacy
pull default. Assert strict receipt version/required fields, qualified identity,
exit 0/succeeded/complete, immutable created image content **and reference**, and
matching durable source/operation/target via offline `coordination inspect`.
The harness performs this actual copied-caller proof and independently verifies
all copied hashes before creating the public archive. Failed cleanup fails the
case; a partial setup manifest cannot green artifact supply.

Only native Docker, prepared immutable no-VOLUME images and dedicated named
storage are supported. Binds, secrets, devices/caps, host namespaces, image
VOLUMEs (even covered ones), orphans and qualified teardown remain unsupported.
Actual Brine requirements outside that subset must reject before writes; do not
silently omit them or reinterpret this fixture as application E2E.

Inspect uncertain/partial outcomes manually and use offline exact operation-ID
acknowledgement with explicit `--inspected`; never automatically replay or call
cleanup rollback. Docker network retention can yield down exit 1/failed/partial
after known container writes, or failed/none when only a retained network remains.
External Brine qualification, browser assertions and production approval are
separate downstream responsibilities. No such acceptance is supplied here.
