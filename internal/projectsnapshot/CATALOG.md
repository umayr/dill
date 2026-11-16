# Snapshot catalog

The service optionally uses `Catalog.Receive` to publish a snapshot through the
catalog's anchored staging directory and register it before returning success.
This confers no deployment authority. `RegisterReceived` must only be used after
a trusted receiver has successfully published and durably synced the exact
snapshot. Never register directories discovered by scanning: unregistered
canonical stage names are UNKNOWN. Handle `ErrCatalogUnknown` as an ambiguous
result, without retrying or removing any stage, temporary file, or record. No
catalog operation deletes anything.

The two roots must already exist, be private, owned by the effective UID and
separate. Both roots, ancestors and all same-UID processes are operator-trusted.
Directory descriptors and device/inode checks detect ordinary root and stage
replacement; they are not hostile same-UID attestation or protection against
inode reuse, mounts, or modification after verification. Callers must coordinate
writers and consumers; lookup is a point-in-time check, not a pinned immutable
filesystem snapshot. Do not interpret stage identity as consent to deploy.

Records contain a canonical manifest with exact entrypoint, paths, lengths and
SHA-256, its digest, stage ID and root/stage device/inode identities. Publication
writes a new random temporary record, syncs it, renames without replacement and
syncs the catalog directory. Rename or subsequent failure is UNKNOWN. File and
rename durability depends on filesystem/OS sync semantics. Temporary records
are intentionally retained on all failures, including cancellation. Lookup
validates the bounded canonical record and streams and rehashes the complete
stage tree via no-follow descriptor-relative operations. Stage directories and
files must be private and owned by the effective UID; files must have one link.
Unexpected, missing, non-regular or corrupted entries fail closed. Unregistered
IDs return `ErrStageUnknown`; discovery and inventory are not implemented.
A future inventory must provide resumable progress and bounded total work; no
scan may infer that foreign stages were published. No cross-process mutual
exclusion is supplied, so trusted callers must serialize cooperating operations
as required; no-replace publication prevents overwriting an existing record.

The receiver syncs files and directories before publication and the staging
root after publication. Ambiguous publication or registration outcomes retain
the stage for operator inspection. These guarantees depend on the documented
trusted-root and filesystem assumptions; see `docs/snapshot-staging.md`.
