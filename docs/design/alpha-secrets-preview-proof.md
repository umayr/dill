# Alpha secret preview proof extension (implemented; qualification pending)

Dill #84 and Brine #71 require more than comparing rendered plans: equal plans
cannot establish that the evaluated configuration, policy and relevant live
state are unchanged. This is a narrow private confirmation mechanism, not a
saved plan, transaction or new deployment protocol.

`dill --secret-target ID -p STACK --engine ENGINE --engine-socket /ABS/SOCKET plan [existing flags] --proof-key-file ABS --proof-file ABS` accepts both
flags together, only on a secret-enabled plan. On a successfully completed
plan, including the normal drift exit status 1, Dill atomically writes a fresh,
private mode-0600 file containing exactly:

```json
{"contract":"dill.alpha-secrets-preview/1","proof":"<base64url HMAC-SHA256>"}
```

A parse or operational error produces no proof and uses the secret path's fixed
safe error. Brine supplies a fresh output filename for each invocation, treating
exit 1 as a completed plan only if the new valid proof and valid plan are both
present. Neither path is included in the MAC. The key is exactly 32 random bytes
in a private Brine/operator-owned location outside editable or served project
files. Reject symlinks, unsafe ownership or permissions, nonregular files and
incorrect lengths. Both key and output must be outside the known resolved recipe workspace.
The output path must also be private and safe; use no-follow,
atomic mode-0600 creation and do not overwrite key, policy, config, source or
other preexisting files. Key rotation invalidates all previews.

The domain-separated HMAC covers a canonical length-delimited representation of
the effective evaluated configuration (including import semantics), the exact
candidate binding, authorized policy inputs, target/engine/socket, operation
selection and options, relevant live configuration, identity, ownership,
mounts, image IDs and resource facts, lifecycle state relevant to safety, and
the normalized plan. It never reads source bytes or emits an unkeyed value hash.
The exact live fields must be specified from the implemented planner and
ownership checks, independently audited and tested for stable repeats and
meaningful change invalidation. Exclude irrelevant raw inspect timestamps,
counters and health samples. Semantically identical re-evaluation may retain
the same proof; this is not a claim about byte-identical remote import provenance.

Brine stores proofs privately, never in API responses, UI, history or logs. It
serializes candidate edits, preview, confirmation, activation and deploy, repeats
the plan under that lock and constant-time compares proof, candidate identity and
checked plan status/output before activating exactly that candidate. `up` still
performs independent preflight. Trusted-operator quiescence remains required;
foreign writers, source replacement and mutable remote image tags are not frozen
by the proof. Paired golden fixtures and real-binary tests are required before
this interface is considered implemented.

## Implemented encoding and exact stable observation surface

Stage C implements this interface. Linux CI and paired Brine results must still
be reviewed before release; local compilation/HTTP fixtures are not runtime
qualification. Both flags work globally or on `plan`, must occur together with
nonempty absolute paths, and require evaluated service grants and full checked
secret authorization. `diff` and other commands reject proof requests. Plan
keeps its normal drift exit 1 and no-change exit 0. Format, verbosity, quiet,
confirmation controls, config filename, policy/binding filenames, key filename
and output filename do not enter the proof. Relative authored mounts are bound
through their effective normalized absolute sources, not the recipe filename.
The proof operation uses the existing positive `--timeout` budget (default five
minutes); private Pkl evaluation and preflight retain their narrower deadlines.
Filesystem syscalls remain subject to host/kernel availability.

The MAC is HMAC-SHA256 under exactly 32 private key bytes, encoded as unpadded
base64url (43 characters). Its input is five successive frames, each an
unsigned big-endian uint64 byte length followed by exactly those bytes:

1. UTF-8 domain `dill.alpha-secrets-preview/1`.
2. Canonical authority JSON: trusted target/stack/engine/canonical socket,
   selected roots and no-deps option, **entire strictly parsed policy and binding
   documents**, selected resolved grant metadata, and **observed validated selected-source
   stat facts**: device, inode, size, mtime/ctime seconds and nanoseconds, UID,
   GID, full mode/type bits and link count. These observations are retained in
   opaque preflight evidence and rechecked before publication. Atime, source
   bytes and content hashes are excluded. Arrays retain declaration
   order; semantically reordered documents may conservatively invalidate.
