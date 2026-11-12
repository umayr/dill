# Alpha secret preflight implementation (Stage A)

This is the internal preflight layer, **not** an advertisement of the Alpha
feature. CLI/adapters, lifecycle routing and preview proofs are separate stages.
The existing rejection of secret delivery stays in place. No engine was mutated
to develop or test this stage.

## Entry point

`internal/secretfile.Preflight(ctx, Request) (*Result, error)` is the one entry
point for a checked operation. `Request` supplies the evaluated configuration,
operator policy and deployment binding absolute paths, explicit target, stack,
engine and canonical socket, service selection and `NoDeps`.

- Policy selection must come from the trusted launcher's `DILL_SECRET_POLICY`,
  never Pkl or a binding. Provision policy outside the editable checkout and
  service configuration independently of it. Inputs are trusted metadata, not
  secret values.
- Both metadata documents must be operator-owned, single-link regular files,
  exactly 0400 or 0600, without an extended access ACL. Reads are bounded to
  64 KiB and checked against descriptor metadata before/after reading.
- Directory traversal is descriptor-relative and no-follow, from `/`, with
  root/operator ownership, no group/other writes and no access/default ACL.
  Sources use the same traversal; their contents are **never read**. Traversal
  and source inspection use `O_PATH` descriptors. ACL metadata is queried via
  the kernel `/proc/self/fd` link to that pinned inode, without reopening a source
  for byte reads. This supports 0400 sources owned by mapped non-root IDs even
  when the operator itself lacks source read permission. Only a validated private
  regular metadata document is reopened for the bounded JSON/TOML read.
- The whole declared graph, binding shape and configuration authorization are
  checked, including unselected services. Only the requested closure (or roots
  with `NoDeps`) requires runtime source availability. Empty selection means all.
- `AuthorizationOnly` performs document/graph/authorization checks only. It does
  not contact the engine or inspect sources, and cannot supply delivery mounts.
- Full checks have a ten-second context deadline for socket/HTTP operations,
  bounded HTTP headers/body, no redirects, no ambient proxy or engine settings,
  no image pull or engine mutation. A hung host filesystem remains a host/kernel
  availability issue; a context does not interrupt arbitrary filesystem syscalls.
- Every failure is exactly `config.ErrSecretUnavailable`, without wrapping,
  input echoes, paths, diagnostics or SDK logging.

`Result` has private fields. `AvailabilityChecked()` is point-in-time evidence,
not a lock or lease. Nil/zero results fail closed. `Matches(Request)` reauthorizes
current documents and compares target/engine/socket/stack, input paths, selected
services and resolved references/permissions. `Mounts(Request)` additionally
requires a full runtime check and returns copies only for that matching request.
It does not replace a fresh `Preflight` immediately before creation. Returned
`config.ResolvedSecret` paths are private adapter inputs, not public output.
Low-level `Check`, `Mapping` and `ParseIDMap` are metadata utilities, **not**
standalone runtime authorization; callers should use `Preflight`.

Adapters must use the exact verified explicit endpoint, leave the supported
normal user namespace unchanged, reject per-container userns/idmap overrides,
and enforce read-only host-file mounts. This stage does not wire those adapters.

## Linux runtime prerequisites

These restrictions apply only to secret runtime operations, not ordinary Dill:

- Linux >=6.5, with `SO_PEERPIDFD`, readable local procfs, namespace ioctls and
  ACL-capable protected host filesystems. There is no `pidfd_open(numeric PID)`
  fallback: it would leave a PID-reuse gap after `SO_PEERCRED`.
- The caller must see host identity uid/gid maps. The explicit Unix socket is
  pinned with `O_PATH` and connected through its descriptor, not independently
  resolved through an SDK connection pool. Its owner must equal the verified
  daemon peer's host UID. TCP/SSH/implicit endpoints and Darwin fail closed.
- Saved peer credentials, an exact peer pidfd, process start time, all real/
  effective/saved/fs credentials, executable inode, namespace inode and uid/gid
  maps are checked. Process identity/maps are rechecked after the bounded info
  request on that same connection. Executables must be protected local `dockerd`
  or `podman` regular files. Socket activation/proxy peers are unsupported.
  The operator must have enough privilege to read the peer's proc metadata;
  Docker-group membership alone is not a substitute when procfs denies access.
- Rootful daemons must share the caller's identity user namespace. Docker info
  must identify Linux and supply security options without rootless/userns
  remapping; Desktop targets are rejected. Docker's SDK version is **not** a
  daemon-version gate. CI must record its actual daemon version.
- Rootless Podman must be a same-operator-UID peer in a direct child user
  namespace; `NS_GET_PARENT` must establish that relationship. Peer root UID/GID
  must map to its socket credentials. `/proc/<peer>/uid_map` and `gid_map`, read
  from the caller's host namespace, must agree with Podman's reported mappings.
  Every requested ID must translate uniquely to the exact policy/file host ID.
  Overlapping, malformed, unmapped or unsupported mappings fail closed.

