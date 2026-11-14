# Named preflight and fresh checked apply

`preflight-capabilities --format json` is engine-free discovery. The positive
`dill.preflight/1` profile is available only to a native, co-located Linux Docker
host with an explicitly selected local socket, independently provisioned daemon
identity and a shared operator-owned `0700` coordination directory. Unsupported
platforms return only `version` and `supported: false`. Discovery does not
itself establish that a particular target is qualified.

For a full-stack action (`up`, `start`, `restart`, `stop`, or `down`):

```sh
dill preflight up --contract dill.preflight/1 --format json \
  -f ./compose.pkl -p example --engine docker --engine-socket unix:///run/docker.sock \
  --coordination-layout native-local-docker-v1 --coordination-daemon-id "$EXPECTED_DAEMON_ID" \
  --coordination-state-dir "$PRIVATE_STATE_DIR" --coordination-source cli \
  --no-prune --pull never

# Pass selectionDigest from a complete successful review verbatim, once.
dill operation up --contract dill.operation/1 --format json \
  -f ./compose.pkl -p example --engine docker --engine-socket unix:///run/docker.sock \
  --coordination-layout native-local-docker-v1 --coordination-daemon-id "$EXPECTED_DAEMON_ID" \
  --coordination-state-dir "$PRIVATE_STATE_DIR" --coordination-source cli \
  --no-prune --pull never --preflight-contract dill.preflight/1 \
  --expect-preflight "$REVIEWED_SELECTION_DIGEST"
```

The digest includes `sha256:`. Use the same action, config, target, stack,
source, duration and effective flags for the review and apply. Binding flags
must be supplied together. Both commands reject service operands, dry-run,
secrets, env files, `--no-deps`, confirmation flags and unsupported options;
`--force-recreate`, `--no-prune`, and `--pull` apply only to `up`. The supported
pull policy is `never`; images must already be local, identifiable and free of
image `VOLUME` declarations. Up rejects undeclared orphans before writes even
when `--no-prune` is omitted. The existing unbound operation CLI is unchanged.

A successful review includes all declared container names, the stack network,
and deduplicated declared attached named volumes. Absent rows are namespace
references, not promises that creation will occur. `ensure-started` can start
an existing container, and create/recreate can ensure volumes and clean up newly
created containers on failure. A recreate/remove can lose the container's
writable layer. Volumes and networks are retained; volume identity does not prove
contents or detect indistinguishable same-name replacements. Detached volumes
are outside the review.

Apply freshly rechecks and compares the complete selection under the retained
qualified connection and coordination locks before engine mutation. A mismatch
returns `PREFLIGHT_MISMATCH` with no engine effects; it does not automatically
preview, retry or authorize a replacement. Locks coordinate cooperating callers,
not external writers; later races or failures can still cause partial effects.
Review is not a reservation, authorization, transaction, backup or guarantee of
success. Runtime running/health state is intentionally not committed. The
selected socket spelling and private state-store identity are bound; use the
same selectors for apply. Preview writes coordination metadata even though it
denies engine mutations. Treat unknown metadata/finalization outcomes as an
inspection barrier rather than confirmation.

Down preserves the existing conservative behavior: a retained existing network
can make apply fail after removing containers (failed/partial, inspection
required), or fail with no container effects for an empty stack. A successful
preview does not change this behavior. This profile does not support binds,
external/anonymous/custom storage, secrets, privileged/special namespace or
capability settings, image pulls, arbitrary inventory, Podman, Docker Desktop,
remote proxies or a VM-hosted daemon. The operator must use synthetic disposable
data and must not treat hashes as permission to expose secret material.
