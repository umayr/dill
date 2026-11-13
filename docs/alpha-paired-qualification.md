# Native Dill/Brine paired Alpha qualification

## Status: input preparation only, application qualification NOT RUN

This is the input-verification component of a **Dill-owned, manually dispatched,
GitHub-hosted Linux amd64** paired-canary workflow. It does not implement or
simulate the Brine application canary. The workflow/application case interface
must be frozen and reviewed before an actual run. A missing hook, empty policy,
skipped/blocked required positive case or missing result must fail closed.

The operator authorized **disposable GitHub Actions only**: no local engines,
homelab/production hosts or credentials, deployment, tags or release publication.
Brine owns the actual application hook/tests; Dill owns trusted provisioning and
public evidence orchestration. One writer per file/worktree; do not duplicate
Brine's implementation.

## Two separate source identities

- **Workflow driver:** the exact Dill commit containing the orchestration. Its
  source revision does not silently replace the payload candidate.
- **Dill payload:** fixed by `test/qualification/pair/v1/dill-input.json` to clean
  main `b3ecbaa29260f65d56cf55e54ccd8fedcdf496d2`, Linux amd64, actual binary SHA
  `3acea65d4121402194787bb0bdccc20a79c725e1ee5bb2337cc5b33b16e12d51`.
- **Brine payload:** a separately reviewed, exact full commit and actual native
  server/web-asset build. No branch/tag inference or fixture-server substitution.

The accepted Dill supply comes from main qualification run **43399222914**, Docker
artifact **14025176783**. The pin records the externally reviewed API identity,
ZIP size/digest, inner public tar/manifest/binary/schema/Pkl checksums and source
identity. That supply was independently accepted before this component existed.
Do not derive trust from a newly delivered archive's self-declared checksums.
Actions retention is finite: **expired/missing input fails closed**, never a
`latest`, tag, source-build or alternate-architecture fallback. Replacing this
input requires an explicitly requalified candidate and independent pin review.

## Input verifier

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts \
  -p 'test_prepare_alpha_pair.py' -v

# Inspect the actual API-downloaded ZIP without unpacking or executing it:
python3 scripts/prepare-alpha-pair.py \
  --artifact-zip /path/to/exact-docker.zip \
  --artifact-metadata /path/to/artifact-api.json --validate-only
```

The API metadata must be obtained from the pinned repository/artifact endpoint,
not from the candidate. The workflow must authenticate that fetch separately;
this verifier performs no network requests and cannot authenticate a forged API
JSON file by itself. The externally anchored ZIP digest still authenticates the
bytes it accepts.

It rejects wrong/expired API identity, wrong run/head/branch/size, mismatched
outer/inner hashes, duplicate JSON keys/nonfinite constants, unsafe ZIP names or
links, and any extra/missing/duplicate/nonregular/non-normalized tar member. It
checks the exact 16-member public tar and all manifest/payload hashes. No HOME,
auth, private journal, runtime log or browser output is in that supply.

Only explicit `--output /absolute/new/private-parent/supply` extracts, after all
validation, into a new directory under a real owner-only private parent. It never
overwrites an existing path or follows an output symlink. Files are copied by
name, not by general tar extraction; binaries become owner read/execute only,
other payloads owner read-only, directories owner-only. Extraction is reserved
for the approved disposable receiver. Local actual-payload validation uses
`--validate-only`; unit extraction tests use synthetic nonexecutables only.

The result is **`verified-dill-input`**, with
`jointApplicationAcceptance=not-run` and `receivingTargetQualified=false`. Neither
successful validation nor the historical copied `source=brine` receipt qualifies
a new daemon, Brine process, application lifecycle or release. The old CI target
identity is deliberately not exported as a receiving-host identity.

## Receiving-host gate, before any candidate execution

The eventual literal trusted workflow step must precede checkout/source scripts,
source builds, version/capability probes and every Brine/Dill/Pkl execution:

1. Verify fresh GitHub-hosted Linux native placement, not container/proxy/another
   VM. Verify root-owned native Docker service/socket and actual user/pid/mount/
   network namespace co-location.
2. With literal trusted system tools and an empty auth/context/proxy-free client
   environment, independently capture this host's daemon identity. Never fill it
   from the candidate's capability/result output.
3. Create owner-only0700 shared coordination state and separate private work,
   client HOME/cache/auth directories. Native Brine and CLI use the same operator.
4. Fetch the pinned API metadata/archive, validate all bytes, then prepare the
   verified input. Build the exact reviewed Brine source/web assets in an
   allowlisted environment; retain actual clean-source and binary provenance.
5. Start the real authenticated native server with **all** qualified inputs.
   Neither stock Brine Docker packaging nor its systemd hardening/account choices
   automatically qualify. Do not weaken service hardening or use legacy cleanup
   to make the canary pass.

The existing `.github/workflows/alpha-qualification.yml` literal provisioning
prefix is the reviewed starting point, not permission to execute application
code before the new receiving environment is independently established.

## Reserved Brine-owned hook (interface not yet frozen)

Brine reserves **`scripts/ci-alpha-paired-app.sh`**. Agreed conceptual inputs are:
`DILL_BINARY`, `PKL`, `BRINE_BINARY`, `ENGINE_SOCKET`, `EXPECTED_DAEMON_ID`,
`COORDINATION_STATE_DIR`, `PRIVATE_WORK_DIR`, `PUBLIC_OUTPUT_DIR`.

These names are not a claim that the hook exists. Exact finite case IDs,
selectors, closed public JSON schema and allowed output filenames require the
Brine owner's reviewed freeze. The runner must not invent a passing hook or
accept arbitrary JSON/log files as application evidence. Keep credentials,
cookies/CSRF values, full browser traces/screenshots/HAR, HOME, private state and
raw engine/server logs outside public artifacts.

Brine's current planned qualified routes are **up/start/restart/stop/down**;
Dill also supports qualified `rm`, but that is not an advertised Brine route.
Do not expand the product scope merely to satisfy a case name. Qualified
teardown is **unsupported**: a teardown rejection/retained-volume negative must
not be reported as successful teardown. Read-only plan/validate/diff are
explicit-target ordinary operations, not target attestation, locking or saved
plan authorization. Apply freshly revalidates/qualifies under the shared locks.

The actual canary must cover real authenticated browser/API routes, supported
lifecycle/no-op/update/logs/retention, wrong/default targets and invalid inputs,
CLI/app contention in both orders and aliases, independent stacks, disconnect
versus cancellation, proven-effect crash/server restart, durable honest status,
exact inspected recovery/no replay, foreign inventory/data preservation, and
public-artifact provenance. Fake fixtures, route fulfilments, source-arm64
process checks and the older paired secret workflow are not substitutes.

## Promotion is separate

After actual application acceptance, independently audit the exact paired
artifacts and final source/CI evidence. A versioned/tag rebuild may change
binary bytes; do not borrow an old input's hash/runtime evidence for new assets.
Dill's existing `v*` tag workflow publishes builds and is not an authorization to
promote an unqualified pair. Versions, asset set and publication require the
explicit release decision. Never overwrite the existing v0.7.0 release or
silently claim qualified Podman/containerized Brine/production support.