3. Effective evaluated configuration JSON: config name/engine/declarations;
   every service field (`secrets`, image, containerName, restart, pullPolicy,
   environment, volumes, ports, labels, dependsOn, command, networkMode,
   healthcheck including durations/retries, user, init, capAdd, devices, cpuset,
   memory/CPU/PID resource declarations); additionally normalized/resolved
   volumes, normalized ports and normalized resource limits per service; and
   effective force-recreate, dry-run and timeout options. `BaseDir` itself is
   excluded; its effect on relative mounts is included. Successful Pkl imports
   are represented by their evaluated semantics, not import-file timestamps.
4. Private live observation JSON described below.
5. Normalized plan JSON (ordered changes, kinds and ordered field differences).

JSON sections use compact Go JSON with lexicographically sorted object/map
keys; embedded RawMessages are decoded/re-encoded before MAC framing. JSON
numbers retain their evaluated representation. This is stable for identical
re-evaluation, not a promise that every alternate numeric or array spelling is
equivalent. No unkeyed digest of these sections is emitted.

The private live section contains exactly:

- A map of effective container names to absent/null or `LiveConfig`: immutable
  full container ID, configured image and actual image ID, all inspect env,
  normalized ports, mount type/source/destination/RO facts, mount-visibility
  evidence, restart policy, all user/system labels (including ownership, hash
  and marker), network mode, user, healthcheck test, command, added caps, init,
  cpuset, normalized memory/reservation/CPU-share/nanoCPU/PID limits, userns mode
  and declared UID/GID mappings. Mounts, ports, caps and mapping rows are sorted.
  Unsupported per-container namespace overrides are rejected, not accepted as
  a noop. Desired ownership and every orphan's immutable identity/ownership are
  checked before publishing. Missing/unknown inspect evidence fails closed.
- Every desired image's local immutable ID or explicit absence, including images
  for desired containers not yet created. Conflicting repeated image reads fail.
- The sorted full managed stack membership and per-container lifecycle **state**
  (running/exited/paused/etc.), not human status strings, uptime, health samples,
  exit counters or inspect creation/update timestamps.
- The sorted list of stack-labelled volume names and native read-only resource
  facts for the stack network and every declared resolved named volume.
  Network: existence/name/full ID and static configuration. Docker fields are
  driver, scope, internal/IPv6/attachable/ingress, IPAM, options and labels;
  Podman fields are native name/ID/driver/interface/subnets/routes/IPv6/internal/
  DNS servers/options/IPAM-options/labels/DNS-enabled (creation time zeroed).
  Attached containers and usage counters are excluded.
  Volume: existence/name, driver, mountpoint, options, labels and **stable volume
  creation time**; Docker also includes scope. The creation time is a relevant
  replacement discriminator because these APIs lack immutable volume IDs, not
  a changing sample. Volume size/refcounts/mount counts and arbitrary driver
  status are excluded. No network/volume mutation is performed for proof reads.

The planner observation implementation is `internal/plan/preview.go`; native
resource allowlists live in both adapter `PreviewResources` methods. Any future
change to this surface must update these lists and invalidation tests. It is
not an assertion that all engine inspect fields influence reconciliation.

Selected-source replacement or detectable stat changes before confirmation
invalidate the proof even when policy/revision and engine inspect paths are
unchanged. A change during planning withholds publication until fresh preflight.
This observes the current authorized host path, **not the inode already mounted
inside an existing container**. It is not mounted-inode or content attestation;
explicit revision rotation and external immutability remain required. Identical
repeated checks and ordinary workload reads (atime only) retain the proof.

## Private file handling

Keys use the same protected, no-follow, operator-owned, single-link, regular-file
metadata validation as policy documents (0400 or 0600, no extended ACL), then
exact-length validation and clearing of the read buffer. **Both key and output**
must be outside the selected and fully resolved recipe directories, including
nested children. Recipe directory/file symlinks are resolved before exclusion;
private-path ancestry remains no-follow. Directory-identity checks also exclude
known workspace aliases. Missing, dangling or nonregular recipe paths and
unestablishable directories withhold proof. This is only the known recipe
workspace boundary, not a general Pkl/import or serving sandbox. Private sibling
state directories are supported and used by integration fixtures.
A key must not be policy/bindings or a known policy source;
metadata inode checks reject source aliases before key reads. Sources are never
opened for byte reads. All source entries, including absent versions, are also
excluded as output destinations (including observed directory aliases).
Metadata-only probes also inspect
unselected/unsafe catalog entries for aliases; unobservable, non-absent source
metadata withholds proof rather than risking a source read as key. These probes
do not authorize delivery through unsafe ancestry.