The peer must have the same filesystem root inode as the caller. This does not
cryptographically authenticate a daemon or attest all mounts. The trusted
operator must ensure it really serves the local host, exclude VM/proxy filesystem
indirection and prevent socket/policy/source replacement. Source immutability
continues for container lifetime. No historical inode/content attestation is
claimed.

## Dedicated qualified Podman service

Podman `/info` does **not** expose the default container user namespace from
`containers.conf`. Peer maps alone cannot establish default container mapping.
Stage A therefore intentionally qualifies only a dedicated **Podman server
5.0.0**, not merely a client built with the v5.0.0 SDK, with this exact launch:

```sh
CONTAINERS_CONF=/srv/dill-alpha/containers.conf \
  /usr/bin/podman system service --time=0 unix:///run/user/1000/dill-alpha.sock
```

Use the actual protected executable path, host UID and protected socket path.
Rootful Podman uses the same qualified launch/config, with a root-owned config
and socket. The daemon config is an independently provisioned private 0400/0600
file owned by the daemon's host UID with protected ancestry and exactly these
supported TOML settings (whitespace/comments are allowed):

```toml
[containers]
userns = "host"
```

`host` here inherits the **daemon** namespace; for the rootless service it is
not the initial host user namespace. Dill does not set or modify this property.
The service config, selectors and launch must be immutable **from daemon startup
through the operation and container lifetime**. Restart a freshly qualified
service after changing configuration; reading its current file cannot establish
what an already-running daemon cached before an administrator changed it.

No global/service flags other than those shown are accepted. In particular,
`--module`, override/userns/remote selectors and socket activation are rejected.
The daemon environment must have a single absolute `CONTAINERS_CONF` and no
`CONTAINERS_CONF_OVERRIDE`, other `CONTAINERS_CONF*` selectors, `PODMAN_USERNS`,
`CONTAINER_HOST` or `CONTAINER_CONNECTION`, even if empty. No arbitrary daemon
environment or command line is returned, logged or retained in the result.

The source basis is the pinned upstream **server** v5.0.0 and its
containers/common v0.58.0 dependency:

- `common/pkg/config/new.go:systemConfigs` replaces system/user/drop-in files
  when `CONTAINERS_CONF` is set. `newLocked` then applies command-line modules
  and `CONTAINERS_CONF_OVERRIDE`; both are excluded above.
- `podman/cmd/podman/registry/config.go` obtains modules from global argv.
- `podman/pkg/specgen/generate/container_create.go` resolves omitted `UserNS`
  through `GetDefaultNamespaceMode`; `generate/namespaces.go` reads the config.
- `podman/pkg/specgen/namespaces.go:SetupUserNS` removes the OCI user namespace
  for `host`; `pkg/util/utils.go:ParseIDMapping` leaves host mapping enabled
  with no per-container map overrides. The container therefore inherits the
  daemon namespace under this narrowly qualified launch.
- `podman/libpod/info_linux.go` reports daemon `/proc/self` maps through
  `libpod/define.Info`; `containers/storage/pkg/idtools.IDMap` uses actual wire
  keys `container_id`, `host_id`, `size`. The info request is
  `GET /v5.0.0/libpod/info`, not a hypothetical spec/defaults endpoint.

### Disposable CI qualification gate (not executed on macOS)

Existing distro Podman jobs do not qualify this target automatically: Ubuntu
24.04 commonly ships 4.9.x, and SDK versions prove nothing about the server.
The isolated rootful/rootless Alpha jobs must install upstream **server v5.0.0**
from its pinned source/release, build `make bin/podman` with upstream documented
Linux dependencies, and record the exact checkout commit, artifact checksum and
`podman version`. Launch that installed binary with the dedicated configuration
above (not the distribution or systemd socket), on a >=6.5 kernel. Do not run
these provisioning instructions on an unverified production/local engine.

Before claiming rootless support, those jobs must prove mapped non-root UID/GID
read access and denial/read-only enforcement through both create and inspect,
and reject auto/keep-id, modules/override selectors, proxies, wrong/unmapped IDs
and config-layer ambiguity. Record Docker's actual server version separately.
Run the Linux synthetic unit tests in a protected workspace on the runner,
including an isolated root unit-test invocation for
`TestSourceMetadataWithoutReadPermission` (it drops only a synthetic child
process's UID). Treat unexpected metadata/ACL skips as a qualification failure.

Widening Podman server versions requires inspecting their config precedence and
namespace implementation plus equivalent disposable tests; do not just loosen
the version comparison. Compilation and synthetic fixtures are not that proof.
