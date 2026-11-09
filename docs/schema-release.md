# Schema packaging and releases

`internal/loader/pkl-toolchain.json` is the single Pkl version and asset digest
source. Runtime embeds it; `scripts/install-pkl.py` consumes the same file for
CI, release and local installations. Digests for 0.31.1 were taken from the
`digest` fields of the official [Apple Pkl GitHub release API](https://api.github.com/repos/apple/pkl/releases/tags/0.31.1),
not calculated from the binary being installed. To upgrade, independently review
the upstream release and update the version and **all** supported asset digests
together, then run the package, example and design checks with the new binary.
The digests authenticate bytes relative to this checked-in trust decision; they
are not a separate signature or protection against a compromised upstream account.
Only listed platforms can use the managed downloader. Explicit `DILL_PKL_PATH`
and PATH installations remain operator-trusted. Cached managed binaries are
versioned and checked before use; legacy cache files are left untouched.
Legacy checksum and unverified-download environment variables now fail explicitly.

## Published URLs and migration

The README and `examples/remote/compose.pkl` use
`package://pkg.ushd.ca/dill@0.5.0#/dill.pkl`. v0.5.0 is a real GitHub release;
its HTTPS metadata, metadata checksum, release archive, archive checksum, and
clean-cache Pkl evaluation were checked using Pkl 0.31.1. The previous remote
example referenced 0.4.0, which is also a real release with live metadata; users
are not automatically upgraded. Test and explicitly update their package URI
and any Pkl project dependency/lockfile as appropriate. These are current schema
releases, not the experimental roadmap model; no `@NEXT` package is promised.
Existing historical archives included the source tree; this change does not
alter or republish them. Users relying on non-schema files inside an old archive
must instead obtain those files from source; future archives contain only
`dill.pkl`. First-use Pkl package checksums do not authenticate the publisher.

## Release procedure and recovery

The tag-triggered workflow stages only `PklProject` and `dill.pkl` in a temporary
directory, checks the resulting zip's exact allowlist, and publishes release
assets without replacement. It first compares every existing asset byte-for-byte,
then uploads missing assets, verifies them, and only then commits both discovery
metadata files to `umayr/pkl-registry` in one normal Git push. An existing
registry file with different bytes, a changed release asset, a race, permission
failure, or non-fast-forward push fails closed. Publication is serialized within
this repository; external writers and manual edits are not made impossible by
GitHub or Git. Protect release assets, registry branch, and the registry token.
Do not delete or replace existing assets to make a retry pass. Re-run the same
tag workflow with identical source/toolchain after a partial failure; it verifies
existing objects and fills missing ones. A mismatched version requires a new
version or operator investigation, never an overwrite. If a rebuild is not
byte-identical (including executable binaries), retry deliberately fails.

After discovery publication the workflow checks actual HTTPS metadata and its
checksum, fetches and checks the archive checksum, then evaluates a synthetic
module with an empty Pkl cache. CDN propagation is retried. A failing smoke
leaves published objects in place for investigation and safe retry, rather than
silently altering them. `PKL=/path/to/pkl python3 scripts/smoke-schema.py VERSION`
can perform the same read-only check for an already published release. No new
release has been published or smoke-verified as part of this change. Manual
catalog publication, mirrors, publisher authentication, and cross-repository
atomicity are not provided.

Run `PKL=/path/to/pkl make validate` for pinned-version packaging, allowlist and
reproducibility checks, mocked immutable publication/retry tests, schema/example
evaluation and design fixtures. These do not prove engine runtime semantics.