Output is written to a private unpredictable `O_EXCL|O_NOFOLLOW` temporary file
under a pinned protected parent, explicitly mode 0600, synced, then atomically
published with Linux `renameat2(RENAME_NOREPLACE)` and directory fsync. Existing
files, directories and symlinks are never overwritten. Rendering failure removes
only the newly published inode. Preexisting output remains untouched on error;
Brine must continue to demand a fresh filename per invocation. Unsupported
platform/filesystem behavior fails fixed, never falls back to overwrite.

## Evidence and remaining gates

- Independent Python-derived framing vector in `preview_test.go` and shared
  two-field wire fixture `internal/secretfile/testdata/preview-v1.json` (test key:
  32 bytes of 0x42; authority/config/live `{}`, plan `{"changes":[]}`; encoding
  evidence only, not runtime authorization); Linux private
  key/output/authority tests, including source-key/output exclusion, key rotation,
  stable repeats and no overwrite; CLI tests cover exact pairing, grants-only,
  drift exit completion and failure-without-publication.
- Planner tests cover stable observations, canonical object ordering, effective
  imported fields, target/policy/options, ownership/identity, mounts, namespaces,
  images, lifecycle, resource limits, network and volume changes; native HTTP
  fixtures exclude volatile fields on both adapters.
- `Alpha secrets / docker`, `/ podman-rootful`, `/ podman-rootless` CI jobs run the
  real candidate binary with protected per-run synthetic files, actual mapped
  non-root access and foreign-ID denial, independent chmod/write RO denial,
  stable proofs on repeated create/noop plans, equivalent and changed imports,
  lifecycle invalidation without uptime instability, valid same-reference source
  replacement invalidation, atime-independent reads, explicit revision rotation,
  missing/mode/symlink faults before pull/orphan/recreate, repaired inspect mount
  drift and source-independent cleanup retaining external files. Engine inventory
  plus an event stream proven to see a real pull checks zero mutation.
- Podman jobs compile exact upstream 5.0.0 source commit
  `e71ec6f1d94d2d97fb3afe08aae0d8adaf8bddf0` with pinned Go 1.22.12 for that old
  source, explicit build tags and vendored dependencies. crun 1.26 is built from
  pinned verified source as described below; other native distro helpers and
  their versions are recorded. No opaque Podman/crun binary is downloaded. The
  built executable uses the distro `/usr/bin/podman` AppArmor path on the
  disposable runner only. Baseline jobs are unchanged.
- Jobs require Linux >=6.5 and actual SO_PEERPIDFD success, record kernel and
  server versions, and use a dedicated protected `CONTAINERS_CONF` containing
  only `[containers] userns = "host"`. Rootless host IDs come from the exact
  socket peer's kernel maps, not an independent `unshare` assumption.

### OCI runtime compatibility correction

Run 43314699489 at `20ef7a7` established that both Podman variants passed
preflight, mapping, ownership, image and network phases, then failed native
create/start with `crun: unknown version specified`. Source inspection explains
that precise failure independently of the runtime's `spec: 1.0.0` banner:

- Pinned Podman `e71ec6f1d94d2d97fb3afe08aae0d8adaf8bddf0` calls
  `generate.New("linux")` in `pkg/specgen/generate/oci_linux.go`.
  Its vendored `github.com/opencontainers/runtime-tools/generate/generate.go`
  initializes `Spec.Version` from `rspec.Version`; the vendored
  `github.com/opencontainers/runtime-spec/specs-go/version.go` defines **1.2.0**.
  There is no version downgrade on that generation path.
- Ubuntu's observed crun **1.14.1**, upstream source
  `de537a7965bfbe9992e2cfae0baeb56a08128171`, rejects
  `def->oci_version && strstr(def->oci_version, "1.0") == NULL` in
  `src/libcrun/container.c` create/run/restore paths, producing exactly
  `unknown version specified`. OCI 1.2.0 fails that check.
