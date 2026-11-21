# Snapshot staging admission budget

Snapshot uploads to the authenticated `dill-agent` HTTPS listener and to local
`dill-serve` share the same receiver rules. Per private staging root, a new stage
must fit within **512 MiB of logical staged file data** and **64 stage trees**.
These are fixed limits in this slice, in addition to the existing per-upload
limits. Capacity is checked before the receiver creates a directory or writes
upload data to disk.

- **Bytes:** the sum of existing regular-file lengths (`stat` size, including
  holes in sparse files), plus the decoded lengths of the candidate files. This
  is not allocated-block, metadata, memory, or free-disk-space accounting.
- **Count:** each `snapshot-<id>` and `.in-progress-<id>` directory consumes one
  slot, even when it has no files. IDs are 32 lowercase hexadecimal characters.
  Uncatalogued, partially written, and publication-unknown trees all count;
  partial trees are charged for the data currently present. Catalog records and
  record remnants live separately and are **not** included in this budget.

The receiver takes a non-blocking exclusive advisory lock on the opened root
inode, inventories that same root, and holds the lock through publication and
sync or the failed request's existing identity-checked cleanup. Independently
opened receivers (including separate cooperating processes) for that root cannot
both admit against the same inventory. No lock file or accounting database is
needed: every admission re-reads the retained filesystem state, including after
restart. There is no adoption or catalog repair on scan. Read-only manifest
lookup does not need spare staging capacity.

Use a dedicated private staging root, separate from the catalog, with stable,
trusted ancestors and a local filesystem that honors directory `flock`, rename,
and sync. All writers must cooperate; older binaries, direct operator edits,
aliasing through mounts, and same-UID tampering are outside the guarantee. Do not
nest staging and catalog roots or put other data or mounts inside a staging
root. The bounded inventory does not follow symlinks and refuses unexpected
root entries, non-private entries, unsupported file types, hard-linked files,
cross-device entries, uninspectable trees, and trees exceeding the receiver's
implementation file/directory/depth ceilings. It does not read file contents,
validate catalog provenance, or qualify filesystem semantics.

An over-budget `POST /v1/snapshots` returns **507** (`staging_capacity`), a held
admission lock returns **429** (`busy`), and an unaccountable inventory returns
the existing sanitized **500** (`staging_failed`). None publishes a stage. A
lost response still has unknown outcome; do not automatically retry it. Data
already present is not deleted, quarantined, or rewritten, even if the root is
already over budget. This is not a filesystem quota, retention policy,
active-use tracker, or garbage collector. Continue to monitor free space. Any
reconciliation or reclamation is an explicit operator action; stop writers and
verify that material is not in use before changing it.
