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

The README and `examples/remote/compose.pkl` reference
`package://pkg.ushd.ca/dill@0.5.0#/dill.pkl`. On 2026-11-18, the registry's
public HTTPS endpoint returned 404 for the metadata and its checksum. Both
`PKL=/path/to/pinned/pkl python3 scripts/smoke-schema.py 0.5.0` and evaluation of
`examples/remote/compose.pkl` with Pkl 0.31.1 and a fresh cache failed. The
metadata in the registry source repository and the GitHub release archive
still matched their published SHA-256 files and each other. That confirms the
artifacts exist; it does not confirm the package URI resolves.

Use the checked-in schema examples while discovery is unavailable. Do not
republish an existing version or switch to another version based on archive
availability alone. Once public discovery is restored, rerun the HTTPS smoke
check with the pinned Pkl before treating the package as usable from a fresh
cache. These schema releases are not the experimental roadmap model; no
`@NEXT` package is promised.
Existing historical archives included the source tree; this change does not
alter or republish them. Users relying on non-schema files inside an old archive
must instead obtain those files from source; future archives contain only
`dill.pkl`. First-use Pkl package checksums do not authenticate the publisher.

## Release procedure and recovery

The tag-triggered workflow stages only `PklProject` and `dill.pkl` in a temporary
directory, checks the resulting zip's exact allowlist, and publishes release
assets without replacement. Before uploading assets or publishing discovery, it
verifies the local archive allowlist, both checksums, and the metadata's version,
URI, archive URL and archive digest. It then compares every existing asset byte-for-byte, uploads missing
assets, verifies them, and only then commits both discovery
metadata files to `umayr/pkl-registry` in one normal Git push. An existing
registry file with different bytes, pre-existing staged registry changes, a
changed release asset, a race, permission failure, or non-fast-forward push
fails closed. Publication is serialized within this repository; external writers
and manual edits are not made impossible by
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

Run `make validate PKL=/path/to/pinned/pkl` for packaging, allowlist and
reproducibility checks, mocked immutable publication/retry tests, schema/example
evaluation and design fixtures. This passed with Pkl 0.31.1 on 2026-11-18 even
though the clean-cache smoke failed: ordinary example evaluation may reuse the
local package cache. For #17, the local packaging and simulated immutable
publication/retry checks pass; clean-cache public discovery remains blocked,
and no live publication or retry was attempted. These checks do not prove
engine runtime semantics.