- The qualification build now uses upstream crun **1.26**, tag target
  `3241e671f92c33b0c003cd7de319e4f32add6231`. That source no longer contains
  the 1.0-only rejection. Its parser submodule is pinned by the superproject:
  libocispec `bf749566cda632fb2f5dcf9c4eb5bbec71ac7d5f`, with runtime-spec
  `d64c1d945da7cf6970061c7c9ff4391fafdf2a15` (newer OCI schemas).
  Recursive submodules are checked out at recorded gitlinks, never floating
  remote branches. The tag's peeled commit is verified before building, and
  submodule identities, executable version and SHA-256 are logged.

`scripts/ci-build-podman-alpha.sh` builds with seccomp/systemd enabled and installs
root-owned mode-0755 `/usr/bin/crun` only on the disposable qualification runner.
The service retains its exact Podman 5.0.0 argv and host-userns-only private
`CONTAINERS_CONF`. The actual dedicated server must report runtime name `crun`,
path `/usr/bin/crun`, and executable version `crun version 1.26`; a client binary
or stale dpkg package label is not accepted as proof of selection. The package
database still describes distro crun, so its package version is not the built
executable's version. Compatibility reasoning concerns acceptance of this
profile's OCI 1.2.0 input, not a blanket OCI conformance claim based on a banner.
Actual create/inspect/access/RO tests remain mandatory; source audit alone is
not a successful runtime qualification.

The daemon and client test PATH now includes `/usr/sbin` and `/sbin`, retaining
`/usr/bin` first for the pinned executables. The runner explicitly resolves and
prints versions for `iptables`, `ip6tables` and `ip`. Docker's existing isolated
bridge/firewall/forwarding flags are unchanged. This corrects executable lookup
for resolver/network helpers; it is **not** asserted to explain the separate
paired harness cleanup error for intentionally retained networks.

Initial published `ff4d11a` CI (run 43306923484) passed Linux unit/metadata and
Docker Alpha qualification. The later diagnostic run identified the Podman
runtime mismatch above, and `20ef7a7` passed the source-stat/workspace unit tests.
Run 43319430110 at `ffbca6c` subsequently passed baseline jobs and Alpha Docker;
both Podman jobs built and selected exact crun 1.26 successfully, then failed
the restored-lifecycle proof equality assertion. That assertion's old wording
("volatile lifecycle counters entered proof") did not identify the differing
HMAC component. Pinned Podman's `libpod/container_inspect.go` builds inspect
configuration/host fields from `specFromState()` as well as stored configuration;
reading those definitions does not establish which normalized fact changed in
this run. Full Podman qualification remains pending, not established by the
successful runtime build or the earlier create/access/proof phases.

A test-only diagnostic now captures the four private HMAC frames immediately
after the actual CLI proof, before stop, while stopped, and after restart. Its
copied authority shape is checked against production publication in a regression
test; each real-engine snapshot must also reproduce that phase's actual CLI MAC
before its diff can be interpreted as exact evidence. This includes validated
source device/inode, size, mtime/ctime (seconds/nanoseconds), UID/GID, mode and
link count, rather than assuming only live counters changed. The read-only probe
uses the same evaluator, preflight and planner with the same options. It has no
engine mutations, readiness polling, retries or replacement approval baseline.
Snapshots stay in exclusive protected fixture files and are removed with the
fixture. Logs contain only a stage, MAC-match boolean and up to 64 changed field
paths; dynamic keys are masked. No source bytes, values/value hashes, keys,
proofs or canonical frame values are printed. The original stop, restart,
immutable-ID and valid-replacement assertions remain enforced. If a snapshot
cannot match the just-published CLI proof, the probe explicitly fails and its
fresh observations must not be mistaken for exact CLI evidence. Next disposable
CI must establish the changed fields before a product or test assumption fix.

A separate CI-only test-binary probe replays the same independently checked adapter
phases on synthetic fixtures after a failed Podman create, reporting bounded
native SDK causes. It does not replace real CLI acceptance or relax production
errors, preflight, namespace or RO settings. Dedicated daemon logs are collected
on failure with synthetic-value/hash filtering; no source bytes are read by the
probe. Both Podman jobs must pass before merge. Paired Brine candidate/old-binary
qualification and final-head independent review also remain required.
No production or unverified local engine was mutated during implementation.
